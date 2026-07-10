package opcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/valuesource"
)

// Provider resolves the value at address for one named value source: the shared
// valuesource.Provider, so the consumer's one registry drives both engines.
type Provider = valuesource.Provider

// contentTypeJSON is the body content type for every non-multipart verb.
const contentTypeJSON = "application/json"

// defaultHTTPTimeout bounds a single live request made through the engine's
// default client (RuntimeConfig.Client nil).
const defaultHTTPTimeout = 30 * time.Second

// Runtime carries the per-tree request dependencies shared by every leaf,
// urfave/cli-free: specverb embeds it and a non-CLI consumer drives it directly.
type Runtime struct {
	BaseURL   string
	Auth      guardfile.Auth
	Providers map[string]Provider
	Client    *http.Client
	Restrict  []guardfile.Restriction

	// BaseURLValue (zero = static BaseURL) resolves the host through the first
	// available source in its chain once, caching it. See specverb-policy.md.
	BaseURLValue guardfile.ValueChain
	baseURLOnce  sync.Once
	baseURLVal   string
	baseURLErr   error
}

// RuntimeConfig is the input to NewRuntime: BaseURL is raw (scheme + trim
// applied), a nil Client defaults to the redirect-guarding client.
type RuntimeConfig struct {
	BaseURL      string
	Auth         guardfile.Auth
	Providers    map[string]Provider
	Client       *http.Client
	Restrict     []guardfile.Restriction
	BaseURLValue guardfile.ValueChain
}

// NewRuntime assembles a Runtime, defaulting the base-url scheme and the HTTP
// client; the wrap pipeline and step runner are CLI-layer concerns layered on top.
func NewRuntime(c RuntimeConfig) *Runtime {
	client := c.Client
	if client == nil {
		client = defaultHTTPClient()
	}
	return &Runtime{
		BaseURL:      DefaultScheme(strings.TrimRight(c.BaseURL, "/")),
		Auth:         c.Auth,
		Providers:    c.Providers,
		Client:       client,
		Restrict:     c.Restrict,
		BaseURLValue: c.BaseURLValue,
	}
}

// DefaultScheme prepends https:// to a base-url that carries no scheme, so a
// Guardfile may write `base-url "host/api/v1"` and rely on TLS by default.
func DefaultScheme(baseURL string) string {
	if baseURL == "" || strings.Contains(baseURL, "://") {
		return baseURL
	}
	return "https://" + baseURL
}

// defaultHTTPClient (used when RuntimeConfig.Client is nil) follows GET/HEAD
// redirects but refuses them for mutating methods. See docs/specverb.md.
func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultHTTPTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			switch via[0].Method {
			case http.MethodGet, http.MethodHead:
				return nil
			}
			return fmt.Errorf("opcore: refusing to follow a %s redirect to %s; a mutating verb must not be silently downgraded", via[0].Method, req.URL)
		},
	}
}

// BaseForRequest returns the request base-url: the static base, or (for a
// `base-url { value }` host) a once-resolved provider value. Dry-run stays offline.
func (rt *Runtime) BaseForRequest(ctx context.Context, dry bool) (string, error) {
	if rt.BaseURLValue.IsZero() {
		return rt.BaseURL, nil
	}
	if dry {
		return "{base-url:" + rt.BaseURLValue.String() + "}", nil
	}
	rt.baseURLOnce.Do(func() {
		v, err := rt.resolveChain(ctx, rt.BaseURLValue)
		if err != nil {
			rt.baseURLErr = err
			return
		}
		rt.baseURLVal = DefaultScheme(strings.TrimRight(v, "/"))
	})
	if rt.baseURLErr != nil {
		return "", rt.baseURLErr
	}
	return rt.baseURLVal, nil
}

// authorize resolves the scheme's secret(s) and applies them to req: a header
// for header-token/bearer, or query parameters for query-param.
func (rt *Runtime) authorize(ctx context.Context, req *http.Request) error {
	if rt.Auth.Scheme == "" {
		return nil
	}
	if rt.Auth.Scheme == "query-param" {
		q := req.URL.Query()
		for _, p := range rt.Auth.Params {
			secret, err := rt.resolveChain(ctx, p.Value)
			if err != nil {
				return err
			}
			q.Set(p.Name, secret)
		}
		req.URL.RawQuery = q.Encode()
		return nil
	}
	secret, err := rt.resolveChain(ctx, rt.Auth.Value)
	if err != nil {
		return err
	}
	req.Header.Set(rt.Auth.Header, rt.Auth.Prefix+secret)
	return nil
}

// Authorize applies the runtime's configured auth to req. Blank auth is a no-op.
func (rt *Runtime) Authorize(ctx context.Context, req *http.Request) error {
	return rt.authorize(ctx, req)
}

// resolveChain resolves a value chain to its first available source, wrapping an
// all-failed chain as a coded Internal error - never a value, never a silent empty.
func (rt *Runtime) resolveChain(ctx context.Context, chain guardfile.ValueChain) (string, error) {
	v, err := valuesource.ResolveFirst(ctx, rt.Providers, chainSources(chain))
	if err != nil {
		return "", exitcode.New(exitcode.Internal, "internal", err,
			"check the value provider address and credentials, or register the provider via specverb.Config.Providers")
	}
	return v, nil
}

// ChainSources lowers a guardfile.ValueChain to the []valuesource.Source the
// shared resolver walks, keeping guardfile free of the resolution layer.
func ChainSources(chain guardfile.ValueChain) []valuesource.Source {
	out := make([]valuesource.Source, len(chain))
	for i, vs := range chain {
		out[i] = valuesource.Source{Provider: vs.Provider, Address: vs.Address}
	}
	return out
}

func chainSources(chain guardfile.ValueChain) []valuesource.Source { return ChainSources(chain) }

// FireCapture sends one request and returns the decoded JSON value plus raw
// body, rendering nothing: the "fire and capture" path complex actions feed on.
func (rt *Runtime) FireCapture(ctx context.Context, method, url string, body []byte, contentType string) (decoded any, raw []byte, status string, err error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, rerr := http.NewRequestWithContext(ctx, method, url, reqBody)
	if rerr != nil {
		return nil, nil, "", exitcode.New(exitcode.Internal, "internal", rerr, "")
	}
	if aerr := rt.Authorize(ctx, req); aerr != nil {
		return nil, nil, "", aerr
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, derr := rt.Client.Do(req)
	if derr != nil {
		return nil, nil, "", exitcode.New(exitcode.UpstreamFailed, "upstream_failed", derr, "the API was unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, nil, resp.Status, exitcode.New(exitcode.UpstreamFailed, "upstream_failed",
			fmt.Errorf("%s %s -> %s: %s", method, url, resp.Status, strings.TrimSpace(string(respBody))),
			"the API rejected the request")
	}

	if len(bytes.TrimSpace(respBody)) == 0 {
		return nil, respBody, resp.Status, nil
	}
	if jerr := json.Unmarshal(respBody, &decoded); jerr != nil {
		return nil, respBody, resp.Status, exitcode.New(exitcode.Internal, "internal", jerr, "the response was not valid JSON")
	}
	return decoded, respBody, resp.Status, nil
}
