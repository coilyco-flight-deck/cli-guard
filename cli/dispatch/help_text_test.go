package dispatch

import (
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"github.com/urfave/cli/v3"
)

// The dispatch + headless help text must advertise the same owner set the
// runtime gate (ownerAllowed) accepts, not AllowedOwner alone.
func TestHelpTextAdvertisesFullOwnerSet(t *testing.T) {
	repoRoot := t.TempDir()
	worktreeRoot := t.TempDir()
	logRoot := t.TempDir()
	d, err := New(Config{
		Runner:        &shell.Runner{},
		Wrap:          func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner:  "primary",
		AllowedOwners: []string{"sib-a", "sib-b"},
		BinaryName:    "example-cli",
		RepoPath:      func(_ string) (string, error) { return repoRoot, nil },
		WorktreeRoot:  func() (string, error) { return worktreeRoot, nil },
		LogRoot:       func() (string, error) { return logRoot, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := d.cfg.allowedOwnersLabel()
	if !strings.Contains(want, "sib-a") {
		t.Fatalf("precondition: label should mention sibling owners, got %q", want)
	}

	cmd := d.Command()
	descrs := map[string]string{"dispatch": cmd.Description}
	for _, sub := range cmd.Commands {
		if sub.Name == "headless" {
			descrs["headless"] = sub.Description
		}
	}
	if _, ok := descrs["headless"]; !ok {
		t.Fatal("headless subcommand not found")
	}

	for name, descr := range descrs {
		if !strings.Contains(descr, want) {
			t.Errorf("%s help text should advertise %q, got:\n%s", name, want, descr)
		}
		// The old bug rendered "primary/* org" — the single owner with no
		// mention of the siblings the gate accepts.
		if strings.Contains(descr, "primary/* org") {
			t.Errorf("%s help text still uses the single-owner label", name)
		}
	}
}
