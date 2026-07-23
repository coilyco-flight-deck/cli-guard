package specgencli

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/kdlspecs"
)

func TestVersionReportsDriverAndDefaultCLIGuardRef(t *testing.T) {
	var out bytes.Buffer

	if code := Run(context.Background(), []string{"specgen", "--version"}, &out, &out); code != 0 {
		t.Fatalf("run --version exit code = %d, want 0", code)
	}

	want := fmt.Sprintf(
		"specgen version %s (cli-guard ref %s)\n",
		kdlspecs.DriverVersion(),
		kdlspecs.DefaultCLIGuardRef(),
	)
	if got := out.String(); got != want {
		t.Fatalf("--version output = %q, want %q", got, want)
	}
}
