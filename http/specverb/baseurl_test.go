package specverb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
)

// baseURLSSMFixture parses a bearer guardfile whose host comes from SSM (no
// committed base-url), over the tailscale 3.1 spec.
func baseURLSSMFixture(t *testing.T) (*guardfile.Guardfile, []byte) {
	t.Helper()
	spec := readSpec(t, "tailscale.openapi.yaml")
	gf, err := guardfile.Parse([]byte(`wrap ward ops tailscale {
		spec tailscale.openapi.yaml
		base-url { ssm "/coilysiren/open-webui/url" }
		auth bearer { ssm "/tailscale/api-key" }
		can list devices { op "listTailnetDevices" }
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if gf.BaseURL != "" || gf.BaseURLSSM != "/coilysiren/open-webui/url" {
		t.Fatalf("base-url parse: BaseURL=%q BaseURLSSM=%q", gf.BaseURL, gf.BaseURLSSM)
	}
	return gf, spec
}

// TestBaseURLFromSSMLive proves a real request resolves the host from BaseURLFn
// (the SSM seam) and that the resolver is consulted exactly once.
func TestBaseURLFromSSMLive(t *testing.T) {
	gf, spec := baseURLSSMFixture(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"devices":[]}`))
	}))
	defer srv.Close()

	var resolved int
	cfg := Config{Guardfile: gf, Spec: spec,
		Token:     func(context.Context, string) (string, error) { return "tskey-abc", nil },
		BaseURLFn: func(context.Context) (string, error) { resolved++; return srv.URL, nil },
	}
	if _, err := runTree(t, cfg, "tailscale", "devices", "list", "my-tailnet", "--output", "json"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1 (base-url did not resolve to the live host)", hits)
	}
	if resolved != 1 {
		t.Errorf("BaseURLFn calls = %d, want 1 (lazy, cached once)", resolved)
	}
}

// TestBaseURLFromSSMDryRunOffline proves --dry-run never resolves the host from
// SSM (offline) and shows the SSM path symbolically.
func TestBaseURLFromSSMDryRunOffline(t *testing.T) {
	gf, spec := baseURLSSMFixture(t)
	cfg := Config{Guardfile: gf, Spec: spec,
		HTTPClient: &http.Client{Transport: failingTransport{t}},
		Token: func(context.Context, string) (string, error) {
			t.Fatal("dry-run must not resolve an auth secret")
			return "", nil
		},
		BaseURLFn: func(context.Context) (string, error) {
			t.Fatal("dry-run must not resolve the base-url from SSM")
			return "", nil
		},
	}
	out, err := runTree(t, cfg, "tailscale", "devices", "list", "my-tailnet", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(out, "base-url:ssm /coilysiren/open-webui/url") {
		t.Errorf("dry-run preview missing the symbolic SSM base marker:\n%s", out)
	}
}

// TestBaseURLSSMDescribeNamesPath proves the describe surface names the SSM path
// for the host rather than resolving it.
func TestBaseURLSSMDescribeNamesPath(t *testing.T) {
	gf, spec := baseURLSSMFixture(t)
	surface, err := Describe(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(surface.BaseURL, "/coilysiren/open-webui/url") {
		t.Errorf("describe base-url = %q, want the SSM path named", surface.BaseURL)
	}
}
