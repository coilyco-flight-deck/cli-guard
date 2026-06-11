package specverb

import (
	"encoding/json"
	"sort"
	"testing"
)

// TestPruneKeepsOnlyGrantedSurface prunes to the repo trio and asserts only the
// granted paths/methods + reachable defs survive, smaller and still mounting.
func TestPruneKeepsOnlyGrantedSurface(t *testing.T) {
	gf, full := loadFixtures(t)

	pruned, err := Prune(full, gf)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(pruned) >= len(full) {
		t.Errorf("pruned spec (%d) not smaller than full (%d)", len(pruned), len(full))
	}

	var doc struct {
		Paths       map[string]map[string]json.RawMessage `json:"paths"`
		Definitions map[string]json.RawMessage            `json:"definitions"`
	}
	if err := json.Unmarshal(pruned, &doc); err != nil {
		t.Fatalf("pruned spec is not valid json: %v", err)
	}

	wantPaths := []string{"/repos/{owner}/{repo}", "/user/repos"}
	gotPaths := keysOf(doc.Paths)
	if !equalSorted(gotPaths, wantPaths) {
		t.Errorf("pruned paths = %v, want %v", gotPaths, wantPaths)
	}
	// The repo path carries both granted methods; nothing else.
	repoMethods := keysOf(doc.Paths["/repos/{owner}/{repo}"])
	if !equalSorted(repoMethods, []string{"delete", "get"}) {
		t.Errorf("repo path methods = %v, want [delete get]", repoMethods)
	}
	// Only the body schema reachable from create survives; issue/label defs go.
	gotDefs := keysOf(doc.Definitions)
	if !equalSorted(gotDefs, []string{"CreateRepoOption"}) {
		t.Errorf("pruned definitions = %v, want [CreateRepoOption]", gotDefs)
	}

	// The pruned lock must still parse and mount identically.
	if _, err := parseSwagger(pruned); err != nil {
		t.Fatalf("pruned spec failed to parse: %v", err)
	}
	if _, err := Build(Config{Guardfile: gf, Spec: pruned}); err != nil {
		t.Fatalf("Build on pruned spec: %v", err)
	}
}

// TestPruneIsIdempotent re-pruning a pruned spec yields the same bytes, so a
// committed (already-pruned) lock compares cleanly against a freshly pruned one.
func TestPruneIsIdempotent(t *testing.T) {
	gf, full := loadFixtures(t)
	once, err := Prune(full, gf)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	twice, err := Prune(once, gf)
	if err != nil {
		t.Fatalf("Prune (second): %v", err)
	}
	if string(once) != string(twice) {
		t.Error("Prune is not idempotent: re-pruning changed the bytes")
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
