//go:build linux

package sandbox_test

import (
	"os/exec"
	"slices"
	"syscall"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/sandbox"
)

// jailSpec is a usable Spec so Wrap actually rewrites the cmd (vs no-op).
func jailSpec() *sandbox.Spec {
	return &sandbox.Spec{SelfExe: "/proc/self/exe", Tools: []string{"true"}}
}

func TestWrapNoOpWhenOptedOut(t *testing.T) {
	t.Setenv(sandbox.EnvNoSandbox, "1")
	cmd := exec.Command("/bin/true")
	orig := slices.Clone(cmd.Args)
	sandbox.Wrap(cmd, "/bin/true", []string{"true"}, jailSpec())
	if sandbox.IsJailInvocation(cmd.Args) {
		t.Error("Wrap must no-op under CLIGUARD_NO_SANDBOX")
	}
	if !slices.Equal(cmd.Args, orig) {
		t.Errorf("Wrap mutated args under opt-out: %v", cmd.Args)
	}
}

func TestSetupDenied(t *testing.T) {
	// A non-jailed cmd is never a setup-denial, even on EPERM.
	plain := exec.Command("/bin/true")
	if sandbox.SetupDenied(plain, syscall.EPERM) {
		t.Error("non-jailed cmd must not count as a setup-denial")
	}

	// The jailed-path assertions need Wrap to rewrite argv, which it skips when
	// opted out or already jailed — skip there rather than fatal (plain checks ran).
	if sandbox.NoSandbox() || sandbox.Jailed() {
		t.Skip("sandbox opted out or already jailed: Wrap no-ops, jailed-path assertions not exercised")
	}

	// Wrap (do not run) so the cmd is jailed and ProcessState stays nil.
	jailed := exec.Command("/bin/true")
	sandbox.Wrap(jailed, "/bin/true", []string{"true"}, jailSpec())
	if !sandbox.IsJailInvocation(jailed.Args) {
		t.Fatal("precondition: Wrap should have jailed the cmd")
	}
	if !sandbox.SetupDenied(jailed, syscall.EPERM) {
		t.Error("jailed + EPERM + unstarted should be a setup-denial")
	}
	if !sandbox.SetupDenied(jailed, syscall.EACCES) {
		t.Error("jailed + EACCES should be a setup-denial")
	}
	if sandbox.SetupDenied(jailed, nil) {
		t.Error("nil err is never a setup-denial")
	}
	if sandbox.SetupDenied(jailed, syscall.ENOENT) {
		t.Error("a non-permission error is not a setup-denial")
	}
	if sandbox.SetupDenied(nil, syscall.EPERM) {
		t.Error("nil cmd is never a setup-denial")
	}
}
