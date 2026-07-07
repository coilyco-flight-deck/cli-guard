package opcore

import "fmt"

// ReservedFlagNames are the universal per-leaf engine flags no promoted input
// (query, body, or form field) may shadow. See docs/specverb-request.md.
var ReservedFlagNames = map[string]bool{
	"dry-run": true, "query": true, "output": true, "body-file": true,
}

// CheckFlagCollisions fails closed when a promoted input shadows a reserved engine
// flag or another input on the same leaf. Shared by both descriptor sources.
func CheckFlagCollisions(desc Descriptor) error {
	seen := map[string]bool{}
	all := append(append([]Field{}, desc.QueryFlags...), desc.BodyFlags...)
	for _, f := range append(all, desc.FormFlags...) {
		if ReservedFlagNames[f.Name] {
			return fmt.Errorf("opcore: %s: input %q collides with a reserved engine flag (fail-closed)", desc.VerbName, f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("opcore: %s: two inputs both name %q (fail-closed)", desc.VerbName, f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}
