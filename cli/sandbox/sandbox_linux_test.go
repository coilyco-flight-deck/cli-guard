//go:build linux

package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/sandbox"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"golang.org/x/sys/unix"
)

// This binary plays three roles via TestMain: jail helper (__jail), the SHIM a
// rerouted grandchild hits (basename faketool), and a ptrace probe (ptracetool).
const (
	fakeTool   = "faketool"
	ptraceTool = "ptracetool"
	envLog     = "CLIGUARD_SANDBOX_TEST_LOG"
)

// Distinct exit codes let a security-claim test tell "the runner can't enforce
// the jail" (skip) from "the jail engaged but the guarantee broke" (fail).
const (
	jailSetupFailed     = 7 // RunJail errored (clone ok, but mount/seccomp denied)
	probeNotJailed      = 8 // probe ran outside the jail (opt-out or auto-degrade)
	probeSeccompMissing = 9 // probe ran inside the jail but ptrace was not denied
)

func TestMain(m *testing.M) {
	if sandbox.IsJailInvocation(os.Args) {
		if err := sandbox.RunJail(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "jail:", err)
			os.Exit(jailSetupFailed)
		}
		return
	}
	switch filepath.Base(os.Args[0]) {
	case fakeTool:
		appendLog("SHIM " + strings.Join(os.Args[1:], " "))
		os.Exit(0)
	case ptraceTool:
		// Jail never engaged (opt-out or auto-degrade): flag distinctly from a
		// jailed-but-unfiltered run so the test can skip vs fail.
		if os.Getenv(sandbox.EnvJailed) != "1" {
			os.Exit(probeNotJailed)
		}
		// TRACEME normally succeeds; the seccomp denylist turns it into EPERM.
		_, _, errno := unix.Syscall(unix.SYS_PTRACE, uintptr(unix.PTRACE_TRACEME), 0, 0)
		if errno == unix.EPERM {
			os.Exit(0) // denied -> filter active
		}
		os.Exit(probeSeccompMissing) // not denied -> filter missing
	}
	os.Exit(m.Run())
}

func appendLog(line string) {
	path := os.Getenv(envLog)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, line)
}

// TestSecurityClaim_GrandchildRoutesThroughGate: a sandboxed tool's descendants
// can't invoke a wrapped tool (name or absolute path) without hitting the shim.
func TestSecurityClaim_GrandchildRoutesThroughGate(t *testing.T) {
	self := testSelf(t)
	dir := t.TempDir()

	// PATH dir holds the canonical `faketool` symlink the gate masks.
	binDir := filepath.Join(dir, "bin")
	mustMkdir(t, binDir)
	// The real tool lives in a *different* directory (like brew's script vs its
	// PATH symlink), so masking the symlink leaves the real target reachable.
	realScript := filepath.Join(dir, "real", fakeTool)
	mustMkdir(t, filepath.Dir(realScript))
	writeScript(t, realScript, `#!/bin/bash
echo "REAL $* JAILED=$CLIGUARD_JAILED" >> "$CLIGUARD_SANDBOX_TEST_LOG"
if [ "$1" = "spawn" ]; then
  faketool byname            # PATH lookup -> masked symlink -> shim
  "$BINDIR/faketool" byabspath  # absolute path -> masked symlink -> shim
fi
`)
	mustSymlink(t, realScript, filepath.Join(binDir, fakeTool))

	logPath := filepath.Join(dir, "log")
	env := []string{
		envLog + "=" + logPath,
		"BINDIR=" + binDir,
		"PATH=" + binDir + ":" + os.Getenv("PATH"),
	}

	// Negative control: no sandbox -> grandchildren run the REAL tool, no shim.
	runTool(t, env, nil, fakeTool, "spawn")
	if got := readLog(t, logPath); strings.Contains(got, "SHIM") {
		t.Fatalf("without sandbox, expected no reroute; log:\n%s", got)
	}

	// Sandboxed: grandchildren must be rerouted to the shim.
	_ = os.Remove(logPath)
	spec := &sandbox.Spec{SelfExe: self, Tools: []string{fakeTool}}
	if err := runTool(t, env, spec, fakeTool, "spawn"); err != nil {
		if isUserNSUnavailable(err) {
			t.Skipf("unprivileged namespaces unavailable: %v", err)
		}
		if exitCode(err) == jailSetupFailed {
			t.Skipf("jail setup failed (mount/seccomp denied by runner); reroute not enforceable here: %v", err)
		}
		t.Fatalf("sandboxed run failed: %v", err)
	}
	got := readLog(t, logPath)
	if !strings.Contains(got, "REAL spawn") {
		t.Fatalf("expected the real tool to run once at its true path; log:\n%s", got)
	}
	// No CLIGUARD_JAILED means the run opted out or auto-degraded unsandboxed, so
	// the reroute is untestable — skip. When it engaged, the SHIM asserts below fire.
	if !strings.Contains(got, "JAILED=1") {
		t.Skipf("sandbox did not engage (opted out or namespace jail unavailable); reroute not enforceable; log:\n%s", got)
	}
	link := filepath.Join(binDir, "."+fakeTool+".cliguard")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("exec symlink leaked onto host filesystem at %s: %v", link, err)
	}
	if !strings.Contains(got, "SHIM byname") {
		t.Errorf("name-based grandchild call was not rerouted to the gate; log:\n%s", got)
	}
	if !strings.Contains(got, "SHIM byabspath") {
		t.Errorf("absolute-path grandchild call was not rerouted to the gate; log:\n%s", got)
	}
}

