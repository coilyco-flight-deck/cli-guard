//go:build !unix

package flock

import "os"

// flock fallback for non-unix builds: the advisory flock syscall is
// unix-only, so cross-process serialisation degrades to a no-op here.

func exclusive(_ *os.File) error { return nil }

func unlock(_ *os.File) error { return nil }
