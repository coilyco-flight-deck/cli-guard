package clitrees

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// This description is the taxonomy's only full statement with the retry policy
// attached, and gen-webdocs publishes it. See teable umbra#7336.
func TestExitcodeDescriptionStatesTheWholeTaxonomy(t *testing.T) {
	desc := Exitcode().Description
	for _, want := range []string{
		"0 - Success",
		"1 - Generic",
		"2 - PolicyDenied",
		"3 - UpstreamFailed",
		"4 - Internal",
		"5 - UserError",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("the rendered taxonomy no longer states %q", want)
		}
	}
	// Codes alone are a table. The retry behaviour is what makes it a contract.
	if !strings.Contains(desc, "non-retryable") {
		t.Error("the taxonomy states codes but no longer states retry behaviour")
	}
}

// gen-webdocs enumerates these by name, so a tree that stops building takes its
// published page down with it.
func TestEveryRenderedTreeBuilds(t *testing.T) {
	for name, build := range map[string]func() *cli.Command{
		"audit":    func() *cli.Command { return Audit(nil) },
		"exitcode": Exitcode,
		"policy":   Policy,
	} {
		cmd := build()
		if cmd == nil {
			t.Fatalf("%s: nil tree", name)
		}
		if len(cmd.Commands) == 0 {
			t.Errorf("%s: tree has no leaves, so its page would render empty", name)
		}
	}
}
