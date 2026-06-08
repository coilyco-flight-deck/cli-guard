package specdrv

import (
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
}
