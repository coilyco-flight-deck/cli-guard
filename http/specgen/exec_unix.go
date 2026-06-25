//go:build !windows

// Process replacement (syscall.Exec) and cache locking (flock) on Unix.
package specgen

import (
	"os"
	"syscall"
)

// execBinary replaces the current process with path, passing args through.
// It returns only on failure to exec; on success the process image is gone.
func execBinary(path string, args []string) error {
	return syscall.Exec(path, append([]string{path}, args...), os.Environ())
}

// lockFile takes an exclusive advisory lock on f, serializing concurrent
// materialize+build of the same cache dir.
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

// unlockFile releases the advisory lock taken by lockFile.
func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
