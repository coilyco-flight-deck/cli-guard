package dispatch

import (
	"testing"

	"github.com/coilysiren/cli-guard/shell"
	"github.com/coilysiren/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

// newTestDispatcher builds a Dispatcher whose required Config fields point
// at stable test tempdirs. Wrap is the identity pass-through: the verb
// pipeline (audit, argv validation) is exercised in the verb package's
// own tests, so the dispatch tests only need the bare Action to run.
// Individual tests mutate d.cfg fields (seams, roots) after construction.
func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	repoRoot := t.TempDir()
	worktreeRoot := t.TempDir()
	logRoot := t.TempDir()
	d, err := New(Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "coilysiren",
		BinaryName:   "coily",
		RepoPath:     func(_ string) (string, error) { return repoRoot, nil },
		WorktreeRoot: func() (string, error) { return worktreeRoot, nil },
		LogRoot:      func() (string, error) { return logRoot, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}
