package specgen

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specgen/codegen"
	"gopkg.in/yaml.v3"
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

const skillSpecFixture = `{
	"swagger": "2.0",
	"info": {"title": "test", "version": "1"},
	"paths": {
		"/repos/{owner}/{repo}": {
			"get": {
				"operationId": "repoGet",
				"parameters": [
					{"name": "owner", "in": "path", "required": true, "type": "string"},
					{"name": "repo", "in": "path", "required": true, "type": "string"}
				],
				"responses": {"200": {"description": "ok"}}
			}
		},
		"/user/repos": {
			"post": {
				"operationId": "createCurrentUserRepo",
				"responses": {"201": {"description": "created"}}
			}
		}
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
	m, err := readMember(gfPath, filepath.Base(gfPath))
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

func writeMember(t *testing.T, root, name, src string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectRootDiscoversArbitraryNestedMembersInStableOrder(t *testing.T) {
	dir := t.TempDir()
	writeMember(t, dir, "forge/writes.kdl", guardfileFixture)
	selected := writeMember(t, dir, "cloud/reads.kdl", guardfileFixture)
	// A fleet configuration is recognized as unrelated project KDL and ignored.
	writeMember(t, dir, "fleet.kdl", `agents { schema-version 2; agent codex { binary codex } }`)
	g, err := loadGroup(Options{ProjectRoot: dir, GuardfilePath: selected})
	if err != nil {
		t.Fatalf("loadGroup: %v", err)
	}
	if got, want := []string{g.Members[0].Path, g.Members[1].Path}, []string{"cloud/reads.kdl", "forge/writes.kdl"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("member order = %v, want %v", got, want)
	}
	for _, m := range g.Members {
		if m.Params.GuardfileName != m.Path {
			t.Errorf("embed identity = %q, want %q", m.Params.GuardfileName, m.Path)
		}
	}
	main, err := g.render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"//go:embed cloud/reads.kdl", "//go:embed forge/writes.kdl"} {
		if !strings.Contains(string(main), want) {
			t.Errorf("rendered source missing %q", want)
		}
	}
}

func TestConventionalProjectRootDiscoversDotSpecgenRecursively(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, ".specgen")
	writeMember(t, project, "aguard/ops.kdl", guardfileFixture)
	t.Chdir(dir)

	g, err := loadGroup(Options{})
	if err != nil {
		t.Fatalf("loadGroup: %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if g.Dir != wantRoot {
		t.Errorf("project root = %q, want %q", g.Dir, wantRoot)
	}
	if len(g.Members) != 1 || g.Members[0].Path != "aguard/ops.kdl" {
		t.Errorf("conventional members = %+v", g.Members)
	}
}

func TestExplicitGuardfileKeepsLegacyDiscoveryBesideDotSpecgen(t *testing.T) {
	dir := t.TempDir()
	writeMember(t, filepath.Join(dir, ".specgen"), "aguard/ops.kdl", guardfileFixture)
	selected := writeMember(t, dir, "legacy.guardfile.kdl", strings.Replace(guardfileFixture, "wrap ward-kdl", "wrap legacy", 1))
	t.Chdir(dir)

	g, err := loadGroup(Options{GuardfilePath: selected})
	if err != nil {
		t.Fatalf("loadGroup: %v", err)
	}
	if g.Binary != "legacy" || len(g.Members) != 1 || g.Members[0].Path != "legacy.guardfile.kdl" {
		t.Errorf("legacy selection = %+v", g)
	}
}

func TestProjectRootUsesDistinctArtifactsForSameBasename(t *testing.T) {
	dir := t.TempDir()
	writeMember(t, dir, "cloud/api.kdl", strings.Replace(guardfileFixture, "ops forgejo", "ops cloud", 1))
	selected := writeMember(t, dir, "forge/api.kdl", guardfileFixture)
	g, err := loadGroup(Options{ProjectRoot: dir, GuardfilePath: selected})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{g.Members[0].Params.SpecLockName, g.Members[1].Params.SpecLockName}
	want := []string{"cloud/forgejo.swagger.lock.json.gz", "forge/forgejo.swagger.lock.json.gz"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("lock names = %v, want %v", got, want)
	}
	if got[0] == got[1] {
		t.Errorf("same-basename members must keep separate lock names: %v", got)
	}
}

func TestMaterializeModuleDirPreservesNestedMemberArtifacts(t *testing.T) {
	dir := t.TempDir()
	mems := []member{
		{Path: "cloud/api.kdl", Params: codegen.Params{GuardfileName: "cloud/api.kdl", SpecLockName: "cloud/api.lock.json.gz"}, Bytes: []byte("cloud")},
		{Path: "forge/api.kdl", Params: codegen.Params{GuardfileName: "forge/api.kdl", SpecLockName: "forge/api.lock.json.gz"}, Bytes: []byte("forge")},
	}
	if err := materializeModuleDir(dir, []byte("package main\n"), mems, map[string][]byte{"cloud/api.kdl": []byte("cloud lock"), "forge/api.kdl": []byte("forge lock")}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"cloud/api.kdl":          "cloud",
		"forge/api.kdl":          "forge",
		"cloud/api.lock.json.gz": "cloud lock",
		"forge/api.lock.json.gz": "forge lock",
	} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || string(got) != want {
			t.Errorf("artifact %s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestProjectRootRequiresSelectorForMultipleBinaries(t *testing.T) {
	dir := t.TempDir()
	selected := writeMember(t, dir, "a.kdl", guardfileFixture)
	writeMember(t, dir, "b.kdl", strings.Replace(guardfileFixture, "wrap ward-kdl", "wrap other", 1))
	if _, err := loadGroup(Options{ProjectRoot: dir}); err == nil || !strings.Contains(err.Error(), "pass --guardfile") || !strings.Contains(err.Error(), "other") {
		t.Fatalf("multi-binary discovery error = %v", err)
	}
	g, err := loadGroup(Options{ProjectRoot: dir, GuardfilePath: selected})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Members) != 1 || g.Binary != "ward-kdl" {
		t.Errorf("selected group = %+v", g)
	}
}

func TestProjectRootFailsClosedForConflictingMemberLock(t *testing.T) {
	dir := t.TempDir()
	selected := writeMember(t, dir, "a.kdl", guardfileFixture)
	writeMember(t, dir, "b.kdl", strings.Replace(guardfileFixture, "ops forgejo", "ops alternate", 1))
	if _, err := loadGroup(Options{ProjectRoot: dir, GuardfilePath: selected}); err == nil || !strings.Contains(err.Error(), "conflicting spec lock") {
		t.Fatalf("conflicting lock error = %v", err)
	}
}

func TestProjectRootFailsClosedForMalformedOperationAndSymlinkEscape(t *testing.T) {
	t.Run("malformed intended member", func(t *testing.T) {
		dir := t.TempDir()
		writeMember(t, dir, "broken.kdl", "wrap ward-kdl {")
		if _, err := loadGroup(Options{ProjectRoot: dir}); err == nil || !strings.Contains(err.Error(), "intended operation") {
			t.Fatalf("malformed operation error = %v", err)
		}
	})
	t.Run("symlink escape", func(t *testing.T) {
		dir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.kdl")
		if err := os.WriteFile(outside, []byte(guardfileFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "escape.kdl")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := loadGroup(Options{ProjectRoot: dir}); err == nil || !strings.Contains(err.Error(), "escapes project root") {
			t.Fatalf("symlink escape error = %v", err)
		}
	})
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
	for _, tc := range []struct {
		goos string
		want string
	}{
		{goos: "linux", want: "ward"},
		{goos: "windows", want: "ward.exe"},
	} {
		if got := executablePathForOS(tc.goos, renamedGroup.runtimeBinary()); got != tc.want {
			t.Errorf("%s runtime executable: got %q want %q", tc.goos, got, tc.want)
		}
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
	defaultKey := cacheKeyForGroup(defaultGroup)
	renamedKey := cacheKeyForGroup(renamedGroup)
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

func TestGenDoesNotEmitReferenceDocs(t *testing.T) {
	dir := t.TempDir()
	gfPath := filepath.Join(dir, "aws.guardfile.kdl")
	if err := os.WriteFile(gfPath, []byte(execFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Gen(Options{GuardfilePath: gfPath, Out: filepath.Join(dir, "main.go")}); err != nil {
		t.Fatalf("Gen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "aws.guardfile.md")); !os.IsNotExist(err) {
		t.Fatalf("Gen wrote retired reference doc: %v", err)
	}
}

func TestGenWritesDeterministicMixedTransportSkill(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "forgejo.guardfile.kdl")
	execPath := filepath.Join(dir, "aws.guardfile.kdl")
	if err := os.WriteFile(specPath, []byte(guardfileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, []byte(execFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSpecLock(filepath.Join(dir, "forgejo.swagger.lock.json.gz"), []byte(skillSpecFixture)); err != nil {
		t.Fatal(err)
	}
	skillsOut := filepath.Join(dir, "skills")
	opts := Options{
		GuardfilePath: specPath,
		BinaryName:    "fixtureguard",
		Out:           filepath.Join(dir, "main.go"),
		SkillsOut:     skillsOut,
	}
	if err := Gen(opts); err != nil {
		t.Fatalf("Gen: %v", err)
	}

	skillPath := filepath.Join(skillsOut, "fixtureguard", "SKILL.md")
	indexPath := filepath.Join(skillsOut, "fixtureguard", "references", "commands.yaml")
	firstSkill, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	firstIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read commands.yaml: %v", err)
	}
	if err := Gen(opts); err != nil {
		t.Fatalf("Gen second pass: %v", err)
	}
	secondSkill, _ := os.ReadFile(skillPath)
	secondIndex, _ := os.ReadFile(indexPath)
	if string(firstSkill) != string(secondSkill) || string(firstIndex) != string(secondIndex) {
		t.Fatal("generated skill output changed across identical inputs")
	}

	parts := strings.SplitN(string(firstSkill), "---", 3)
	if len(parts) != 3 {
		t.Fatalf("SKILL.md lacks frontmatter:\n%s", firstSkill)
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
		t.Fatalf("parse SKILL.md frontmatter: %v", err)
	}
	if meta.Name != "fixtureguard" || meta.Description == "" {
		t.Errorf("frontmatter = %+v", meta)
	}
	if strings.Contains(string(firstSkill), "get-caller-identity") || strings.Contains(string(firstSkill), "repoGet") {
		t.Errorf("SKILL.md copied exhaustive command detail:\n%s", firstSkill)
	}

	var index struct {
		Commands []struct {
			Path []string `yaml:"path"`
		} `yaml:"commands"`
	}
	if err := yaml.Unmarshal(firstIndex, &index); err != nil {
		t.Fatalf("parse commands.yaml: %v", err)
	}
	paths := map[string]bool{}
	for _, entry := range index.Commands {
		paths[strings.Join(entry.Path, " ")] = true
	}
	for _, want := range []string{
		"fixtureguard ops aws sts get-caller-identity",
		"fixtureguard ops aws s3 ls",
		"fixtureguard ops forgejo describe",
		"fixtureguard ops forgejo repos read",
		"fixtureguard ops forgejo repos create",
	} {
		if !paths[want] {
			t.Errorf("commands.yaml missing reachable command %q; got %v", want, paths)
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
	for _, tc := range []struct {
		goos   string
		suffix string
	}{
		{goos: "linux"},
		{goos: "windows", suffix: ".exe"},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := resolveBuildDestForOS(tc.goos, "", "forgejo-guardrail"); err == nil {
				t.Error("empty out should error")
			}
			// Existing directory -> binary name joined on.
			got, err := resolveBuildDestForOS(tc.goos, dir, "forgejo-guardrail")
			if err != nil {
				t.Fatalf("dir dest: %v", err)
			}
			if want := filepath.Join(dir, "forgejo-guardrail"+tc.suffix); got != want {
				t.Errorf("dir dest: got %q want %q", got, want)
			}
			// Trailing separator on a not-yet-existing dir -> treated as a directory.
			sub := filepath.Join(dir, "out") + string(os.PathSeparator)
			got, err = resolveBuildDestForOS(tc.goos, sub, "forgejo-guardrail")
			if err != nil {
				t.Fatalf("trailing-sep dest: %v", err)
			}
			if want := filepath.Join(dir, "out", "forgejo-guardrail"+tc.suffix); got != want {
				t.Errorf("trailing-sep dest: got %q want %q", got, want)
			}
			// Explicit file path -> platform-normalized, parent created.
			file := filepath.Join(dir, "nested", "mybin")
			got, err = resolveBuildDestForOS(tc.goos, file, "forgejo-guardrail")
			if err != nil {
				t.Fatalf("file dest: %v", err)
			}
			if want := file + tc.suffix; got != want {
				t.Errorf("file dest: got %q want %q", got, want)
			}
			if _, err := os.Stat(filepath.Dir(file)); err != nil {
				t.Errorf("parent dir not created: %v", err)
			}
		})
	}
}

func TestExecutablePathForOSDoesNotDuplicateEXE(t *testing.T) {
	for _, path := range []string{"aguard.exe", "aguard.EXE"} {
		if got := executablePathForOS("windows", path); got != path {
			t.Errorf("executablePathForOS(windows, %q) = %q", path, got)
		}
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
