// Package scope resolves cwd to its git toplevel for an audit record's
// RepoRoot. Best-effort and forensic-only: outside a repo it yields "".
package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/ttlcache"
)

// gitToplevelCache memoizes (cwd -> toplevel) so the per-invocation
// RepoRoot stamp does not re-shell out to git on every call.
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

// RepoRoot returns the git toplevel of cwd, or "" when cwd is not inside a
// git repo (or git is unavailable). Best-effort: "" is valid, never an error.
func RepoRoot(cwd string) string {
	cache := toplevelCache()
	if v, ok := cache.Get(cwd); ok {
		return string(v)
	}
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return ""
	}
	top = filepath.Clean(top)
	_ = cache.Set(cwd, []byte(top), gitToplevelCacheTTL) // perf hint, not load-bearing
	return top
}

// CWD returns the current working directory or empty on error. Lets callers
// write scope.RepoRoot(scope.CWD()) without juggling the os.Getwd error.
func CWD() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
