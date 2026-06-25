//go:build unix

package flock

import (
	"os"
	"syscall"
)

// exclusive / unlock wrap the BSD advisory flock the shell idiom used
// (`flock 9`) for cross-process mutual exclusion. Unix-only.

func exclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
