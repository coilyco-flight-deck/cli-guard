// Package version compares release tags the way a self-update reminder
// needs: parse a vX.Y.Z tag into numeric parts, and decide whether a
// running build is behind the latest release. It is deliberately lenient
// about tag shape (a missing v, a short tag, a -pre/+build suffix) and
// conservative about its verdict: Behind only fires when both tags parse
// and the comparison is unambiguous, so a self-update nag never cries
// wolf on an unparseable or dev build.
package version

import (
	"strconv"
	"strings"
)

// LooksReleased reports whether v is a real release tag worth comparing
// against latest - not the "dev" default and not a blank/source build.
func LooksReleased(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && v != "dev"
}

// Parse splits a vX.Y.Z tag into 3 numeric parts, tolerating a missing v, a
// short tag, and a -pre/+build suffix. ok is false on a bad or empty tag.
func Parse(tag string) (parts [3]int, ok bool) {
	s := strings.TrimSpace(tag)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return parts, false
	}
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	segs := strings.Split(s, ".")
	if len(segs) > 3 {
		segs = segs[:3]
	}
	for i, seg := range segs {
		n, err := strconv.Atoi(seg)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		parts[i] = n
	}
	return parts, true
}

// Behind reports whether current is an older release than latest. A dev,
// unparseable, or already-current build returns false (fires only if behind).
func Behind(current, latest string) bool {
	if !LooksReleased(current) {
		return false
	}
	cur, ok1 := Parse(current)
	lat, ok2 := Parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if cur[i] != lat[i] {
			return cur[i] < lat[i]
		}
	}
	return false
}
