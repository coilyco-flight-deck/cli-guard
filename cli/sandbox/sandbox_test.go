package sandbox_test

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/sandbox"
)

func TestNoSandbox(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "Yes", "on", "  1  "} {
		t.Setenv(sandbox.EnvNoSandbox, v)
		if !sandbox.NoSandbox() {
			t.Errorf("NoSandbox() = false for %q, want true", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off", "nope"} {
		t.Setenv(sandbox.EnvNoSandbox, v)
		if sandbox.NoSandbox() {
			t.Errorf("NoSandbox() = true for %q, want false", v)
		}
	}
}
