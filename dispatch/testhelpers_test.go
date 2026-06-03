package dispatch

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

// newTestDispatcher builds a Dispatcher whose required Config fields point
// at stable test tempdirs. Wrap is the identity pass-through: the verb
func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	repoRoot := t.TempDir()
	worktreeRoot := t.TempDir()
	logRoot := t.TempDir()
	d, err := New(Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		BinaryName:   "example-cli",
		RepoPath:     func(_ string) (string, error) { return repoRoot, nil },
		WorktreeRoot: func() (string, error) { return worktreeRoot, nil },
		LogRoot:      func() (string, error) { return logRoot, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}
