// Package ownertrust is the allow-list gate for which repository owners a
// dispatcher will act on. An agent that runs with elevated permissions
// (bypassing the usual approval prompts) must refuse to fan out into an
// untrusted owner's repos, so this is the single yes/no check plus the
// human-readable label naming the accepted set for a refusal message.
package ownertrust

import (
	"fmt"
	"strings"
)

// List is the accepted-owner set: a required Primary owner plus any number
// of Extra owners accepted alongside it.
type List struct {
	// Primary is the main owner the gate accepts. Required for a meaningful
	// allow-list; an empty Primary accepts nothing (unless Extra is set).
	Primary string
	// Extra holds additional accepted owners. Empty means Primary-only.
	Extra []string
}

// Allowed reports whether owner is the Primary or a member of Extra. An
// empty owner is never allowed.
func (l List) Allowed(owner string) bool {
	if owner == "" {
		return false
	}
	if owner == l.Primary {
		return true
	}
	for _, o := range l.Extra {
		if owner == o {
			return true
		}
	}
	return false
}

// Label renders the accepted-owner set for a refusal message: "<owner>/*"
// for one owner, or "{primary, a, b}/*" (Primary-first, de-duplicated).
func (l List) Label() string {
	seen := map[string]bool{}
	var owners []string
	for _, o := range append([]string{l.Primary}, l.Extra...) {
		if o == "" || seen[o] {
			continue
		}
		seen[o] = true
		owners = append(owners, o)
	}
	switch len(owners) {
	case 0:
		return "(no owners)/*"
	case 1:
		return owners[0] + "/*"
	default:
		return fmt.Sprintf("{%s}/*", strings.Join(owners, ", "))
	}
}
