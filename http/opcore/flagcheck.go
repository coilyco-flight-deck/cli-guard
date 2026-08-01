package opcore

import "fmt"

// ReservedFlagNames are the universal per-leaf engine flags no promoted input
// (query, body, or form field) may shadow. See docs/specverb-request.md.
var ReservedFlagNames = map[string]bool{
	"dry-run": true, "query": true, "output": true, "body-file": true,
}

// CheckFlagCollisions rejects reserved or duplicate local inputs and duplicate
// outgoing query names. Both descriptor sources share this fail-closed check.
func CheckFlagCollisions(desc Descriptor) error {
	if err := validateBodyMappingMode(desc); err != nil {
		return fmt.Errorf("opcore: %s: %w", desc.VerbName, err)
	}
	seen := map[string]bool{}
	bodyInputs := append(append([]Field{}, desc.BodyFlags...), bodyMappingFields(desc.BodyMappings)...)
	all := append(append([]Field{}, desc.QueryFlags...), bodyInputs...)
	for _, f := range append(all, desc.FormFlags...) {
		if ReservedFlagNames[f.Name] {
			return fmt.Errorf("opcore: %s: input %q collides with a reserved engine flag (fail-closed)", desc.VerbName, f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("opcore: %s: two inputs both name %q (fail-closed)", desc.VerbName, f.Name)
		}
		seen[f.Name] = true
	}
	for _, f := range append(bodyInputs, desc.FormFlags...) {
		if f.UpstreamName != "" {
			return fmt.Errorf("opcore: %s: input %q sets an upstream name outside query parameters (fail-closed)", desc.VerbName, f.Name)
		}
	}
	wireNames := map[string]string{}
	for _, f := range desc.QueryFlags {
		if f.UpstreamName == f.Name && f.UpstreamName != "" {
			return fmt.Errorf("opcore: %s: query input %q repeats its local name as the upstream name (fail-closed)", desc.VerbName, f.Name)
		}
		wireName := f.QueryName()
		if localName, ok := wireNames[wireName]; ok {
			return fmt.Errorf("opcore: %s: query inputs %q and %q both map to upstream parameter %q (fail-closed)", desc.VerbName, localName, f.Name, wireName)
		}
		wireNames[wireName] = f.Name
	}
	return nil
}
