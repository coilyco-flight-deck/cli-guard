// Package scope resolves the --commit-scope flag value into the absolute
// repo path that an audit record should be bound to. The trailer-emitting
package scope

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coilysiren/cli-guard/ttlcache"
)

// gitToplevelCache memoizes (cwd -> toplevel) resolutions across coily
// invocations. Every non-SkipScope verb calls Resolve twice (once for
var (
	gitToplevelCache     *ttlcache.Cache
	gitToplevelCacheOnce sync.Once
	gitToplevelCacheTTL  = 5 * time.Minute
)

func toplevelCache() *ttlcache.Cache {
	gitToplevelCacheOnce.Do(func() {
		dir := os.Getenv("COILY_CACHE_DIR")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				home = os.TempDir()
			}
			dir = filepath.Join(home, ".coily", "cache")
		}
		gitToplevelCache = ttlcache.New(filepath.Join(dir, "git-toplevel"))
	})
	return gitToplevelCache
}

// ErrNotInRepo is returned when --commit-scope=auto is requested but cwd
// is not inside a git repo. Caller is expected to pass --commit-scope
var ErrNotInRepo = errors.New("scope: cwd is not inside a git repo; pass --commit-scope=<repo-path> explicitly")

// ErrOptOutRejected is returned when the caller passes a value meant to
// disable binding ("-", "none", "off"). The opt-out hatch was removed -
var ErrOptOutRejected = errors.New("scope: --commit-scope opt-out is not supported; pass an explicit repo path")

// Resolve interprets a --commit-scope flag value:
//   - "auto" (case-insensitive): git toplevel of cwd, or ErrNotInRepo.
func Resolve(flagValue, envFallback, cwd string) (string, error) {
	val := flagValue
	if val == "" {
		val = envFallback
	}
	if val == "" {
		val = "auto"
	}
	switch strings.ToLower(val) {
	case "auto":
		return gitToplevel(cwd)
	case "-", "none", "off":
		return "", ErrOptOutRejected
	}
	if !filepath.IsAbs(val) {
		val = filepath.Join(cwd, val)
	}
	return filepath.Clean(val), nil
}

func gitToplevel(cwd string) (string, error) {
	cache := toplevelCache()
	if v, ok := cache.Get(cwd); ok {
		return string(v), nil
	}
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w (%w)", ErrNotInRepo, err)
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", ErrNotInRepo
	}
	_ = cache.Set(cwd, []byte(top), gitToplevelCacheTTL) // perf hint, not load-bearing
	return top, nil
}

// CWD returns the current working directory or empty on error. Convenience
// wrapper so callers can do scope.Resolve(flag, env, scope.CWD()) without
func CWD() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
