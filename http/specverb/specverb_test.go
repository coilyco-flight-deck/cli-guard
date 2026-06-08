package specverb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/audit"
	"github.com/urfave/cli/v3"
)

// loadFixtures reads the proving-slice Guardfile and spec from testdata.
func loadFixtures(t *testing.T) (*guardfile.Guardfile, []byte) {
	t.Helper()
	kdl, err := os.ReadFile(filepath.Join("testdata", "forgejo.kdl"))
	if err != nil {
		t.Fatalf("read guardfile: %v", err)
	}
	gf, err := guardfile.Parse(kdl)
	if err != nil {
		t.Fatalf("parse guardfile: %v", err)
	}
	spec, err := os.ReadFile(filepath.Join("testdata", "forgejo.swagger.v1.json"))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	return gf, spec
}

// runTree mounts the built command under `ward ops` and runs argv, capturing
// stdout. The leading "ward","ops" mirror how the real consumer mounts it.
func runTree(t *testing.T, cfg Config, argv ...string) (string, error) {
	t.Helper()
	root, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	app := &cli.Command{
		Name:     "ward",
		Commands: []*cli.Command{{Name: "ops", Commands: []*cli.Command{root}}},
	}

	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	runErr := app.Run(context.Background(), append([]string{"ward", "ops"}, argv...))
	_ = w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

// failingTransport fails any live HTTP call, so a dry-run test proves the wire
// is never touched.
type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatalf("dry-run must not fire an HTTP request")
	return nil, http.ErrUseLastResponse // unreachable: t.Fatalf stops the goroutine
}

func TestBuildMountsProvingSlice(t *testing.T) {
	gf, spec := loadFixtures(t)
	root, err := Build(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if root.Name != "forgejo" {
		t.Errorf("root name = %q, want forgejo", root.Name)
	}
	repo := childNamed(root, "repo")
	if repo == nil {
		t.Fatalf("want a `repo` group, got %v", names(root.Commands))
	}
	// describe is mounted as a sibling verb on the group.
	if childNamed(root, "describe") == nil {
		t.Fatalf("want a `describe` verb on the group, got %v", names(root.Commands))
	}
	leaves := names(repo.Commands)
	want := map[string]bool{"get": true, "create": true, "delete": true}
	if len(leaves) != 3 {
		t.Fatalf("want 3 leaves, got %v", leaves)
	}
	for _, l := range leaves {
		if !want[l] {
			t.Errorf("unexpected leaf %q", l)
		}
	}
}

func names(cmds []*cli.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name
	}
	return out
}

func TestDenyByDefault(t *testing.T) {
	_, spec := loadFixtures(t)
	// `labels` has no expansion-table row: the build must fail closed.
	gf, err := guardfile.Parse([]byte(`wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		auth header-token { header Authorization; ssm "/forgejo/api-token" }
		can read labels
	}`))
	if err != nil {
		t.Fatalf("parse guardfile: %v", err)
	}
	if _, err := Build(Config{Guardfile: gf, Spec: spec}); err == nil {
		t.Fatal("expected deny-by-default error for an unmapped grant, got nil")
	}
}

func TestSwagger2Required(t *testing.T) {
	gf, _ := loadFixtures(t)
	_, err := Build(Config{Guardfile: gf, Spec: []byte(`{"openapi":"3.0.0","paths":{"/x":{}}}`)})
	if err == nil {
		t.Fatal("expected an error for a non-2.0 spec, got nil")
	}
}

func TestDryRunCreate(t *testing.T) {
	gf, spec := loadFixtures(t)
	cfg := Config{
		Guardfile:  gf,
		Spec:       spec,
		HTTPClient: &http.Client{Transport: failingTransport{t}},
		Token: func(context.Context, string) (string, error) {
			t.Fatal("dry-run must not resolve the auth secret")
			return "", nil
		},
	}
	out, err := runTree(t, cfg, "forgejo", "repo", "create", "--name", "demo", "--private", "--dry-run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"POST", "/user/repos", "demo", "private", "redacted"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "token actual") {
		t.Errorf("dry-run leaked a secret:\n%s", out)
	}
}

