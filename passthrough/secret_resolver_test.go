package passthrough_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/mcporter"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/passthrough"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/shell"
	"github.com/urfave/cli/v3"
)

// secretResolverHarness wires a passthrough that exec's a stub script
// dumping its env to a file. Lets the test assert which env vars
type secretResolverHarness struct {
	cmd     *cli.Command
	envFile string
}

func newSecretResolverHarness(t *testing.T, resolver mcporter.SecretResolver, mcporterConfigBody string) secretResolverHarness {
	t.Helper()

	dir := t.TempDir()
	// Place a mcporter.json under MCPORTER_CONFIG so ScanConfig finds it.
	cfgPath := filepath.Join(dir, "mcporter.json")
	if err := os.WriteFile(cfgPath, []byte(mcporterConfigBody), 0o600); err != nil {
		t.Fatalf("write mcporter.json: %v", err)
	}
	t.Setenv("MCPORTER_CONFIG", cfgPath)

	// Stub script: write `env` to envFile, exit 0.
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
		Resolve: func(_ string) (string, error) { return stub, nil },
	}
	cmd := passthrough.Command("mcporter", r, w, passthrough.WithSecretResolver(resolver))
	cmd.ExitErrHandler = func(_ context.Context, _ *cli.Command, _ error) {}
	return secretResolverHarness{cmd: cmd, envFile: envFile}
}

func TestWithSecretResolver_InjectsResolvedValuesIntoChild(t *testing.T) {
	body := `{"servers":{"a":{"url":"http://x/${EXAMPLE_VAR}"}}}`
	resolver := mcporter.ResolverFunc(func(name string) (string, error) {
		if name != "EXAMPLE_VAR" {
			t.Errorf("unexpected name %q", name)
		}
		return "resolved-value", nil
	})
	h := newSecretResolverHarness(t, resolver, body)
	// Ensure the var is NOT set in the parent before exec, so we can
	// prove it reaches the child only via the preflight.
	os.Unsetenv("EXAMPLE_VAR")

	if err := h.cmd.Run(context.Background(), []string{"mcporter", "list", "a"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(h.envFile)
	if err != nil {
		t.Fatalf("read env.out: %v", err)
	}
	if !strings.Contains(string(got), "EXAMPLE_VAR=resolved-value") {
		t.Errorf("child env missing EXAMPLE_VAR=resolved-value, got:\n%s", got)
	}
	if v, ok := os.LookupEnv("EXAMPLE_VAR"); ok {
		t.Errorf("parent env now has EXAMPLE_VAR=%q (preflight leaked)", v)
	}
}

func TestWithSecretResolver_NoResolverIsNoOp(t *testing.T) {
	body := `{"servers":{"a":{"url":"http://x/${EXAMPLE_VAR}"}}}`
	h := newSecretResolverHarness(t, nil, body)
	if err := h.cmd.Run(context.Background(), []string{"mcporter", "list"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(h.envFile)
	if strings.Contains(string(got), "EXAMPLE_VAR=") {
		t.Errorf("child env unexpectedly has EXAMPLE_VAR with nil resolver:\n%s", got)
	}
}

func TestWithSecretResolver_ErrorShortCircuits(t *testing.T) {
	body := `{"servers":{"a":{"url":"http://x/${BAD_VAR}"}}}`
	boom := errors.New("expired sso")
	resolver := mcporter.ResolverFunc(func(_ string) (string, error) {
		return "", boom
	})
	h := newSecretResolverHarness(t, resolver, body)

	err := h.cmd.Run(context.Background(), []string{"mcporter", "list"})
	if err == nil {
		t.Fatal("Run: want err, got nil")
	}
	if !strings.Contains(err.Error(), "BAD_VAR") {
		t.Errorf("err = %q, want to name failing var BAD_VAR", err.Error())
	}
	// Subprocess must not have run.
	if _, statErr := os.Stat(h.envFile); statErr == nil {
		t.Error("env file exists, subprocess ran despite resolver error")
	}
}

func TestWithSecretResolver_MissingConfigIsNoOp(t *testing.T) {
	dir := t.TempDir()
	// MCPORTER_CONFIG points at a path that doesn't exist.
	t.Setenv("MCPORTER_CONFIG", filepath.Join(dir, "absent.json"))

	envFile := filepath.Join(dir, "env.out")
	stub := filepath.Join(dir, "stub.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nenv > "+envFile+"\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write stub: %v", err)
	}
	w := audit.NewWriter(filepath.Join(dir, "audit.jsonl"))
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}
	called := false
	resolver := mcporter.ResolverFunc(func(_ string) (string, error) {
		called = true
		return "", nil
	})
	r := &shell.Runner{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Resolve: func(_ string) (string, error) { return stub, nil },
	}
	cmd := passthrough.Command("mcporter", r, w, passthrough.WithSecretResolver(resolver))
	cmd.ExitErrHandler = func(_ context.Context, _ *cli.Command, _ error) {}
	if err := cmd.Run(context.Background(), []string{"mcporter", "list"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("resolver called despite missing config")
	}
}
