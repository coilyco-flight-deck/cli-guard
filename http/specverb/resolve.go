// Convention-based operation resolution: the spec-agnostic bridge from a grant's
// verb+resource to a spec operation. See docs/specverb-resolution.md.

package specverb

import (
	"fmt"
	"sort"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
)

// resolveShape distinguishes an item leaf (path ends in a {param} run, e.g.
// /repos/{owner}/{repo}) from a collection leaf (ends in a static segment).
type resolveShape int

const (
	shapeItem resolveShape = iota
	shapeCollection
)

// verbConvention maps a canonical CRUD verb to the HTTP method(s) and path shape
// it names. Non-CRUD verbs do not auto-resolve and must carry an explicit `op`.
type verbConvention struct {
	methods []string // tried in order; first method with a unique match wins
	shape   resolveShape
}

// verbConventions is the closed set of auto-resolvable verbs. `edit` tries PATCH
// then PUT so JSON-merge (Forgejo) and whole-replace (Trello) APIs both resolve.
var verbConventions = map[string]verbConvention{
	"get":    {methods: []string{"GET"}, shape: shapeItem},
	"view":   {methods: []string{"GET"}, shape: shapeItem},
	"list":   {methods: []string{"GET"}, shape: shapeCollection},
	"create": {methods: []string{"POST"}, shape: shapeCollection},
	"edit":   {methods: []string{"PATCH", "PUT"}, shape: shapeItem},
	"delete": {methods: []string{"DELETE"}, shape: shapeItem},
}

// resolveOp returns the operationId a grant authorizes. An explicit g.Op wins;
// otherwise verb+resource resolve by convention, failing closed unless unique.
func resolveOp(spec *swaggerSpec, g guardfile.Grant) (string, error) {
	if g.Op != "" {
		return g.Op, nil
	}
	conv, ok := verbConventions[g.Verb]
	if !ok {
		return "", fmt.Errorf("specverb: cannot resolve %q %q: verb %q has no resolution convention; add `op \"<operationId>\"`", g.Verb, g.Resource, g.Verb)
	}
	want := singularize(g.Resource)
	var ambiguous []string
	for _, method := range conv.methods {
		cands := matchOps(spec, method, conv.shape, want)
		switch {
		case len(cands) == 1:
			return cands[0], nil
		case len(cands) > 1:
			ambiguous = cands
		}
	}
	if len(ambiguous) > 1 {
		sort.Strings(ambiguous)
		return "", fmt.Errorf("specverb: cannot resolve %q %q: %d operations match (%s); add `op \"<operationId>\"` to pin one",
			g.Verb, g.Resource, len(ambiguous), strings.Join(ambiguous, ", "))
	}
	return "", fmt.Errorf("specverb: cannot resolve %q %q: no %s operation whose path resource is %q; add `op \"<operationId>\"`",
		g.Verb, g.Resource, strings.Join(conv.methods, "/"), g.Resource)
}

// matchOps returns the operationIds whose method and path resource segment match
// the wanted (method, shape, singular resource).
func matchOps(spec *swaggerSpec, method string, shape resolveShape, want string) []string {
	var out []string
	for path, methods := range spec.Paths {
		op, ok := methods[strings.ToLower(method)]
		if !ok || op.OperationID == "" {
			continue
		}
		seg, ok := resourceSegment(path, shape)
		if !ok || singularize(seg) != want {
			continue
		}
		out = append(out, op.OperationID)
	}
	return out
}

// resourceSegment returns the static segment naming the resource: last static
// before the trailing {param} run (item), or the trailing static (collection).
func resourceSegment(path string, shape resolveShape) (string, bool) {
	segs := splitPath(path)
	if len(segs) == 0 {
		return "", false
	}
	switch shape {
	case shapeCollection:
		if last := segs[len(segs)-1]; !isParam(last) {
			return last, true
		}
		return "", false
	case shapeItem:
		i := len(segs) - 1
		if !isParam(segs[i]) {
			return "", false // an item path ends in {param}
		}
		for i >= 0 && isParam(segs[i]) {
			i-- // walk back over the trailing {param} run to the naming segment
		}
		if i < 0 {
			return "", false
		}
		return segs[i], true
	}
	return "", false
}

// splitPath splits a URL path template into its non-empty segments.
func splitPath(path string) []string {
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// isParam reports whether a path segment is a `{name}` template parameter.
func isParam(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}

// singularize lowercases and strips a trailing plural "s" (not "ss"), matching a
// path segment (`repos`) to a grant's singular resource (`repo`).
func singularize(s string) string {
	s = strings.ToLower(s)
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}
