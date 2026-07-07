package opcore

import (
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
)

// OwnerRepoArg is the documented `args` sugar: a single `owner/name` value
// split across the leaf's `owner` and `repo` path params.
const OwnerRepoArg = "owner-repo"

// pathParamRe pulls `{name}` tokens out of a path template.
var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// FillPath substitutes the i-th positional value into the i-th `{...}` slot.
// The caller has already enforced len(values) == number of slots.
func FillPath(template string, values []string) string {
	i := 0
	return pathParamRe.ReplaceAllStringFunc(template, func(string) string {
		if i >= len(values) {
			return ""
		}
		v := values[i]
		i++
		return v
	})
}

// ArgBinder routes one poll arg onto a path param (by name or owner-repo sugar),
// a query param, or a body field, keeping leaf-request assembly flat.
type ArgBinder struct {
	pathIdx    map[string]int
	PathVals   []string
	queryNames map[string]bool
	flagNames  map[string]bool
	Query      neturl.Values
	BodyObj    map[string]any
}

// NewArgBinder builds a binder over one leaf's path/query/body flag surface.
func NewArgBinder(leaf Descriptor) *ArgBinder {
	b := &ArgBinder{
		pathIdx:    map[string]int{},
		PathVals:   make([]string, len(leaf.PathParams)),
		queryNames: map[string]bool{},
		flagNames:  map[string]bool{},
		Query:      neturl.Values{},
		BodyObj:    map[string]any{},
	}
	for i, p := range leaf.PathParams {
		b.pathIdx[p] = i
	}
	for _, f := range leaf.QueryFlags {
		b.queryNames[f.Name] = true
	}
	for _, f := range append(append([]Field{}, leaf.QueryFlags...), append(leaf.BodyFlags, leaf.FormFlags...)...) {
		b.flagNames[f.Name] = true
	}
	return b
}

// Bind routes one resolved arg value; the caller has already proved every arg
// targets something, so an unmatched name here is a no-op, not an error.
func (b *ArgBinder) Bind(name, val string) error {
	switch {
	case name == OwnerRepoArg:
		owner, repo, ok := splitOwnerRepo(val)
		if !ok {
			return exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("arg %q value %q is not owner/name", OwnerRepoArg, val), "pass it as owner/name")
		}
		b.PathVals[b.pathIdx["owner"]] = owner
		b.PathVals[b.pathIdx["repo"]] = repo
	case hasKey(b.pathIdx, name):
		b.PathVals[b.pathIdx[name]] = val
	case b.queryNames[name]:
		b.Query.Set(name, val)
	case b.flagNames[name]:
		b.BodyObj[name] = val
	}
	return nil
}

// RequireAllPaths fails closed if any path param went unbound, so a poll tick
// never fires an under-bound URL.
func (b *ArgBinder) RequireAllPaths() error {
	for p, i := range b.pathIdx {
		if b.PathVals[i] == "" {
			return exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("path param %q was not bound by any arg", p), "add an `args { ... }` binding for it")
		}
	}
	return nil
}

// splitOwnerRepo splits an `owner/name` value into its two halves; ok=false
// unless there are exactly two non-empty parts.
func splitOwnerRepo(v string) (owner, repo string, ok bool) {
	parts := strings.Split(v, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// hasKey reports whether m contains key.
func hasKey(m map[string]int, key string) bool {
	_, ok := m[key]
	return ok
}
