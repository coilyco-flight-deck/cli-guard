// Path defaults and per-repo audit derivation, rooted at the consumer's
// app dir (see appdir.go) so cli-guard hardcodes no consumer's directory.
package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// AuditSubdir is the subdirectory under the global dir where per-repo audit
// logs live. One JSONL file per repo slug.
const AuditSubdir = "audit"

// SessionsSubdir is the subdirectory under AuditSubdir where per-session
// state lives. One directory per CLAUDE_CODE_SESSION_ID. Currently holds
const SessionsSubdir = "sessions"

// SessionProfileFile is the basename of the per-session sentinel file
// containing the active lockdown profile name. Plain text, one line.
const SessionProfileFile = "profile"

// UnrootedAuditName is the slug used when the consumer is invoked outside any git
// repo (or inside one with no origin remote). All such invocations land in
const UnrootedAuditName = "_unrooted"

// GlobalDir returns ~/<app-dir> (e.g. ~/.coily), expanded against $HOME.
// Returns an error only if $HOME cannot be resolved.
func GlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home: %w", err)
	}
	return filepath.Join(home, AppDir()), nil
}

// GlobalConfigPath returns ~/<app-dir>/config.yaml.
func GlobalConfigPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// LocalConfigPath returns ./<app-dir>/config.yaml relative to the current
// working directory. The file may or may not exist.
func LocalConfigPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("config: getwd: %w", err)
	}
	return filepath.Join(cwd, AppDir(), "config.yaml"), nil
}

// SessionDir returns ~/.coily/audit/sessions/<sessionID>. Caller is
// responsible for MkdirAll. Returns an error if sessionID is empty or
func SessionDir(sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("config: session id is empty")
	}
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AuditSubdir, SessionsSubdir, sessionID), nil
}

// SessionProfilePath returns the per-session profile sentinel file path
// for sessionID. Errors propagate from SessionDir.
func SessionProfilePath(sessionID string) (string, error) {
	d, err := SessionDir(sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, SessionProfileFile), nil
}

// DefaultAuditPath returns ~/.coily/audit/<slug>.jsonl, where slug is
// derived from the current git repo's origin remote. Falls back to
func DefaultAuditPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AuditSubdir, RepoAuditSlug()+".jsonl"), nil
}

// ExpandHome turns a leading "~/" or "~" into the user's home directory.
// Returns the input unchanged if it doesn't start with "~" or if $HOME
func ExpandHome(p string) string {
	if p == "" || !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// repoSlugCache memoizes the result of RepoAuditSlug for a single process.
// Audit append happens once per invocation, but the slug resolver shells out
var (
	repoSlugCache    string
	repoSlugCacheSet bool
	repoSlugMu       sync.Mutex
)

// slugSanitizer collapses everything outside [a-z0-9-] into a single dash.
var slugSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)
var slugDashRun = regexp.MustCompile(`-+`)

// RepoAuditSlug returns the audit slug for the current working directory.
// Format: "<owner>-<repo>" lowercased and reduced to [a-z0-9-]. Returns
func RepoAuditSlug() string {
	repoSlugMu.Lock()
	defer repoSlugMu.Unlock()
	if repoSlugCacheSet {
		return repoSlugCache
	}
	slug := resolveRepoSlug()
	repoSlugCache = slug
	repoSlugCacheSet = true
	return slug
}

// ResetRepoSlugCacheForTest clears the cache. Test-only.
func ResetRepoSlugCacheForTest() {
	repoSlugMu.Lock()
	defer repoSlugMu.Unlock()
	repoSlugCache = ""
	repoSlugCacheSet = false
}

func resolveRepoSlug() string {
	origin, err := gitOriginURL()
	if err != nil || origin == "" {
		return UnrootedAuditName
	}
	owner, repo, ok := parseOwnerRepo(origin)
	if !ok {
		return UnrootedAuditName
	}
	return SanitizeSlug(owner + "-" + repo)
}

// SanitizeSlug normalizes input to lowercase, replaces every non-[a-z0-9-]
// run with a single dash, and trims leading and trailing dashes. Exported
func SanitizeSlug(s string) string {
	s = strings.ToLower(s)
	s = slugSanitizer.ReplaceAllString(s, "-")
	s = slugDashRun.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return UnrootedAuditName
	}
	return s
}

// gitOriginURL shells out to `git remote get-url origin`. A 2-second timeout
// keeps a hung git from blocking every invocation. Returns the empty
func gitOriginURL() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parseOwnerRepo extracts owner and repo from a git remote URL. Handles:
func parseOwnerRepo(remote string) (string, string, bool) {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	remote = strings.TrimSuffix(remote, "/")

	// scp-style: git@host:owner/repo
	if strings.Contains(remote, "@") && !strings.Contains(remote, "://") {
		if idx := strings.Index(remote, ":"); idx != -1 {
			path := remote[idx+1:]
			return splitOwnerRepo(path)
		}
	}

	// URL form
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err == nil && u.Path != "" {
			return splitOwnerRepo(strings.TrimPrefix(u.Path, "/"))
		}
	}

	// Bare path
	return splitOwnerRepo(remote)
}

func splitOwnerRepo(path string) (string, string, bool) {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner := parts[len(parts)-2]
	repo := parts[len(parts)-1]
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}
