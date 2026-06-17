package specverb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
)

// restrictFixture grants repo get/delete under a `restrict owner matches "coily*"`
// scope gate, over the shared Swagger 2.0 proving spec.
func restrictFixture(t *testing.T) (*guardfile.Guardfile, []byte) {
	t.Helper()
	_, spec := loadFixtures(t)
	gf, err := guardfile.Parse([]byte(`wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		auth header-token { header Authorization; prefix "token "; ssm "/forgejo/api-token" }
		restrict owner matches "coily*" "coilyco-*"
		can get repo { op "repoGet" }
		can delete repo { op "repoDelete" }
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return gf, spec
}

// TestRestrictAllowsInScope proves an owner matching the glob passes the gate and
// fires the request.
func TestRestrictAllowsInScope(t *testing.T) {
	gf, spec := restrictFixture(t)
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cfg := Config{Guardfile: gf, Spec: spec, BaseURL: srv.URL,
		Token: func(context.Context, string) (string, error) { return "x", nil }}
	if _, err := runTree(t, cfg, "forgejo", "repo", "get", "coilyco-flight-deck", "demo", "--output", "json"); err != nil {
		t.Fatalf("in-scope run: %v", err)
	}
	if gotPath != "/repos/coilyco-flight-deck/demo" {
		t.Errorf("server saw %q", gotPath)
	}
}

// TestRestrictFailsClosedOutOfScope proves an owner outside the glob set is a
// PolicyDenied exit before any wire call.
func TestRestrictFailsClosedOutOfScope(t *testing.T) {
	gf, spec := restrictFixture(t)
	cfg := Config{Guardfile: gf, Spec: spec, HTTPClient: &http.Client{Transport: failingTransport{t}},
		Token: func(context.Context, string) (string, error) {
			t.Fatal("an out-of-scope arg must not reach the wire")
			return "", nil
		}}
	_, err := runTree(t, cfg, "forgejo", "repo", "get", "someone-else", "demo")
	if err == nil {
		t.Fatal("expected an out-of-scope deny, got nil")
	}
	if coded := exitcode.From(err); coded == nil || coded.Code() != exitcode.PolicyDenied {
		t.Errorf("error = %v, want a coded PolicyDenied exit", err)
	}
	if !strings.Contains(err.Error(), "outside the allowed scope") {
		t.Errorf("deny error should explain the scope gate: %v", err)
	}
}

// TestRestrictDescribed proves the describe surface names the scope restriction.
func TestRestrictDescribed(t *testing.T) {
	gf, spec := restrictFixture(t)
	surface, err := Describe(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(surface.Restrict) != 1 || surface.Restrict[0].Param != "owner" {
		t.Fatalf("Restrict = %+v", surface.Restrict)
	}
	md := surface.Markdown()
	for _, want := range []string{"Scope restrictions", "`owner` must match", "coily*"} {
		if !strings.Contains(md, want) {
			t.Errorf("describe prose missing %q:\n%s", want, md)
		}
	}
}
