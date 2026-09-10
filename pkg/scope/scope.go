// Package scope resolves cwd to its git toplevel for an audit record's
// RepoRoot. Best-effort and forensic-only: outside a repo it yields "".
package scope

import (
	"os"
	"path/filepath"
)

// RepoRoot returns the git toplevel of cwd, "" outside a repo. It walks the
// filesystem: a `git` replacement on PATH intercepts `rev-parse`. teable umbra#7343.
func RepoRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for {
		// A linked worktree and a submodule carry `.git` as a file rather than
		// a directory, and both make this directory a toplevel.
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
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
