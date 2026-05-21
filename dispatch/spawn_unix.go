//go:build !windows

package dispatch

import "syscall"

// detachSysProcAttr returns the SysProcAttr that fully detaches the
// headless dispatch child on Unix. Setsid puts the child in a new session
// with its own process group, so it has no controlling terminal and is
// not killed when the parent's tty closes.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
