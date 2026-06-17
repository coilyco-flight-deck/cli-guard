// Spec-lock pruning: reduce a full upstream Swagger document to the operations a
// Guardfile grants + their transitive schemas. See docs/specverb-driver.md.

package specverb

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
)

// Prune returns a minimal spec doc holding only the operations gf grants and
// their reachable schemas. Dispatches on version. See docs/specverb-driver.md.
func Prune(spec []byte, gf *guardfile.Guardfile) ([]byte, error) {
	if gf == nil {
		return nil, fmt.Errorf("specverb: prune: nil guardfile")
	}
	version, err := detectSpecVersion(spec)
	if err != nil {
		return nil, err
	}
	if version == 3 {
		return pruneOpenAPI3(spec, gf)
	}
	return pruneSwagger2(spec, gf)
}

// pruneSwagger2 keeps only the granted paths/methods and the transitive closure
// of Swagger 2.0 definitions they reach, re-emitting raw-fidelity JSON.
func pruneSwagger2(spec []byte, gf *guardfile.Guardfile) ([]byte, error) {
	typed, err := parseSwagger2(spec)
	if err != nil {
		return nil, err
	}
	keep, err := grantedPathMethods(typed, gf)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("specverb: prune: parse spec json: %w", err)
	}
	rawPaths, _ := doc["paths"].(map[string]any)
	rawDefs, _ := doc["definitions"].(map[string]any)

	newPaths := map[string]any{}
	seedRefs := map[string]bool{}
	for path, methods := range keep {
		pathObj, ok := rawPaths[path].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("specverb: prune: path %q absent from spec paths", path)
		}
		kept := map[string]any{}
		for m := range methods {
			opObj, ok := pathObj[m]
			if !ok {
				return nil, fmt.Errorf("specverb: prune: %s %s absent from spec", strings.ToUpper(m), path)
			}
			kept[m] = opObj
			collectRefs(opObj, seedRefs)
		}
		// Path-level shared parameters apply to every method under the path.
		if params, ok := pathObj["parameters"]; ok {
			kept["parameters"] = params
			collectRefs(params, seedRefs)
		}
		newPaths[path] = kept
	}

	doc["paths"] = newPaths
	closed := closeDefinitions(rawDefs, seedRefs)
	if len(closed) > 0 {
		doc["definitions"] = closed
	} else {
		delete(doc, "definitions")
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("specverb: prune: marshal pruned spec: %w", err)
	}
	return append(out, '\n'), nil
}

// grantedPathMethods resolves each `can` grant to its (path, lowercase method),
// failing closed like the engine so the lock holds exactly the buildable surface.
func grantedPathMethods(spec *swaggerSpec, gf *guardfile.Guardfile) (map[string]map[string]bool, error) {
	denied := deniedKeys(gf)
	keep := map[string]map[string]bool{}
	for _, g := range gf.Grants {
		if g.Modal != "can" {
			continue
		}
		if _, blocked := denied[grantKey{Verb: g.Verb, Resource: g.Resource}]; blocked {
			continue // deny beats allow: keep the blocked op out of the lock too
		}
		if g.Op == "" {
			return nil, fmt.Errorf("specverb: prune: grant %q %q has no `op` binding (deny-by-default)", g.Verb, g.Resource)
		}
		method, path, _, err := spec.findOp(g.Op)
		if err != nil {
			return nil, err
		}
		if keep[path] == nil {
			keep[path] = map[string]bool{}
		}
		keep[path][strings.ToLower(method)] = true
	}
	return keep, nil
}

// closeDefinitions returns the transitive closure of seed definition names over
// the raw definitions map: every definition reachable by following $ref chains.
func closeDefinitions(rawDefs map[string]any, seed map[string]bool) map[string]any {
	closed := map[string]any{}
	queue := make([]string, 0, len(seed))
	for name := range seed {
		queue = append(queue, name)
	}
	sort.Strings(queue) // deterministic visitation, though the result is order-free
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, done := closed[name]; done {
			continue
		}
		def, ok := rawDefs[name]
		if !ok {
			continue // a $ref to a missing definition: drop it, the engine fails closed later
		}
		closed[name] = def
		more := map[string]bool{}
		collectRefs(def, more)
		for n := range more {
			if _, done := closed[n]; !done {
				queue = append(queue, n)
			}
		}
	}
	return closed
}

// collectRefs walks an arbitrary decoded JSON value, adding every
// `#/definitions/X` name it finds to set.
func collectRefs(v any, set map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					if name := strings.TrimPrefix(s, "#/definitions/"); name != s {
						set[name] = true
					}
				}
			}
			collectRefs(val, set)
		}
	case []any:
		for _, e := range t {
			collectRefs(e, set)
		}
	}
}
