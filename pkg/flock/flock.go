// Package flock wraps the BSD advisory whole-file lock (flock(2)) so a
// consumer can serialise mutually-exclusive work across processes that
// share a lock file - e.g. one warm-cache writer at a time. The lock is
// advisory: it only constrains cooperating processes that also call here.
//
// Exclusive blocks until it holds the lock; Unlock releases it. Both take
// the *os.File the caller opened on the shared lock path and keep open for
// the lock's lifetime. On non-unix builds both degrade to a no-op, since
// the advisory flock syscall is unix-only.
package flock

import "os"

// Exclusive takes a blocking exclusive (LOCK_EX) advisory lock on f, held
// until Unlock or f is closed.
func Exclusive(f *os.File) error { return exclusive(f) }

// Unlock releases the advisory lock held on f (LOCK_UN). Releasing a file
// that holds no lock is harmless.
func Unlock(f *os.File) error { return unlock(f) }