// TestSecurityClaim_SeccompDeniesPtrace pins the SECURITY.md claim that the jail
// installs a seccomp denylist: a tool running inside it cannot ptrace.
func TestSecurityClaim_SeccompDeniesPtrace(t *testing.T) {
	self := testSelf(t)
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	mustMkdir(t, binDir)
	// The wrapped tool here is the test binary itself (probes ptrace); the jail
	// execs it with argv[0]=ptracetool, which TestMain routes to the probe.
	mustSymlink(t, self, filepath.Join(binDir, ptraceTool))

	env := []string{"PATH=" + binDir + ":" + os.Getenv("PATH")}
	spec := &sandbox.Spec{SelfExe: self, Tools: []string{ptraceTool}}
	err := runTool(t, env, spec, ptraceTool)
	if err == nil {
		return // ptrace was denied inside the jail: the seccomp filter is enforced.
	}
	if isUserNSUnavailable(err) {
		t.Skipf("unprivileged namespaces unavailable: %v", err)
	}
	// Skip only where the runner can't deliver the jail; a probe that ran inside
	// the jail without ptrace denied (probeSeccompMissing) is a real regression.
	switch exitCode(err) {
	case jailSetupFailed:
		t.Skipf("jail setup failed (mount/seccomp denied by runner); seccomp not enforceable here: %v", err)
	case probeNotJailed:
		t.Skipf("sandbox did not engage (opted out or auto-degraded); seccomp not enforceable here: %v", err)
	default:
		t.Fatalf("ptrace probe ran inside the jail but was not denied: seccomp denylist not enforced (%v)", err)
	}
}

// exitCode returns the process exit code carried by err, or -1 if err is not an
// exec exit error.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// runTool execs bin under a shell.Runner with the given env and optional
// sandbox spec, mirroring the production exec chokepoint.
func runTool(t *testing.T, env []string, spec *sandbox.Spec, bin string, args ...string) error {
	t.Helper()
	// LookPath in the parent resolves against the process PATH, not Runner.Env.
	t.Setenv("PATH", envValue(env, "PATH"))
	r := &shell.Runner{
		Stdout:  os.Stderr,
		Stderr:  os.Stderr,
		Env:     env,
		Sandbox: spec,
	}
	return r.Exec(context.Background(), bin, args...)
}

func testSelf(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return self
}

func envValue(env []string, key string) string {
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && k == key {
			return v
		}
	}
	return os.Getenv(key)
}

func isUserNSUnavailable(err error) bool {
	s := err.Error()
	return strings.Contains(s, "operation not permitted") ||
		strings.Contains(s, "permission denied") ||
		strings.Contains(s, "invalid argument")
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil { // #nosec G306 -- test fixture must be executable
		t.Fatalf("write %s: %v", path, err)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- test-controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read log: %v", err)
	}
	return string(b)
}
