//go:build !windows

package dispatch

import "syscall"

// detachSysProcAttr returns the SysProcAttr that fully detaches the
// headless dispatch child on Unix. Setsid puts the child in a new session
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
