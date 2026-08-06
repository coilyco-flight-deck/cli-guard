package embedfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeWritesAbsolutePrivateFilesAndCleansUp(t *testing.T) {
	resolved, cleanup, err := Materialize("guard", map[int]map[string]Source{
		2: {"scripts/measure.py": {Path: "ops/scripts/measure.py", Data: []byte("print('ok')\n")}},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	path := resolved[2]["scripts/measure.py"]
	if !filepath.IsAbs(path) {
		t.Errorf("materialized path is not absolute: %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "print('ok')\n" {
		t.Errorf("materialized data = %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("materialized mode = %o, want no group/other permissions", info.Mode().Perm())
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("runtime directory remains after cleanup: %v", err)
	}
}

func TestMaterializeRejectsUnsafeOrConflictingGeneratedPaths(t *testing.T) {
	for name, sources := range map[string]map[int]map[string]Source{
		"traversal": {0: {"x": {Path: "../x", Data: []byte("x")}}},
		"absolute":  {0: {"x": {Path: "/x", Data: []byte("x")}}},
		"conflict": {
			0: {"x": {Path: "same", Data: []byte("x")}},
			1: {"y": {Path: "same", Data: []byte("y")}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Materialize("guard", sources); err == nil {
				t.Fatal("expected materialization to fail closed")
			}
		})
	}
}
