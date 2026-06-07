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

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/verb"
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
	if len(root.Commands) != 1 || root.Commands[0].Name != "repo" {
		t.Fatalf("want a single `repo` group, got %v", names(root.Commands))
	}
	leaves := names(root.Commands[0].Commands)
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
