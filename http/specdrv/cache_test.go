package specdrv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheKeyStableAndPathScoped(t *testing.T) {
	a1, err := cacheKey("/repo/a/forgejo.guardfile.kdl")
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	a2, err := cacheKey("/repo/a/forgejo.guardfile.kdl")
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	b, err := cacheKey("/repo/b/forgejo.guardfile.kdl")
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	if a1 != a2 {
		t.Errorf("cacheKey not stable: %q != %q", a1, a2)
	}
	if a1 == b {
		t.Errorf("distinct guardfile paths collided on key %q", a1)
	}
	if len(a1) != 16 {
		t.Errorf("cacheKey len = %d, want 16", len(a1))
	}
}

func TestStaleDetectsEveryInput(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "ward-kdl")
	if err := os.MkdirAll(filepath.Dir(bin), 0o750); err != nil {
		t.Fatal(err)
	}
	base := stamp{
		GuardfileHash:    "gf",
		SpecLockHash:     "spec",
		DepLockHash:      "dep",
		GeneratorVersion: "v1",
	}

	// No binary yet -> stale.
	if !stale(dir, bin, base) {
		t.Error("expected stale when binary is missing")
	}
	if err := os.WriteFile(bin, []byte("#!/bin/true"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Binary present but no stamp -> stale.
	if !stale(dir, bin, base) {
		t.Error("expected stale when stamp is missing")
	}
	if err := writeStamp(dir, base); err != nil {
		t.Fatal(err)
	}

	// Matching stamp -> fresh.
	if stale(dir, bin, base) {
		t.Error("expected fresh when all inputs match")
	}

	// Each differing field independently triggers a rebuild.
	for name, mut := range map[string]func(stamp) stamp{
		"guardfile": func(s stamp) stamp { s.GuardfileHash = "x"; return s },
		"speclock":  func(s stamp) stamp { s.SpecLockHash = "x"; return s },
		"deplock":   func(s stamp) stamp { s.DepLockHash = "x"; return s },
		"generator": func(s stamp) stamp { s.GeneratorVersion = "x"; return s },
		"ldversion": func(s stamp) stamp { s.LDVersion = "x"; return s },
	} {
		if !stale(dir, bin, mut(base)) {
			t.Errorf("expected stale when %s hash differs", name)
		}
	}
}

func TestStampRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := stamp{GuardfileHash: "a", SpecLockHash: "b", DepLockHash: "c", GeneratorVersion: "v", BuiltAt: "now"}
	if err := writeStamp(dir, want); err != nil {
		t.Fatalf("writeStamp: %v", err)
	}
	got, ok := readStamp(dir)
	if !ok {
		t.Fatal("readStamp reported missing after write")
	}
	if *got != want {
		t.Errorf("stamp round-trip = %+v, want %+v", *got, want)
	}
}
