// Package catalog validates a repo's catalog config: it asserts the config
// YAML carries a top-level `catalog:` block with the required descriptor
// keys. It ports agentic-os's catalog-block-present pre-commit hook into the
// shared substrate so cli-guard consumers can enforce the same contract from
// Go (a doctor verb, a PreToolUse-adjacent shim, CI) instead of shelling out
// to the Python hook.
//
// Consumers (coily, ward, a pre-commit wrapper) supply the candidate config
// paths and wrap Check in their own surface; this package owns the rule
// engine, not the path-resolution policy or the CLI. cli-guard hardcodes no
// consumer's filesystem layout, so the caller passes the ordered candidates
// it wants probed (e.g. .ward/ward.yaml then .coily/coily.yaml).
//
// Schema reference: coilysiren/agentic-os-kai#420.
package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RequiredKeys are the keys a `catalog:` block must declare. Trivial repos
// still declare every key (use an empty value for list keys) rather than
// omitting it: missing is a violation, empty is fine.
var RequiredKeys = []string{
	"kind",
	"type",
	"system",
	"owner",
	"lifecycle",
	"description",
	"dependsOn",
}

// ListKeys are the required keys whose value must be a YAML sequence. An
// empty list ([]) satisfies the rule; a scalar or mapping does not.
var ListKeys = []string{
	"dependsOn",
}

// Problem is one contract violation, anchored to the config file and the
// line of the offending node. Consumers format `File:Line: Msg` however
// suits them. Mirrors allowlist.Problem so the two validators report alike.
type Problem struct {
	File string
	Line int
	Msg  string
}

// Check resolves the first existing path in candidates and validates its
// `catalog:` block. Candidates are probed in order; the first that exists on
// disk is the one checked, matching the upstream "ward.yaml first, then
// coily.yaml" fallback while leaving the path list to the caller.
//
// Rules:
//   - The config must carry a top-level `catalog:` mapping.
//   - Every key in RequiredKeys must be present inside it.
//   - Every key in ListKeys must hold a YAML sequence (use [] for empty).
//
// An empty []Problem with nil error means clean. A non-nil error means the
// inputs could not be resolved or parsed (no candidate exists, malformed
// YAML, non-mapping top level); rule violations come back as Problems, never
// as errors. Passing no candidates returns an error.
func Check(candidates ...string) ([]Problem, error) {
	path, doc, err := loadDoc(candidates)
	if err != nil {
		return nil, err
	}

	catalog := findChild(doc, "catalog")
	if catalog == nil {
		return []Problem{{
			File: path, Line: 1,
			Msg: "missing top-level `catalog:` block",
		}}, nil
	}
	if catalog.Kind != yaml.MappingNode {
		return []Problem{{
			File: path, Line: catalog.Line,
			Msg: "`catalog:` must be a mapping",
		}}, nil
	}
	return checkBlock(path, catalog), nil
}

// loadDoc resolves the first existing candidate, reads it, and returns the
// path alongside the top-level document mapping node. The returned errors
// cover unresolved or unparseable input (no candidate, malformed YAML,
// non-mapping top level); a clean parse with no `catalog:` block is not an
// error here, it is left for Check to report as a Problem.
func loadDoc(candidates []string) (string, *yaml.Node, error) {
	path := firstExisting(candidates)
	if path == "" {
		if len(candidates) == 0 {
			return "", nil, fmt.Errorf("catalog: no candidate config paths supplied")
		}
		return "", nil, fmt.Errorf("catalog: no config found among %v", candidates)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- caller-supplied config path
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", path, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", nil, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return "", nil, fmt.Errorf("%s top level must be a mapping", path)
	}
	return path, root.Content[0], nil
}

// checkBlock validates an already-confirmed `catalog:` mapping node against
// the required-key and list-key rules, anchoring each Problem to path.
func checkBlock(path string, catalog *yaml.Node) []Problem {
	var problems []Problem
	for _, k := range RequiredKeys {
		if findChild(catalog, k) == nil {
			problems = append(problems, Problem{
				File: path, Line: catalog.Line,
				Msg: fmt.Sprintf("catalog block missing required key %q (trivial repos still declare it, use [] for list keys)", k),
			})
		}
	}
	for _, k := range ListKeys {
		v := findChild(catalog, k)
		if v != nil && v.Kind != yaml.SequenceNode {
			problems = append(problems, Problem{
				File: path, Line: v.Line,
				Msg: fmt.Sprintf("catalog.%s must be a list (use [] for empty)", k),
			})
		}
	}
	return problems
}

// firstExisting returns the first candidate path that exists on disk, or ""
// when none do (or the slice is empty).
func firstExisting(candidates []string) string {
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(filepath.Clean(p)); err == nil {
			return p
		}
	}
	return ""
}

// findChild returns the value node for key in a mapping node, or nil when the
// key is absent. mapping.Content is a flat [key, value, key, value, ...]
// slice, so we step by two.
func findChild(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
