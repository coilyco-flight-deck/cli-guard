// Package gittree inspects a repo's working tree for the clean+synced state
// that gates a repo's allowlist verbs. The gate refuses repo-verb
package gittree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// State is the outcome of CheckClean. Clean is true when every gate property
// holds. When false, Reason names the first failure in human-readable form
type State struct {
	Clean    bool
	Reason   string
	Recovery string
	Status   string
	// DirtyPaths is every working-tree path that git porcelain v1 reported
	// as dirty (modified, added, deleted, untracked, renamed - both halves
	DirtyPaths []string
	Branch     string
	Upstream   string
	Ahead      int
	Behind     int
}

// MaxStatusBytes caps Status so a sprawling untracked tree does not bloat
// the audit row or refusal message.
const MaxStatusBytes = 2048

// ErrNotGitRepo is returned when the supplied path is not inside a git repo.
// Repo verbs only fire when a repocfg file was discovered, which itself implies
var ErrNotGitRepo = errors.New("gittree: path is not inside a git repo")

// CheckClean evaluates the gate at repoRoot. Returns a *State whose Clean
// field tells the caller whether the repo verb may run. A non-nil error is
func CheckClean(repoRoot string) (*State, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("gittree: git binary not found on $PATH: %w", err)
	}
	if out, gerr := runGit(repoRoot, "rev-parse", "--is-inside-work-tree"); gerr != nil || strings.TrimSpace(out) != "true" {
		return nil, fmt.Errorf("%w: %s", ErrNotGitRepo, repoRoot)
	}

	status, err := runGit(repoRoot, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return nil, fmt.Errorf("gittree: git status: %w", err)
	}
	branch, err := runGit(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("gittree: git rev-parse HEAD: %w", err)
	}

	st := &State{
		Status:     truncate(status, MaxStatusBytes),
		DirtyPaths: parseDirtyPaths(status),
		Branch:     strings.TrimSpace(branch),
	}
	if checkLocalState(st, status) {
		return st, nil
	}
	if checkUpstreamState(st, repoRoot) {
		return st, nil
	}
	return st, checkAheadBehind(st, repoRoot)
}

// checkLocalState fills st with a refusal reason from local-only signals
// (dirty tree, detached HEAD). Returns true when a refusal was set.
func checkLocalState(st *State, status string) bool {
	if status != "" {
		st.Reason = "working tree is dirty"
		st.Recovery = recoveryDirty(st.Status)
		return true
	}
	if st.Branch == "HEAD" {
		st.Reason = "HEAD is detached (no branch)"
		st.Recovery = "  git checkout <branch>\n"
		return true
	}
	return false
}

// checkUpstreamState resolves the branch's upstream and runs `git fetch`.
// Sets a refusal reason on st when the branch has no upstream or fetch
func checkUpstreamState(st *State, repoRoot string) bool {
	upstream, err := runGit(repoRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		st.Reason = fmt.Sprintf("branch %q has no upstream", st.Branch)
		st.Recovery = fmt.Sprintf("  git push -u origin %s\n", st.Branch)
		return true
	}
	st.Upstream = strings.TrimSpace(upstream)
	if _, ferr := runGit(repoRoot, "fetch", "--quiet", "--", remoteOf(st.Upstream)); ferr != nil {
		st.Reason = fmt.Sprintf("git fetch failed for %s", remoteOf(st.Upstream))
		st.Recovery = fmt.Sprintf("  git fetch %s\n", remoteOf(st.Upstream))
		return true
	}
	return false
}

// checkAheadBehind populates st.Ahead/st.Behind from `git rev-list`. Sets
// st.Clean=true when the branch is not behind upstream. Returns a non-nil
func checkAheadBehind(st *State, repoRoot string) error {
	revs, err := runGit(repoRoot, "rev-list", "--left-right", "--count", "HEAD..."+st.Upstream)
	if err != nil {
		return fmt.Errorf("gittree: git rev-list: %w", err)
	}
	ahead, behind, err := parseAheadBehind(revs)
	if err != nil {
		return err
	}
	st.Ahead = ahead
	st.Behind = behind
	if behind > 0 {
		st.Reason = fmt.Sprintf("%d commits behind %s", behind, st.Upstream)
		st.Recovery = "  git pull --ff-only\n"
		return nil
	}
	st.Clean = true
	return nil
}

