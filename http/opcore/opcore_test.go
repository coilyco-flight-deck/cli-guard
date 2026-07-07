package opcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/opcore"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/valuesource"
)

// kindOf extracts the coded error's stable token, or "" for a nil/uncoded error.
func kindOf(err error) string {
	var c interface{ Kind() string }
	if errors.As(err, &c) {
		return c.Kind()
	}
	return ""
}

// tokenAuth is the header auth every fixture uses: `Authorization: token <lit>`.
func tokenAuth(secret string) guardfile.Auth {
	return guardfile.Auth{
		Scheme: "header-token",
		Header: "Authorization",
		Prefix: "token ",
		Value:  guardfile.ValueChain{{Provider: "literal", Address: secret}},
	}
}

// newTestOp wires a Runtime at srv against a create-shaped leaf: POST
// /repos/{owner}/{repo}/issues with a query flag and two body flags (one required).
func newTestOp(srv *httptest.Server, auth guardfile.Auth, restrict []guardfile.Restriction) opcore.Operation {
	rt := opcore.NewRuntime(opcore.RuntimeConfig{
		BaseURL:   srv.URL,
		Auth:      auth,
		Providers: valuesource.Merge(nil),
		Client:    srv.Client(),
		Restrict:  restrict,
	})
	return opcore.Operation{
		RT: rt,
		Desc: opcore.Descriptor{
			VerbName:   "test.repo.issues.create",
			Group:      "issues",
			Leaf:       "create",
			Method:     http.MethodPost,
			Path:       "/repos/{owner}/{repo}/issues",
			PathParams: []string{"owner", "repo"},
			QueryFlags: []opcore.Field{{Name: "state", Type: "string"}},
			BodyFlags: []opcore.Field{
				{Name: "title", Type: "string", Required: true},
				{Name: "count", Type: "integer"},
			},
		},
	}
}

