// Package workdir does best-effort detection of the "primary working
// directory" that a consumer invocation is operating against. Subcommands
package workdir

import (
	"os"
	"path/filepath"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/config"
)

// OverrideEnv names the app-dir-derived env var that forces Detect's answer
// (e.g. ".ward" -> "WARD_PRIMARY_DIR").
func OverrideEnv() string { return config.EnvName("_PRIMARY_DIR") }

// Source labels which signal produced the result. Callers that need to
// gate behavior on confidence should branch on this.
type Source string

// Signal labels for Result.Source, in detection-precedence order.
const (
	// SourceEnv means the app-dir-derived $<APP>_PRIMARY_DIR forced the answer.
	SourceEnv Source = "env"
	// SourceGit means the nearest ancestor with a .git entry won.
	SourceGit Source = "git"
	// SourceCoilysiren means the first path segment under
	// ~/projects/coilysiren/ won.
	SourceCoilysiren Source = "coilysiren"
	// SourceCWD means no other signal matched and cwd was returned verbatim.
	SourceCWD Source = "cwd"
)

// Result is what Detect returns. Path is always absolute and cleaned.
type Result struct {
	Path   string
	Source Source
}

// Detect runs the signal chain against the current process cwd.
func Detect() Result {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return DetectFrom(cwd)
}

// DetectFrom is Detect parameterized on cwd, for tests and callers that
// already have a working directory in hand.
func DetectFrom(cwd string) Result {
	if v := strings.TrimSpace(os.Getenv(OverrideEnv())); v != "" {
		return Result{Path: absUnder(cwd, v), Source: SourceEnv}
	}
	if root := findGitRoot(cwd); root != "" {
		return Result{Path: root, Source: SourceGit}
	}
	if repo := coilysirenRepo(cwd); repo != "" {
		return Result{Path: repo, Source: SourceCoilysiren}
	}
	return Result{Path: filepath.Clean(cwd), Source: SourceCWD}
}

func absUnder(cwd, v string) string {
	if !filepath.IsAbs(v) {
		v = filepath.Join(cwd, v)
	}
	return filepath.Clean(v)
}

// findGitRoot walks from start toward the filesystem root, returning the
// first directory that has a .git child (file or dir). Worktree
func findGitRoot(start string) string {
	if start == "" {
		return ""
	}
	dir := filepath.Clean(start)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// coilysirenRepo returns ~/projects/coilysiren/<first-segment> when cwd
// is inside that parent, else "".
func coilysirenRepo(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	parent := filepath.Join(home, "projects", "coilysiren")
	rel, err := filepath.Rel(parent, filepath.Clean(cwd))
	if err != nil {
		return ""
	}
	if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	segs := strings.Split(rel, string(filepath.Separator))
	if len(segs) == 0 || segs[0] == "" {
		return ""
	}
	return filepath.Join(parent, segs[0])
}