// FormatRefusal renders the human-readable refusal message for a non-clean
// State, naming the verb so the operator's recovery suggestion includes
func (s *State) FormatRefusal(verbName string) string {
	if s.Clean {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "refusing repo verb %q - %s\n", verbName, s.Reason)
	if s.Status != "" && strings.HasPrefix(s.Reason, "working tree") {
		b.WriteString(s.Status)
		if !strings.HasSuffix(s.Status, "\n") {
			b.WriteString("\n")
		}
	}
	// One rationale per refusal cause. Quoting the clean-tree rule at the wrong
	// operator misdiagnoses the failure (ward#1129).
	b.WriteString("\n")
	b.WriteString(s.rationale())
	b.WriteString(" so the audit log can be\nreconstructed from git history. Recover with:\n\n")
	b.WriteString(s.Recovery)
	fmt.Fprintf(&b, "  %s %s   # retry\n", filepath.Base(os.Args[0]), verbName)
	b.WriteString("\nOverride for genuine emergencies with --audit-override-dirty.\n")
	b.WriteString("The audit row is tagged audit_override=true and captures the working\n")
	b.WriteString("tree status so the run can still be reconstructed after the fact.")
	return b.String()
}

// rationale names the gate property the refusal is enforcing, matched to the
// Reason that fired, so the explanation never cites the wrong rule.
func (s *State) rationale() string {
	switch {
	case strings.HasPrefix(s.Reason, "working tree"):
		return "Repo verbs require a clean tree"
	case strings.HasPrefix(s.Reason, "HEAD is detached"):
		return "Repo verbs require a branch checkout"
	default:
		return "Repo verbs require the branch to be pushed and synced with its upstream"
	}
}

// parseDirtyPaths extracts the working-tree paths from git porcelain v1
// output. Each non-empty line is "XY <path>" or "XY <orig> -> <new>" for
func parseDirtyPaths(porcelain string) []string {
	if porcelain == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		rest := line[3:]
		if arrow := strings.Index(rest, " -> "); arrow >= 0 {
			paths = append(paths, unquoteStatusPath(rest[:arrow]), unquoteStatusPath(rest[arrow+4:]))
			continue
		}
		paths = append(paths, unquoteStatusPath(rest))
	}
	return paths
}

// unquoteStatusPath strips the optional surrounding quotes that git's
// porcelain output uses for paths containing special characters. The
func unquoteStatusPath(p string) string {
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		return p[1 : len(p)-1]
	}
	return p
}

func recoveryDirty(status string) string {
	hasTracked := false
	hasUntracked := false
	for _, line := range strings.Split(status, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			hasUntracked = true
		} else {
			hasTracked = true
		}
	}
	var b strings.Builder
	b.WriteString("  git status              # see what's outstanding\n")
	if hasTracked {
		b.WriteString("  git add ... && git commit\n")
	}
	if hasUntracked {
		b.WriteString("  git add <untracked> && git commit   # or add to .gitignore\n")
	}
	b.WriteString("  git push\n")
	return b.String()
}

func runGit(repoRoot string, args ...string) (string, error) {
	full := append([]string{"-C", repoRoot}, args...)
	cmd := exec.Command("git", full...) // #nosec G204 -- args are caller-controlled, not user-shaped
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parseAheadBehind(revListOutput string) (ahead, behind int, err error) {
	fields := strings.Fields(strings.TrimSpace(revListOutput))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("gittree: unexpected rev-list output %q", revListOutput)
	}
	if _, err := fmt.Sscanf(fields[0], "%d", &ahead); err != nil {
		return 0, 0, fmt.Errorf("gittree: parse ahead %q: %w", fields[0], err)
	}
	if _, err := fmt.Sscanf(fields[1], "%d", &behind); err != nil {
		return 0, 0, fmt.Errorf("gittree: parse behind %q: %w", fields[1], err)
	}
	return ahead, behind, nil
}

func remoteOf(upstream string) string {
	if i := strings.IndexByte(upstream, '/'); i > 0 {
		return upstream[:i]
	}
	return "origin"
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n... (truncated)"
}
