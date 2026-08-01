package opcore

import (
	"encoding/json"
	"fmt"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	kdl "github.com/calico32/kdl-go"
)

// parseBodyMappings reads a body block containing only
// `map "source.path" to="target"` declarations.
func parseBodyMappings(nodes []*kdl.Node) ([]BodyMapping, error) {
	out := make([]BodyMapping, 0, len(nodes))
	for _, n := range nodes {
		mapping, err := parseBodyMapping(n)
		if err != nil {
			return nil, err
		}
		out = append(out, mapping)
	}
	if err := validateBodyMappings(out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseBodyMapping(n *kdl.Node) (BodyMapping, error) {
	if n.Name() != "map" {
		return BodyMapping{}, fmt.Errorf("unknown node %q in mapped `body` block (want map; fail-closed)", n.Name())
	}
	args := n.Arguments()
	if len(args) != 1 {
		return BodyMapping{}, fmt.Errorf("`map` expects exactly one source path")
	}
	raw, ok := args[0].RawValue().(string)
	if !ok || raw == "" {
		return BodyMapping{}, fmt.Errorf("`map` expects one non-empty string source path")
	}
	if n.Children() != nil && len(n.Children().Nodes) > 0 {
		return BodyMapping{}, fmt.Errorf("`map` cannot have child nodes (fail-closed)")
	}
	props := n.Properties()
	if len(props) != 1 {
		return BodyMapping{}, fmt.Errorf("`map` needs exactly one `to=\"...\"` property")
	}
	to, exists := props["to"]
	if !exists {
		return BodyMapping{}, fmt.Errorf("`map` needs a `to=\"...\"` property")
	}
	target, ok := to.RawValue().(string)
	if !ok || target == "" {
		return BodyMapping{}, fmt.Errorf("`map` needs a non-empty string target")
	}
	return BodyMapping{SourcePath: strings.Split(raw, "."), Target: target}, nil
}

func validateBodyMappings(mappings []BodyMapping) error {
	seenSources := map[string]bool{}
	seenTargets := map[string]bool{}
	for _, mapping := range mappings {
		source, err := validateBodyMapping(mapping, seenSources, seenTargets)
		if err != nil {
			return err
		}
		seenSources[source] = true
		seenTargets[mapping.Target] = true
	}
	return validateBodyMappingShapes(seenSources)
}

func validateBodyMapping(mapping BodyMapping, seenSources, seenTargets map[string]bool) (string, error) {
	if len(mapping.SourcePath) == 0 {
		return "", fmt.Errorf("body `map` source path is empty (fail-closed)")
	}
	for _, segment := range mapping.SourcePath {
		if !validMappingName(segment) {
			return "", fmt.Errorf("body `map` source path %q has an invalid segment %q (fail-closed)", strings.Join(mapping.SourcePath, "."), segment)
		}
	}
	if !validMappingName(mapping.Target) {
		return "", fmt.Errorf("body `map` target %q is not a simple top-level key (fail-closed)", mapping.Target)
	}
	source := strings.Join(mapping.SourcePath, ".")
	if seenSources[source] {
		return "", fmt.Errorf("duplicate body `map` source %q (fail-closed)", source)
	}
	if seenTargets[mapping.Target] {
		return "", fmt.Errorf("duplicate body `map` target %q (fail-closed)", mapping.Target)
	}
	return source, nil
}

func validateBodyMappingShapes(sources map[string]bool) error {
	for source := range sources {
		for other := range sources {
			if source != other && strings.HasPrefix(other, source+".") {
				return fmt.Errorf("body `map` sources %q and %q have an ambiguous parent/child shape (fail-closed)", source, other)
			}
		}
	}
	return nil
}

func validateBodyMappingMode(d Descriptor) error {
	if len(d.BodyMappings) == 0 {
		return nil
	}
	if len(d.BodyFlags) > 0 {
		return fmt.Errorf("body fields and body mappings cannot be combined (fail-closed)")
	}
	if len(d.FixedBody) > 0 {
		return fmt.Errorf("fixed body and body mappings cannot be combined (fail-closed)")
	}
	return validateBodyMappings(d.BodyMappings)
}

func validMappingName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// bodyMappingFields projects mapping sources into the required nested input
// tree exposed to neutral-schema consumers.
func bodyMappingFields(mappings []BodyMapping) []Field {
	var out []Field
	for _, mapping := range mappings {
		if len(mapping.SourcePath) == 0 {
			continue
		}
		out = insertMappingField(out, mapping.SourcePath)
	}
	return out
}

func insertMappingField(fields []Field, path []string) []Field {
	name := path[0]
	for i := range fields {
		if fields[i].Name != name {
			continue
		}
		if len(path) > 1 {
			fields[i].Fields = insertMappingField(fields[i].Fields, path[1:])
		}
		return fields
	}
	f := Field{Name: name, Type: "string", Required: true}
	if len(path) > 1 {
		f.Type = "object"
		f.Fields = insertMappingField(nil, path[1:])
	}
	return append(fields, f)
}

func projectMappedBody(body map[string]any, mappings []BodyMapping) ([]byte, error) {
	out := make(map[string]any, len(mappings))
	for _, mapping := range mappings {
		value, err := mappedString(body, mapping.SourcePath)
		if err != nil {
			return nil, err
		}
		out[mapping.Target] = value
	}
	return json.Marshal(out)
}

func mappedString(body map[string]any, path []string) (string, error) {
	var current any = body
	for i, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return "", mappedBodyError(path, "is not an object at %q", strings.Join(path[:i], "."))
		}
		value, exists := object[segment]
		if !exists {
			return "", mappedBodyError(path, "is missing")
		}
		current = value
	}
	value, ok := current.(string)
	if !ok {
		return "", mappedBodyError(path, "must be a string")
	}
	return value, nil
}

func mappedBodyError(path []string, format string, args ...any) error {
	detail := fmt.Sprintf(format, args...)
	return exitcode.New(exitcode.UserError, "user_error",
		fmt.Errorf("mapped body field %q %s", strings.Join(path, "."), detail),
		"supply every mapped source path as a string")
}
