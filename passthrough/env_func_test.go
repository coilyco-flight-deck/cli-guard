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
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/passthrough"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/shell"
	"github.com/urfave/cli/v3"
)

// newEnvFuncHarness wires a passthrough whose stub dumps its env to a file;
// baseEnv seeds the parent env so a test can prove injected keys win.
func newEnvFuncHarness(t *testing.T, baseEnv []string, fn func() (map[string]string, error)) (cmd *cli.Command, envFile string, base *shell.Runner) {
	t.Helper()
	dir := t.TempDir()
	envFile = filepath.Join(dir, "env.out")
	stub := filepath.Join(dir, "stub.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nenv > "+envFile+"\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write stub: %v", err)
	}
	w := audit.NewWriter(filepath.Join(dir, "audit.jsonl"))
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}
	base = &shell.Runner{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Env:     baseEnv,
		Resolve: func(_ string) (string, error) { return stub, nil },
	}
	cmd = passthrough.Command("netlify", base, w, passthrough.WithEnvFunc(fn))
	cmd.ExitErrHandler = func(_ context.Context, _ *cli.Command, _ error) {}
	return cmd, envFile, base
}

func TestWithEnvFunc_InjectsResolvedValuesIntoChild(t *testing.T) {
	cmd, envFile, _ := newEnvFuncHarness(t, nil, func() (map[string]string, error) {
		return map[string]string{"NETLIFY_AUTH_TOKEN": "secret-from-ssm"}, nil
	})
	if err := cmd.Run(context.Background(), []string{"netlify", "status"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env.out: %v", err)
	}
	if !strings.Contains(string(got), "NETLIFY_AUTH_TOKEN=secret-from-ssm") {
		t.Errorf("child env missing injected token, got:\n%s", got)
	}
}

func TestWithEnvFunc_DoesNotMutateBaseEnv(t *testing.T) {
	baseEnv := []string{"PRE=existing"}
	cmd, _, base := newEnvFuncHarness(t, baseEnv, func() (map[string]string, error) {
		return map[string]string{"NETLIFY_AUTH_TOKEN": "secret"}, nil
	})
	if err := cmd.Run(context.Background(), []string{"netlify", "status"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The injected key must not have leaked back into the shared base slice;
	// otherwise a later non-netlify passthrough would inherit the secret.
	for _, e := range base.Env {
		if strings.HasPrefix(e, "NETLIFY_AUTH_TOKEN=") {
			t.Errorf("base env mutated, contains %q", e)
		}
	}
	if len(base.Env) != 1 || base.Env[0] != "PRE=existing" {
		t.Errorf("base env changed: %v", base.Env)
	}
}

func TestWithEnvFunc_NilIsNoOp(t *testing.T) {
	cmd, envFile, _ := newEnvFuncHarness(t, nil, nil)
	if err := cmd.Run(context.Background(), []string{"netlify", "status"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(envFile)
	if strings.Contains(string(got), "NETLIFY_AUTH_TOKEN=") {
		t.Errorf("child env unexpectedly has NETLIFY_AUTH_TOKEN with nil hook:\n%s", got)
	}
}

func TestWithEnvFunc_EmptyMapInjectsNothing(t *testing.T) {
	cmd, envFile, _ := newEnvFuncHarness(t, nil, func() (map[string]string, error) {
		return map[string]string{}, nil
	})
	if err := cmd.Run(context.Background(), []string{"netlify", "status"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(envFile)
	if strings.Contains(string(got), "NETLIFY_AUTH_TOKEN=") {
		t.Errorf("child env unexpectedly has NETLIFY_AUTH_TOKEN for empty map:\n%s", got)
	}
}

func TestWithEnvFunc_ErrorShortCircuits(t *testing.T) {
	boom := errors.New("ssm: expired sso")
	cmd, envFile, _ := newEnvFuncHarness(t, nil, func() (map[string]string, error) {
		return nil, boom
	})
	err := cmd.Run(context.Background(), []string{"netlify", "status"})
	if err == nil {
		t.Fatal("Run: want err, got nil")
	}
	if !strings.Contains(err.Error(), "expired sso") {
		t.Errorf("err = %q, want to surface the resolver error", err.Error())
	}
	if _, statErr := os.Stat(envFile); statErr == nil {
		t.Error("env file exists, subprocess ran despite hook error")
	}
}
