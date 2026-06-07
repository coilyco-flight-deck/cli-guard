//go:build !linux

package sandbox

import "os/exec"

// Wrap is a no-op off Linux: OS-level jailing is unsupported, so the caller
// execs the real binary directly (pre-sandbox behavior).
func Wrap(_ *exec.Cmd, _ string, _ []string, _ *Spec) {}

// IsJailInvocation is always false off Linux.
func IsJailInvocation(_ []string) bool { return false }

// RunJail is unsupported off Linux.
func RunJail(_ []string) error {
	return errUnsupported
}

var errUnsupported = unsupportedError("sandbox: OS-level jail is Linux-only")

type unsupportedError string

func (e unsupportedError) Error() string { return string(e) }