func TestLiveCreate(t *testing.T) {
	gf, spec := loadFixtures(t)
	var gotAuth, gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"full_name":"kai/demo"}`))
	}))
	defer srv.Close()

	cfg := Config{
		Guardfile: gf,
		Spec:      spec,
		BaseURL:   srv.URL,
		Token:     func(context.Context, string) (string, error) { return "sekret", nil },
	}
	out, err := runTree(t, cfg, "forgejo", "repo", "create", "--name", "demo", "--output", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/user/repos" {
		t.Errorf("server saw %s %s, want POST /user/repos", gotMethod, gotPath)
	}
	if gotAuth != "token sekret" {
		t.Errorf("auth header = %q, want %q", gotAuth, "token sekret")
	}
	if !strings.Contains(gotBody, `"name":"demo"`) {
		t.Errorf("body = %q, want name=demo", gotBody)
	}
	// an unset optional must not be sent
	if strings.Contains(gotBody, "private") {
		t.Errorf("unset optional leaked into body: %q", gotBody)
	}
	if !strings.Contains(out, "kai/demo") {
		t.Errorf("rendered response missing full_name:\n%s", out)
	}
}

func TestLiveDeleteFillsPathParams(t *testing.T) {
	gf, spec := loadFixtures(t)
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := Config{
		Guardfile: gf,
		Spec:      spec,
		BaseURL:   srv.URL,
		Token:     func(context.Context, string) (string, error) { return "sekret", nil },
	}
	out, err := runTree(t, cfg, "forgejo", "repo", "delete", "kai", "demo")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/repos/kai/demo" {
		t.Errorf("server saw %s %s, want DELETE /repos/kai/demo", gotMethod, gotPath)
	}
	if !strings.Contains(out, "ok:") {
		t.Errorf("empty 2xx should print a confirmation, got:\n%s", out)
	}
}

func TestPositionalArgCountValidated(t *testing.T) {
	gf, spec := loadFixtures(t)
	cfg := Config{
		Guardfile: gf,
		Spec:      spec,
		BaseURL:   "http://127.0.0.1:0",
		Token:     func(context.Context, string) (string, error) { return "x", nil },
	}
	// delete wants <owner> <repo>; one positional is a user error before any wire call.
	if _, err := runTree(t, cfg, "forgejo", "repo", "delete", "kai"); err == nil {
		t.Fatal("expected a positional-arg-count error, got nil")
	}
}

// TestComposesWithVerbWrap proves the engine mounts under the real verb
// pipeline (audit + argv gate), not just the identity wrap.
func TestComposesWithVerbWrap(t *testing.T) {
	gf, spec := loadFixtures(t)
	w := &audit.Writer{
		Path: filepath.Join(t.TempDir(), "audit.jsonl"),
		Now:  func() time.Time { return time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC) },
	}
	t.Cleanup(func() { _ = w.Close() })

	cfg := Config{
		Guardfile:  gf,
		Spec:       spec,
		Wrap:       func(s verb.Spec) cli.ActionFunc { return verb.Wrap(s, w) },
		HTTPClient: &http.Client{Transport: failingTransport{t}},
		Token:      func(context.Context, string) (string, error) { return "x", nil },
	}
	if _, err := runTree(t, cfg, "forgejo", "repo", "create", "--name", "demo", "--dry-run"); err != nil {
		t.Fatalf("run through verb.Wrap: %v", err)
	}
	// the wrapped action wrote an audit row
	if data, _ := os.ReadFile(w.Path); !strings.Contains(string(data), "ward.ops.forgejo.repo.create") {
		t.Errorf("audit row missing the verb name; got:\n%s", string(data))
	}
}

// TestMountGeneratesIntermediatePath proves Mount grafts the built group onto a
// root, creating the `ops` path segment the Guardfile names but root lacks.
func TestMountGeneratesIntermediatePath(t *testing.T) {
	gf, spec := loadFixtures(t)
	root := &cli.Command{Name: "ward"}
	if err := Mount(root, Config{Guardfile: gf, Spec: spec}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	ops := childNamed(root, "ops")
	if ops == nil {
		t.Fatalf("root has no generated `ops` group; got %v", names(root.Commands))
	}
	if childNamed(ops, "forgejo") == nil {
		t.Fatalf("`ops` has no `forgejo` group; got %v", names(ops.Commands))
	}
}

// TestMountReusesExistingPath proves Mount attaches to an `ops` group that
// already exists rather than creating a duplicate.
func TestMountReusesExistingPath(t *testing.T) {
	gf, spec := loadFixtures(t)
	existing := &cli.Command{Name: "ops"}
	root := &cli.Command{Name: "ward", Commands: []*cli.Command{existing}}
	if err := Mount(root, Config{Guardfile: gf, Spec: spec}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if n := len(root.Commands); n != 1 {
		t.Fatalf("root should keep one `ops` group, got %d: %v", n, names(root.Commands))
	}
	if childNamed(existing, "forgejo") == nil {
		t.Errorf("existing `ops` did not gain `forgejo`; got %v", names(existing.Commands))
	}
}

func TestDefaultScheme(t *testing.T) {
	cases := map[string]string{
		"forgejo.coilysiren.me/api/v1":         "https://forgejo.coilysiren.me/api/v1",
		"https://forgejo.coilysiren.me/api/v1": "https://forgejo.coilysiren.me/api/v1",
		"http://127.0.0.1:8080":                "http://127.0.0.1:8080",
		"":                                     "",
	}
	for in, want := range cases {
		if got := defaultScheme(in); got != want {
			t.Errorf("defaultScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDescribeModel proves the surface model mirrors the mounted verbs: one
// VerbInfo per grant, with auth scope as the token path (never the secret).
func TestDescribeModel(t *testing.T) {
	gf, spec := loadFixtures(t)
	surface, err := Describe(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got, want := surface.Auth.SSM, "/forgejo/api-token"; got != want {
		t.Errorf("auth ssm = %q, want %q", got, want)
	}
	if surface.Auth.Header != "Authorization" {
		t.Errorf("auth header = %q, want Authorization", surface.Auth.Header)
	}
	byLeaf := map[string]VerbInfo{}
	for _, v := range surface.Verbs {
		byLeaf[v.Leaf] = v
	}
	if len(byLeaf) != 3 {
		t.Fatalf("want 3 verbs in the model, got %d: %+v", len(byLeaf), surface.Verbs)
	}
	create := byLeaf["create"]
	if create.Method != "POST" || create.Path != "/user/repos" {
		t.Errorf("create = %s %s, want POST /user/repos", create.Method, create.Path)
	}
	if create.Grant != "can create repos" {
		t.Errorf("create grant = %q, want %q", create.Grant, "can create repos")
	}
	if create.Name != "ward.ops.forgejo.repo.create" {
		t.Errorf("create dotted name = %q", create.Name)
	}
	del := byLeaf["delete"]
	if !del.Destructive {
		t.Errorf("delete should be flagged destructive")
	}
	if del.Describe == "" {
		t.Errorf("delete should carry the guardfile describe note")
	}
	// path params are modeled as required, kind=path, in invocation order.
	if len(del.Params) != 2 || del.Params[0].Name != "owner" || del.Params[0].Kind != "path" {
		t.Errorf("delete params = %+v, want owner/repo path params", del.Params)
	}
}

// TestDescribeVerbRenders proves `describe` is a real, runnable verb on the
// group whose default output is the readable prose reference.
func TestDescribeVerbRenders(t *testing.T) {
	gf, spec := loadFixtures(t)
	out, err := runTree(t, Config{Guardfile: gf, Spec: spec}, "forgejo", "describe")
	if err != nil {
		t.Fatalf("run describe: %v", err)
	}
	for _, want := range []string{
		"## ward ops forgejo repo create", // heading carries the full command path
		"/user/repos",
		"Authorized by grant: can create repos",
		"/forgejo/api-token",
		"Destructive - mutates irreversibly.",
		"deletes the repo",
		"Positional arguments (", // path params, their own list
		"Options (",              // body flags, kept separate
	} {
		if !strings.Contains(out, want) {
			t.Errorf("describe output missing %q:\n%s", want, out)
		}
	}
}

// TestListOrgsExpands proves `list orgs` resolves to the non-destructive org list
// leaf (orgGetAll, GET /orgs), asserted at the table since the test spec has no orgs.
func TestListOrgsExpands(t *testing.T) {
	e, ok := lookupExpansion("list", "orgs")
	if !ok {
		t.Fatal("list orgs should be in the expansion allowlist")
	}
	if e.Group != "org" || e.Leaf != "list" || e.OperationID != "orgGetAll" {
		t.Errorf("list orgs = %+v, want org/list/orgGetAll", e)
	}
	if destructiveLeaves[e.Leaf] {
		t.Errorf("list leaf must not be destructive")
	}
}

// TestLeafDescriptionIsRich proves a mounted leaf carries structural help -
// method/path, the grant, kind-tagged params, dry-run hint - even with no spec desc.
func TestLeafDescriptionIsRich(t *testing.T) {
	gf, spec := loadFixtures(t)
	root, err := Build(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	del := childNamed(childNamed(root, "repo"), "delete")
	if del == nil {
		t.Fatal("no repo delete leaf")
	}
	for _, want := range []string{"DELETE", "/repos/{owner}/{repo}", "Authorized by: can delete repos", "<owner> (path", "--dry-run", "deletes the repo"} {
		if !strings.Contains(del.Description, want) {
			t.Errorf("leaf description missing %q:\n%s", want, del.Description)
		}
	}
}

func childNamed(parent *cli.Command, name string) *cli.Command {
	for _, c := range parent.Commands {
		if c.Name == name {
			return c
		}
	}
	return nil
}
