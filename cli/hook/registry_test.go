package hook_test

import (
	"os"
	"path/filepath"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/hook"
)

func testRegistry() hook.Registry {
	return hook.Registry{
		Default: "ward",
		Guards: []hook.Guard{
			{Name: "ward", Marker: ".ward/ward.yaml"},
			{Name: "coily", Marker: ".coily/coily.yaml"},
		},
	}
}

// writeMarker creates dir/<marker> (and parents) under root.
func writeMarker(t *testing.T, root, marker string) {
	t.Helper()
	full := filepath.Join(root, marker)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectDefaultOnEmpty(t *testing.T) {
	if got := testRegistry().Detect(""); got != "ward" {
		t.Errorf("Detect(\"\") = %q, want ward", got)
	}
}

func TestDetectFindsMarker(t *testing.T) {
	root := t.TempDir()
	writeMarker(t, root, ".coily/coily.yaml")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := testRegistry().Detect(sub); got != "coily" {
		t.Errorf("Detect walked up to coily marker, got %q", got)
	}
}

func TestDetectDefaultWhenNoMarker(t *testing.T) {
	if got := testRegistry().Detect(t.TempDir()); got != "ward" {
		t.Errorf("no marker should fall back to Default ward, got %q", got)
	}
}

func TestDetectRegistrationOrderWins(t *testing.T) {
	root := t.TempDir()
	writeMarker(t, root, ".ward/ward.yaml")
	writeMarker(t, root, ".coily/coily.yaml")
	// ward is registered first, so it wins when both markers coexist.
	if got := testRegistry().Detect(root); got != "ward" {
		t.Errorf("registration order should pick ward, got %q", got)
	}
}

func TestGuardLookup(t *testing.T) {
	r := testRegistry()
	if g, ok := r.Guard("coily"); !ok || g.Name != "coily" {
		t.Errorf("Guard(coily) = %+v,%v", g, ok)
	}
	// Unknown name falls back to Default.
	if g, ok := r.Guard("nope"); !ok || g.Name != "ward" {
		t.Errorf("Guard(nope) should fall back to ward, got %+v,%v", g, ok)
	}
}

func TestGuardNoDefault(t *testing.T) {
	r := hook.Registry{Guards: []hook.Guard{{Name: "ward"}}}
	if _, ok := r.Guard("nope"); ok {
		t.Error("unknown name with no Default should be ok=false")
	}
}
