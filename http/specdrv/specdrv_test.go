package specdrv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const guardfileFixture = `wrap ward-kdl ops forgejo {
	spec forgejo.swagger.v1.json
	base-url "forgejo.coilysiren.me/api/v1"
	auth header-token { header Authorization; prefix "token "; ssm "/forgejo/api-token" }
	can read repos
	can create repos
}`

func TestDiffSpecsDetectsOperationDrift(t *testing.T) {
	committed := []byte(`{
		"paths": { "/repos": {"get": {}}, "/orgs": {"get": {}} },
		"definitions": { "Repo": {"type": "object"} }
	}`)
	live := []byte(`{
		"paths": { "/repos": {"get": {}, "post": {}}, "/teams": {"get": {}} },
		"definitions": { "Repo": {"type": "object"} }
	}`)
	drift, err := diffSpecs(committed, live)
	if err != nil {
		t.Fatalf("diffSpecs: %v", err)
	}
	got := strings.Join(drift, "\n")
	for _, want := range []string{"paths: + /teams", "paths: - /orgs", "paths: ~ /repos"} {
		if !strings.Contains(got, want) {
			t.Errorf("drift missing %q; got:\n%s", want, got)
		}
	}
}

func TestDiffSpecsIgnoresKeyReordering(t *testing.T) {
	committed := []byte(`{"paths": {"/a": {"get": {}, "post": {}}}}`)
	live := []byte(`{"paths": {"/a": {"post": {}, "get": {}}}}`)
	drift, err := diffSpecs(committed, live)
	if err != nil {
		t.Fatalf("diffSpecs: %v", err)
	}
	if len(drift) != 0 {
		t.Errorf("expected no drift on reordering, got %v", drift)
	}
}

func TestGenWritesToExplicitOut(t *testing.T) {
	dir := t.TempDir()
	gfPath := filepath.Join(dir, "forgejo.guardfile.kdl")
	if err := os.WriteFile(gfPath, []byte(guardfileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "main.go")
	if err := Gen(Options{GuardfilePath: gfPath, Out: out}); err != nil {
		t.Fatalf("Gen: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read generated main.go: %v", err)
	}
	if !strings.Contains(string(b), `Name: "ward-kdl"`) {
		t.Errorf("generated main.go missing the binary name")
	}
}

func TestVerbsErrorWithoutGuardfile(t *testing.T) {
	if err := Gen(Options{}); err == nil {
		t.Error("Gen with no guardfile should error")
	}
	if err := Skew(Options{GuardfilePath: filepath.Join(t.TempDir(), "missing.kdl")}); err == nil {
		t.Error("Skew with missing guardfile should error")
	}
	if err := Build(Options{GuardfilePath: filepath.Join(t.TempDir(), "missing.kdl")}); err == nil {
		t.Error("Build with missing guardfile should error")
	}
}

func TestBuildRefusesWithoutLocks(t *testing.T) {
	dir := t.TempDir()
	gfPath := filepath.Join(dir, "forgejo.guardfile.kdl")
	if err := os.WriteFile(gfPath, []byte(guardfileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	// No spec lock / specverb.lock beside the Guardfile: Build must refuse with
	// ErrNoLock rather than attempt a network fetch, exactly like Run.
	err := Build(Options{GuardfilePath: gfPath, Out: filepath.Join(dir, "bin")})
	if !errors.Is(err, ErrNoLock) {
		t.Fatalf("Build without locks: want ErrNoLock, got %v", err)
	}
}

func TestResolveBuildDest(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveBuildDest("", "forgejo-guardrail"); err == nil {
		t.Error("empty out should error")
	}
	// Existing directory -> binary name joined on.
	got, err := resolveBuildDest(dir, "forgejo-guardrail")
	if err != nil {
		t.Fatalf("dir dest: %v", err)
	}
	if want := filepath.Join(dir, "forgejo-guardrail"); got != want {
		t.Errorf("dir dest: got %q want %q", got, want)
	}
	// Trailing separator on a not-yet-existing dir -> treated as a directory.
	sub := filepath.Join(dir, "out") + string(os.PathSeparator)
	got, err = resolveBuildDest(sub, "forgejo-guardrail")
	if err != nil {
		t.Fatalf("trailing-sep dest: %v", err)
	}
	if want := filepath.Join(dir, "out", "forgejo-guardrail"); got != want {
		t.Errorf("trailing-sep dest: got %q want %q", got, want)
	}
	// Explicit file path -> used verbatim, parent created.
	file := filepath.Join(dir, "nested", "mybin")
	got, err = resolveBuildDest(file, "forgejo-guardrail")
	if err != nil {
		t.Fatalf("file dest: %v", err)
	}
	if got != file {
		t.Errorf("file dest: got %q want %q", got, file)
	}
	if _, err := os.Stat(filepath.Dir(file)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}

func TestCopyExecutable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("#!/bin/true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	if err := copyExecutable(src, dest); err != nil {
		t.Fatalf("copyExecutable: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("dest is not executable: mode %v", info.Mode())
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "#!/bin/true\n" {
		t.Errorf("dest content mismatch: %q (err %v)", b, err)
	}
}
