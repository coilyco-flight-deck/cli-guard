package kdlspecs

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/kdlspecs/codegen"
)

const guardfileFixture = `wrap ward-kdl ops forgejo {
	spec forgejo.swagger.v1.json
	base-url "forgejo.coilysiren.me/api/v1"
	auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }
	can read repos { op "repoGet" }
	can create repos { op "createCurrentUserRepo" }
}`

// execFixture is an exec-dialect member sharing the ward-kdl binary with the
// spec fixture above, so the two merge into one binary.
const execFixture = `wrap ward-kdl ops aws {
	exec aws
	can run sts get-caller-identity
	can run s3 ls {
		deny-when arg0 matches "*tfstate*"
	}
}`

func TestSniffTransport(t *testing.T) {
	spec, err := sniffTransport([]byte(guardfileFixture))
	if err != nil || spec != codegen.TransportSpec {
		t.Errorf("spec fixture: got (%q, %v), want spec", spec, err)
	}
	ex, err := sniffTransport([]byte(execFixture))
	if err != nil || ex != codegen.TransportExec {
		t.Errorf("exec fixture: got (%q, %v), want exec", ex, err)
	}
}

func TestReadMemberDispatchesExec(t *testing.T) {
	dir := t.TempDir()
	gfPath := filepath.Join(dir, "aws.guardfile.kdl")
	if err := os.WriteFile(gfPath, []byte(execFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := readMember(gfPath)
	if err != nil {
		t.Fatalf("readMember: %v", err)
	}
	if !m.isExec() {
		t.Errorf("want exec member, got transport %q", m.Params.Transport)
	}
	if m.ExecGF == nil || m.GF != nil {
		t.Errorf("exec member should carry ExecGF and no spec GF (GF=%v ExecGF=%v)", m.GF, m.ExecGF)
	}
	if m.Params.Binary != "ward-kdl" {
		t.Errorf("binary: got %q want ward-kdl", m.Params.Binary)
	}
	if m.Params.SpecLockName != "" || m.Params.SpecURL != "" {
		t.Errorf("exec member should have no spec lock/url, got %+v", m.Params)
	}
}

func TestLoadGroupMergesSpecAndExec(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "forgejo.guardfile.kdl"), []byte(guardfileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aws.guardfile.kdl"), []byte(execFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := loadGroup(Options{GuardfilePath: filepath.Join(dir, "forgejo.guardfile.kdl")})
	if err != nil {
		t.Fatalf("loadGroup: %v", err)
	}
	if len(g.Members) != 2 {
		t.Fatalf("want 2 merged members, got %d", len(g.Members))
	}
	main, err := g.render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", main, parser.AllErrors); err != nil {
		t.Fatalf("merged main.go does not parse: %v\n%s", err, main)
	}
	src := string(main)
	for _, want := range []string{"specverb.Mount(app", "execverb.Mount(app", "//go:embed aws.guardfile.kdl"} {
		if !strings.Contains(src, want) {
			t.Errorf("merged main.go missing %q", want)
		}
	}
}

func TestLoadGroupKeepsSourceBinaryWhenRuntimeNameChanges(t *testing.T) {
	dir := t.TempDir()
	gfPath := filepath.Join(dir, "forgejo.guardfile.kdl")
	if err := os.WriteFile(gfPath, []byte(guardfileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	defaultGroup, err := loadGroup(Options{GuardfilePath: gfPath})
	if err != nil {
		t.Fatalf("load default group: %v", err)
	}
	renamedGroup, err := loadGroup(Options{GuardfilePath: gfPath, BinaryName: "ward"})
	if err != nil {
		t.Fatalf("load renamed group: %v", err)
	}
	if renamedGroup.Binary != "ward-kdl" {
		t.Errorf("source binary changed: got %q want ward-kdl", renamedGroup.Binary)
	}
	if renamedGroup.runtimeBinary() != "ward" {
		t.Errorf("runtime binary: got %q want ward", renamedGroup.runtimeBinary())
	}
	main, err := renamedGroup.render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	src := string(main)
	for _, want := range []string{`Name: "ward"`, `WARD_KDL_OPS_FORGEJO_SPEC`} {
		if !strings.Contains(src, want) {
			t.Errorf("renamed main.go missing %q", want)
		}
	}
	defaultKey, err := cacheKeyForGroup(defaultGroup)
	if err != nil {
		t.Fatalf("default cache key: %v", err)
	}
	renamedKey, err := cacheKeyForGroup(renamedGroup)
	if err != nil {
		t.Fatalf("renamed cache key: %v", err)
	}
	if defaultKey == renamedKey {
		t.Errorf("renamed build reused default cache key %q", defaultKey)
	}
}

func TestLoadGroupRejectsInvalidRuntimeBinaryName(t *testing.T) {
	dir := t.TempDir()
	gfPath := filepath.Join(dir, "forgejo.guardfile.kdl")
	if err := os.WriteFile(gfPath, []byte(guardfileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{" ward", "ward ", "../ward", "bin/ward", `bin\ward`, ".", "..", "ward\x00"} {
		t.Run(name, func(t *testing.T) {
			_, err := loadGroup(Options{GuardfilePath: gfPath, BinaryName: name})
			if err == nil {
				t.Fatal("expected invalid runtime binary name to error")
			}
			if !strings.Contains(err.Error(), "--binary") {
				t.Fatalf("error should mention --binary, got %v", err)
			}
		})
	}
}

func TestGenEmitsExecReferenceDoc(t *testing.T) {
	dir := t.TempDir()
	gfPath := filepath.Join(dir, "aws.guardfile.kdl")
	if err := os.WriteFile(gfPath, []byte(execFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Gen(Options{GuardfilePath: gfPath, Out: filepath.Join(dir, "main.go")}); err != nil {
		t.Fatalf("Gen: %v", err)
	}
	doc, err := os.ReadFile(filepath.Join(dir, "aws.guardfile.md"))
	if err != nil {
		t.Fatalf("read exec reference doc: %v", err)
	}
	for _, want := range []string{"# ward-kdl ops aws", "Exec-dialect CLI", "## ward-kdl ops aws s3 ls", "denies when arg0 matches"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("exec reference doc missing %q", want)
		}
	}
}

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