func TestExecuteHappyPath(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"number":7}`))
	}))
	defer srv.Close()

	op := newTestOp(srv, tokenAuth("s3cret"), nil)
	resp, err := op.Execute(context.Background(), opcore.Args{
		Path:  map[string]string{"owner": "kai", "repo": "aos"},
		Query: map[string]string{"state": "open"},
		Body:  map[string]any{"title": "hello", "count": 3},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/repos/kai/aos/issues" {
		t.Errorf("method/path = %s %s", gotMethod, gotPath)
	}
	if gotQuery != "state=open" {
		t.Errorf("query = %q, want state=open", gotQuery)
	}
	if gotAuth != "token s3cret" {
		t.Errorf("auth header = %q, want %q", gotAuth, "token s3cret")
	}
	// Body typing is preserved: count is a JSON number, not a string.
	if gotBody["title"] != "hello" || gotBody["count"] != float64(3) {
		t.Errorf("body = %v", gotBody)
	}
	if m, ok := resp.Decoded.(map[string]any); !ok || m["number"] != float64(7) {
		t.Errorf("decoded = %v", resp.Decoded)
	}
}

func TestExecuteSelfGuardsShellMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("server must not be reached; the gate should have rejected first")
	}))
	defer srv.Close()
	op := newTestOp(srv, tokenAuth("s3cret"), nil)

	// A shell metacharacter in a URL-bound query value is rejected before firing,
	// with no verb.Wrap around the call - the whole point for a non-CLI consumer.
	_, err := op.Execute(context.Background(), opcore.Args{
		Path:  map[string]string{"owner": "kai", "repo": "aos"},
		Query: map[string]string{"state": "open;rm -rf"},
		Body:  map[string]any{"title": "x"},
	})
	if kindOf(err) != "policy_denied" {
		t.Fatalf("query metachar: kind = %q, want policy_denied (err=%v)", kindOf(err), err)
	}

	// A metachar in a positional path value is gated too.
	_, err = op.Execute(context.Background(), opcore.Args{
		Path:  map[string]string{"owner": "kai", "repo": "aos`whoami`"},
		Query: map[string]string{"state": "open"},
		Body:  map[string]any{"title": "x"},
	})
	if kindOf(err) != "policy_denied" {
		t.Fatalf("path metachar: kind = %q, want policy_denied", kindOf(err))
	}
}

func TestExecuteRestrictScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("out-of-scope call must not fire")
	}))
	defer srv.Close()
	op := newTestOp(srv, tokenAuth("s3cret"), []guardfile.Restriction{{Param: "owner", Globs: []string{"kai"}}})

	_, err := op.Execute(context.Background(), opcore.Args{
		Path:  map[string]string{"owner": "someone-else", "repo": "aos"},
		Query: map[string]string{},
		Body:  map[string]any{"title": "x"},
	})
	if kindOf(err) != "policy_denied" {
		t.Fatalf("restrict: kind = %q, want policy_denied", kindOf(err))
	}
}

func TestExecuteRequiredBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("call with a missing required field must not fire")
	}))
	defer srv.Close()
	op := newTestOp(srv, tokenAuth("s3cret"), nil)

	_, err := op.Execute(context.Background(), opcore.Args{
		Path:  map[string]string{"owner": "kai", "repo": "aos"},
		Query: map[string]string{},
		Body:  map[string]any{"count": 1}, // title (required) omitted
	})
	if kindOf(err) != "user_error" {
		t.Fatalf("required body: kind = %q, want user_error", kindOf(err))
	}
}

func TestExecuteMissingPathParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	op := newTestOp(srv, tokenAuth("s3cret"), nil)

	_, err := op.Execute(context.Background(), opcore.Args{
		Path:  map[string]string{"owner": "kai"}, // repo missing
		Query: map[string]string{},
		Body:  map[string]any{"title": "x"},
	})
	if kindOf(err) != "user_error" {
		t.Fatalf("missing path param: kind = %q, want user_error", kindOf(err))
	}
}

func TestPreviewResolvesWithoutFiring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("Preview must not fire a request")
	}))
	defer srv.Close()
	op := newTestOp(srv, tokenAuth("s3cret"), nil)

	req, err := op.Preview(opcore.Args{
		Path:  map[string]string{"owner": "kai", "repo": "aos"},
		Query: map[string]string{"state": "open"},
		Body:  map[string]any{"title": "hello"},
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %s", req.Method)
	}
	if req.URL != srv.URL+"/repos/kai/aos/issues?state=open" {
		t.Errorf("url = %q", req.URL)
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil || body["title"] != "hello" {
		t.Errorf("body = %s (%v)", req.Body, err)
	}
}

func TestPreviewValueBaseStaysOffline(t *testing.T) {
	// A value-resolved base-url must never touch its provider during a preview.
	rt := opcore.NewRuntime(opcore.RuntimeConfig{
		Auth:         tokenAuth("s"),
		Providers:    valuesource.Merge(nil),
		BaseURLValue: guardfile.ValueChain{{Provider: "literal", Address: "https://example.test"}},
	})
	op := opcore.Operation{RT: rt, Desc: opcore.Descriptor{
		Method: http.MethodGet, Path: "/x", Leaf: "get",
	}}
	req, err := op.Preview(opcore.Args{Path: map[string]string{}, Query: map[string]string{}})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if want := "{base-url:"; len(req.URL) < len(want) || req.URL[:len(want)] != want {
		t.Errorf("preview url = %q, want a {base-url:...} placeholder", req.URL)
	}
}

func TestExecuteFixedBodyWins(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	rt := opcore.NewRuntime(opcore.RuntimeConfig{
		BaseURL: srv.URL, Auth: tokenAuth("s"), Providers: valuesource.Merge(nil), Client: srv.Client(),
	})
	op := opcore.Operation{RT: rt, Desc: opcore.Descriptor{
		Method:     http.MethodPatch,
		Path:       "/repos/{owner}/{repo}",
		Leaf:       "archive",
		PathParams: []string{"owner", "repo"},
		FixedBody:  map[string]any{"archived": true},
	}}
	if _, err := op.Execute(context.Background(), opcore.Args{
		Path:  map[string]string{"owner": "kai", "repo": "aos"},
		Query: map[string]string{},
		Body:  map[string]any{"ignored": "value"}, // a fixed-body leaf owns its body
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(gotBody) != 1 || gotBody["archived"] != true {
		t.Errorf("fixed body = %v, want {archived:true}", gotBody)
	}
}

func TestExecuteUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	op := newTestOp(srv, tokenAuth("s"), nil)
	_, err := op.Execute(context.Background(), opcore.Args{
		Path: map[string]string{"owner": "kai", "repo": "aos"}, Query: map[string]string{},
		Body: map[string]any{"title": "x"},
	})
	if kindOf(err) != "upstream_failed" {
		t.Fatalf("403: kind = %q, want upstream_failed", kindOf(err))
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
		if got := opcore.DefaultScheme(in); got != want {
			t.Errorf("DefaultScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMethodForVerb(t *testing.T) {
	cases := []struct {
		verb   string
		method string
		known  bool
	}{
		{"get", "GET", true},
		{"list", "GET", true},
		{"create", "POST", true},
		{"edit", "PATCH", true},
		{"delete", "DELETE", true},
		{"set", "PUT", true},
		{"search", "GET", true},
		{"list-runs", "GET", true},
		{"create-on-repo", "POST", true},
		{"comment", "POST", false}, // unknown verb -> sub-collection create default
	}
	for _, c := range cases {
		m, ok := opcore.MethodForVerb(c.verb)
		if m != c.method || ok != c.known {
			t.Errorf("MethodForVerb(%q) = (%q,%v), want (%q,%v)", c.verb, m, ok, c.method, c.known)
		}
	}
}

func TestArgBinderRoutesByLocation(t *testing.T) {
	leaf := opcore.Descriptor{
		Path:       "/repos/{owner}/{repo}/issues",
		PathParams: []string{"owner", "repo"},
		QueryFlags: []opcore.Field{{Name: "state"}},
		BodyFlags:  []opcore.Field{{Name: "title"}},
	}
	b := opcore.NewArgBinder(leaf)
	if err := b.Bind(opcore.OwnerRepoArg, "kai/aos"); err != nil {
		t.Fatalf("bind owner-repo: %v", err)
	}
	if err := b.Bind("state", "open"); err != nil {
		t.Fatalf("bind state: %v", err)
	}
	if err := b.Bind("title", "hello"); err != nil {
		t.Fatalf("bind title: %v", err)
	}
	if err := b.RequireAllPaths(); err != nil {
		t.Fatalf("requireAllPaths: %v", err)
	}
	if opcore.FillPath(leaf.Path, b.PathVals) != "/repos/kai/aos/issues" {
		t.Errorf("path = %q", opcore.FillPath(leaf.Path, b.PathVals))
	}
	if b.Query.Get("state") != "open" {
		t.Errorf("query state = %q", b.Query.Get("state"))
	}
	if b.BodyObj["title"] != "hello" {
		t.Errorf("body title = %v", b.BodyObj["title"])
	}

	// A malformed owner-repo value fails closed.
	if err := opcore.NewArgBinder(leaf).Bind(opcore.OwnerRepoArg, "no-slash"); err == nil {
		t.Errorf("owner-repo without a slash should error")
	}
}
