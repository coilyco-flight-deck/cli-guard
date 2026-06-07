package specdrv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDepLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LockName)
	in := &DepLock{
		Go:       "1.25.5",
		CLIGuard: "v0.4.0",
		GoMod:    "module specverbgen.local/build\n\ngo 1.25.5\n",
		GoSum:    []string{"b v1 h1:bbb=", "a v1 h1:aaa="},
	}
	if err := writeDepLock(path, in); err != nil {
		t.Fatalf("writeDepLock: %v", err)
	}
	out, err := readDepLock(path)
	if err != nil {
		t.Fatalf("readDepLock: %v", err)
	}
	if out.Version != depLockVersion {
		t.Errorf("Version = %d, want %d", out.Version, depLockVersion)
	}
	if out.GoMod != in.GoMod || out.CLIGuard != in.CLIGuard {
		t.Errorf("round-trip mismatch: %+v", out)
	}
	// writeDepLock sorts go.sum lines for a stable diff.
	if out.GoSum[0] != "a v1 h1:aaa=" || out.GoSum[1] != "b v1 h1:bbb=" {
		t.Errorf("go.sum not sorted: %v", out.GoSum)
	}
}

func TestReadDepLockRejectsBadVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LockName)
	if err := os.WriteFile(path, []byte(`{"version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDepLock(path); err == nil {
		t.Fatal("expected error for unsupported schema version")
	}
}

func TestWriteModuleFilesReplaysLock(t *testing.T) {
	dir := t.TempDir()
	dl := &DepLock{
		GoMod: "module specverbgen.local/build\n\ngo 1.25.5\n",
		GoSum: []string{"a v1 h1:aaa=", "b v1 h1:bbb="},
	}
	if err := writeModuleFiles(dir, dl); err != nil {
		t.Fatalf("writeModuleFiles: %v", err)
	}
	gotMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMod) != dl.GoMod {
		t.Errorf("go.mod = %q, want %q", gotMod, dl.GoMod)
	}
	gotSum, err := os.ReadFile(filepath.Join(dir, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotSum) != "a v1 h1:aaa=\nb v1 h1:bbb=\n" {
		t.Errorf("go.sum = %q", gotSum)
	}
}

func TestCLIGuardVersion(t *testing.T) {
	if got := cliGuardVersion("v1.2.3"); got != "v1.2.3" {
		t.Errorf("explicit ref = %q, want v1.2.3", got)
	}
	// With no ref, a dev build resolves to latest (BuildInfo is "(devel)" in tests).
	if got := cliGuardVersion(""); got == "" {
		t.Error("empty ref resolved to empty version")
	}
}
