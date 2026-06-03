package passthrough_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/egress"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/passthrough"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/shell"
	"github.com/urfave/cli/v3"
)

type egressEnvHarness struct {
	cmd     *cli.Command
	envFile string
}

func newEgressEnvHarness(t *testing.T, baseEnv []string) egressEnvHarness {
	t.Helper()

	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.out")
	stub := filepath.Join(dir, "stub.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nenv > "+envFile+"\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write stub: %v", err)
	}

	w := audit.NewWriter(filepath.Join(dir, "audit.jsonl"))
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}
	r := &shell.Runner{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Env:     baseEnv,
		Resolve: func(_ string) (string, error) { return stub, nil },
	}
	cmd := passthrough.Command("gh", r, w, passthrough.WithEgress(nil, egress.ModeObserve))
	return egressEnvHarness{cmd: cmd, envFile: envFile}
}

func envValueFromDump(dump, key string) (string, bool) {
	prefix := key + "="
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), true
		}
	}
	return "", false
}

func TestWithEgress_SetsLoopbackNoProxyByDefault(t *testing.T) {
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	h := newEgressEnvHarness(t, nil)

	if err := h.cmd.Run(context.Background(), []string{"app-test"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	body, err := os.ReadFile(h.envFile)
	if err != nil {
		t.Fatalf("read env dump: %v", err)
	}
	const want = "127.0.0.1,::1,localhost"
	gotUpper, ok := envValueFromDump(string(body), "NO_PROXY")
	if !ok {
		t.Fatalf("NO_PROXY missing from child env:\n%s", string(body))
	}
	if gotUpper != want {
		t.Errorf("NO_PROXY = %q, want %q", gotUpper, want)
	}
	gotLower, ok := envValueFromDump(string(body), "no_proxy")
	if !ok {
		t.Fatalf("no_proxy missing from child env:\n%s", string(body))
	}
	if gotLower != want {
		t.Errorf("no_proxy = %q, want %q", gotLower, want)
	}
}

func TestWithEgress_AppendsLoopbackNoProxyWithoutDroppingExisting(t *testing.T) {
	t.Setenv("NO_PROXY", "example.com,localhost")
	t.Setenv("no_proxy", "internal.local,127.0.0.1")
	h := newEgressEnvHarness(t, nil)

	if err := h.cmd.Run(context.Background(), []string{"app-test"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	body, err := os.ReadFile(h.envFile)
	if err != nil {
		t.Fatalf("read env dump: %v", err)
	}
	const want = "example.com,localhost,internal.local,127.0.0.1,::1"
	gotUpper, ok := envValueFromDump(string(body), "NO_PROXY")
	if !ok {
		t.Fatalf("NO_PROXY missing from child env:\n%s", string(body))
	}
	if gotUpper != want {
		t.Errorf("NO_PROXY = %q, want %q", gotUpper, want)
	}
	gotLower, ok := envValueFromDump(string(body), "no_proxy")
	if !ok {
		t.Fatalf("no_proxy missing from child env:\n%s", string(body))
	}
	if gotLower != want {
		t.Errorf("no_proxy = %q, want %q", gotLower, want)
	}
}
