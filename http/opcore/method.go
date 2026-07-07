package opcore

import "strings"

// verbMethod is the primary HTTP method a convention verb maps to (for a
// multi-method verb like edit: PATCH before PUT, this is the first).
var verbMethod = map[string]string{
	"get":       "GET",
	"view":      "GET",
	"list":      "GET",
	"create":    "POST",
	"edit":      "PATCH",
	"delete":    "DELETE",
	"close":     "PATCH",
	"reopen":    "PATCH",
	"archive":   "PATCH",
	"unarchive": "PATCH",
	"add":       "POST",
	"set":       "PUT",
	"remove":    "DELETE",
}

// destructiveVerbs names the irreversibly-mutating verb classes. Generic, not
// API-specific: `delete` is destructive whatever the resource.
var destructiveVerbs = map[string]bool{"delete": true}

// DestructiveVerb reports whether a verb mutates irreversibly, so both the
// resolved and the inline source flag the same leaves. See docs/specverb.md.
func DestructiveVerb(verb string) bool { return destructiveVerbs[verb] }

// MethodForVerb returns the HTTP method a verb maps to by convention; ok is
// false only for the bare-unknown-verb POST default. See specverb-resolution.md.
func MethodForVerb(verb string) (string, bool) {
	if m, ok := verbMethod[verb]; ok {
		return m, true
	}
	switch {
	case verb == "search":
		return "GET", true
	case strings.HasPrefix(verb, "list-"):
		return "GET", true
	case strings.HasPrefix(verb, "create-on-"):
		return "POST", true
	}
	// Unknown verb: the resolver treats its trailing noun as a child
	// sub-collection to POST onto the resource.
	return "POST", false
}
