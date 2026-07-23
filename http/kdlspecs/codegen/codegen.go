// Package codegen renders a complete consumer `main.go` from a KDL Guardfile,
// so a consumer never hand-writes the specverb wiring. See docs/specverb.md.
package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"net/url"
	"strings"
	"text/template"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
)

// Transport is the dialect one merged member speaks: spec (HTTP/specverb) or
// exec (wrapped binary/execverb). See docs/specgen.md.
const (
	TransportSpec = "spec"
	TransportExec = "exec"
)

// Params is the per-consumer data the template binds to, derived from the
// Guardfile. The spec-lock fields are empty for an exec member. See specverb.md.
type Params struct {
	Transport     string // TransportSpec | TransportExec
	Binary        string // binary name, e.g. ward-kdl (Guardfile group[0])
	GuardfileName string // embedded Guardfile filename, e.g. forgejo.guardfile.kdl
	SpecLockName  string // committed gzip lock filename, e.g. forgejo.swagger.lock.json.gz
	SpecURL       string // upstream Swagger URL; the `specgen lock` source and bootstrap fallback
	SpecEnvVar    string // env var overriding the embedded lock, e.g. WARD_KDL_SPEC

	// Providers are the value-source provider names this member's guardfile uses,
	// so the codegen wires exactly the resolvers in play. Empty for an exec member.
	Providers []string
}

// Plan derives the per-consumer Params from gf without rendering, the shared
// derivation behind both Render and the driver. guardfileName feeds the embed.
func Plan(gf *guardfile.Guardfile, guardfileName string) (Params, error) {
	if gf == nil || len(gf.Group) == 0 {
		return Params{}, fmt.Errorf("codegen: Guardfile has no command group")
	}
	// A base-url-from-value member has no committed host to derive a fetch URL from.
	// Its vendored spec is read locally at lock, so an empty SpecURL is correct.
	var specURL string
	switch {
	case gf.BaseURL != "":
		var err error
		if specURL, err = deriveSpecURL(gf.BaseURL); err != nil {
			return Params{}, err
		}
	case !gf.BaseURLValue.IsZero():
		specURL = ""
	default:
		return Params{}, fmt.Errorf("codegen: guardfile %q needs a `base-url` or `base-url { value ... }`", guardfileName)
	}
	binary := gf.Group[0]
	return Params{
		Transport:     TransportSpec,
		Binary:        binary,
		GuardfileName: guardfileName,
		SpecLockName:  deriveLockName(gf.Spec),
		SpecURL:       specURL,
		// Keyed on the full wrap group, not the binary, so two specs merged
		// into one binary get distinct overrides (see docs/specgen.md).
		SpecEnvVar: strings.ToUpper(strings.ReplaceAll(strings.Join(gf.Group, "_"), "-", "_")) + "_SPEC",
		Providers:  gf.Providers(),
	}, nil
}

// PlanExec derives an exec member's Params from its wrap group and the provider
// names its env injections use; no upstream spec. See specgen.md.
func PlanExec(group, providers []string, guardfileName string) (Params, error) {
	if len(group) == 0 {
		return Params{}, fmt.Errorf("codegen: exec Guardfile has no command group")
	}
	return Params{
		Transport:     TransportExec,
		Binary:        group[0],
		GuardfileName: guardfileName,
		Providers:     providers,
	}, nil
}

// SetParams binds the merged-binary template: one shared Binary and N mounts.
// HasSpec/HasExec gate the per-transport imports the template emits.
type SetParams struct {
	Binary  string
	Mounts  []Params
	HasSpec bool
	HasExec bool
	// HasSSM / HasTailscale gate the consumer-side resolver blocks (and the AWS SDK
	// import) the template emits, derived from the members' Providers by RenderParams.
	HasSSM       bool
	HasTailscale bool
}

// PlanSet derives the merged params for guardfiles that share a binary name. It
// fails closed on an empty set or a Group[0] disagreement (one binary per merge).
func PlanSet(gfs []*guardfile.Guardfile, names []string) (SetParams, error) {
	if len(gfs) != len(names) {
		return SetParams{}, fmt.Errorf("codegen: %d guardfiles but %d names", len(gfs), len(names))
	}
	if len(gfs) == 0 {
		return SetParams{}, fmt.Errorf("codegen: no guardfiles to plan")
	}
	var sp SetParams
	for i, gf := range gfs {
		p, err := Plan(gf, names[i])
		if err != nil {
			return SetParams{}, err
		}
		switch {
		case sp.Binary == "":
			sp.Binary = p.Binary
		case sp.Binary != p.Binary:
			return SetParams{}, fmt.Errorf("codegen: guardfiles disagree on binary name (%q vs %q); a merged build needs one wrap binary", sp.Binary, p.Binary)
		}
		if p.Transport == TransportExec {
			sp.HasExec = true
		} else {
			sp.HasSpec = true
		}
		sp.Mounts = append(sp.Mounts, p)
	}
	return sp, nil
}

// Render emits a gofmt'd `package main` wiring specverb.Mount for gf, the
// single-guardfile convenience over RenderSet.
func Render(gf *guardfile.Guardfile, guardfileName string) ([]byte, error) {
	return RenderSet([]*guardfile.Guardfile{gf}, []string{guardfileName})
}

// RenderSet emits a gofmt'd `package main` mounting every spec guardfile onto
// one binary. names are the embed filenames, parallel to gfs. Spec-only.
func RenderSet(gfs []*guardfile.Guardfile, names []string) ([]byte, error) {
	sp, err := PlanSet(gfs, names)
	if err != nil {
		return nil, err
	}
	return RenderParams(sp)
}

// RenderParams emits a gofmt'd `package main` from pre-planned SetParams,
// mixing spec and exec members onto the one shared binary. The driver's entry.
func RenderParams(sp SetParams) ([]byte, error) {
	if len(sp.Mounts) == 0 {
		return nil, fmt.Errorf("codegen: no mounts to render")
	}
	// Derive which consumer-side resolvers to wire from the members' providers, so
	// a hand-assembled SetParams (the driver) and a planned one agree.
	for _, m := range sp.Mounts {
		for _, prov := range m.Providers {
			switch prov {
			case "ssm":
				sp.HasSSM = true
			case "tailscale":
				sp.HasTailscale = true
			}
		}
	}
	var buf bytes.Buffer
	if err := mainTemplate.Execute(&buf, sp); err != nil {
		return nil, fmt.Errorf("codegen: execute template: %w", err)
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("codegen: gofmt generated source: %w", err)
	}
	return out, nil
}

// deriveLockName maps the Guardfile spec filename to the committed gzip lock
// name. The encoded payload expands to pruned JSON. See specgen.md.
func deriveLockName(spec string) string {
	if strings.HasSuffix(spec, ".v1.json") {
		return strings.TrimSuffix(spec, ".v1.json") + ".lock.json.gz"
	}
	for _, sfx := range []string{".openapi.json", ".openapi.yaml", ".openapi.yml"} {
		if strings.HasSuffix(spec, sfx) {
			return strings.TrimSuffix(spec, sfx) + ".openapi.lock.json.gz"
		}
	}
	return spec + ".lock.gz"
}

// deriveSpecURL turns the Guardfile base-url into the Swagger fetch URL: the
// host root's /swagger.v1.json (Forgejo's convention), scheme defaulted to https.
func deriveSpecURL(baseURL string) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("codegen: Guardfile has no base-url to derive the spec URL from")
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("codegen: parse base-url %q: %w", baseURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("codegen: base-url %q has no host", baseURL)
	}
	return u.Scheme + "://" + u.Host + "/swagger.v1.json", nil
}

// mainTemplate is the consumer main.go, materialized out-of-band into the cache.
// It branches per transport; the spec-only and execverb imports are gated.
var mainTemplate = template.Must(template.New("main").Parse(`// Code generated by specgen; DO NOT EDIT.
// Merged from each consumer guardfile. Regenerate with 'specgen gen' (or 'run').
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
{{if .HasTailscale}}	"os/exec"
	"strings"
{{end}}{{if .HasSSM}}	"errors"
{{end}}{{if .HasSpec}}	"io"
	"net/http"
	"time"
{{end}}
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/config"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/valuesource"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
{{if .HasSpec}}	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specverb"
{{end}}{{if .HasExec}}	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/execverb"
{{end}}	"github.com/urfave/cli/v3"
{{if .HasSSM}}	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
{{end}})

// Each member embeds its policy; a spec member also embeds its committed spec
// lock. Refresh both with 'specgen lock'.
{{range $i, $m := .Mounts}}
//go:embed {{$m.GuardfileName}}
var embeddedGuardfile{{$i}} []byte
{{if eq $m.Transport "spec"}}
//go:embed {{$m.SpecLockName}}
var embeddedSpec{{$i}} []byte
{{end}}{{end}}

// Version is stamped at build via -ldflags "-X main.Version="; "dev" when
// unstamped. Non-empty makes urfave/cli auto-register --version. See driver doc.
var Version = "dev"

func main() {
	app := &cli.Command{Name: "{{.Binary}}", Usage: "guarded verbs generated by specgen", Version: Version}
	if err := mountOps(app); err != nil {
		fmt.Fprintln(os.Stderr, "{{.Binary}}:", err)
		os.Exit(1)
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "{{.Binary}}:", err)
		os.Exit(1)
	}
}

// mountOps mounts every merged guardfile onto app under its shared command
// path, dispatching on transport. The audit writer is built once and reused.
func mountOps(app *cli.Command) error {
	wrap := wrapWith(auditWriter())
	provs := providerRegistry()
{{range $i, $m := .Mounts}}{{if eq $m.Transport "spec"}}	if err := mountSpec(app, wrap, provs, embeddedGuardfile{{$i}}, embeddedSpec{{$i}}, "{{$m.SpecURL}}", "{{$m.SpecEnvVar}}"); err != nil {
		return err
	}
{{else}}	if err := mountExec(app, wrap, provs, embeddedGuardfile{{$i}}); err != nil {
		return err
	}
{{end}}{{end}}	return nil
}

// providerRegistry wires the store-backed resolvers in use; cli-guard merges its
// no-SDK built-ins (env, file, literal) underneath. Shared by both transports.
func providerRegistry() map[string]valuesource.Provider {
	return map[string]valuesource.Provider{
{{if .HasSSM}}		"ssm": ssmTokenResolver,
{{end}}{{if .HasTailscale}}		"tailscale": tailscaleResolver,
{{end}}	}
}
{{if .HasSpec}}
// mountSpec parses one spec member's policy, resolves its spec, and mounts the
// specverb tree onto app. The value providers resolve lazily at request time.
func mountSpec(app *cli.Command, wrap func(verb.Spec) cli.ActionFunc, provs map[string]valuesource.Provider, gfBytes, specLock []byte, specURL, specEnv string) error {
	gf, err := guardfile.Parse(gfBytes)
	if err != nil {
		return fmt.Errorf("parse guardfile: %w", err)
	}
	spec, err := resolveSpec(specLock, specURL, specEnv)
	if err != nil {
		return fmt.Errorf("resolve spec: %w", err)
	}
	return specverb.Mount(app, specverb.Config{
		Guardfile: gf,
		Spec:      spec,
		Wrap:      wrap,
		Providers: provs,
	})
}

// resolveSpec prefers the override, then the embedded lock, then a live-fetch
// bootstrap used only before the first 'specgen lock'.
func resolveSpec(specLock []byte, specURL, specEnv string) ([]byte, error) {
	if path := os.Getenv(specEnv); path != "" {
		return os.ReadFile(path) //nolint:gosec // operator-supplied dev/skew override
	}
	if len(specLock) > 0 {
		return specLock, nil
	}
	fmt.Fprintf(os.Stderr, "{{.Binary}}: no embedded spec lock; fetching %s (run 'specgen lock')\n", specURL)
	return fetchSpec(specURL)
}

func fetchSpec(u string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> %s", u, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
{{end}}{{if .HasExec}}
// mountExec parses one exec member's policy and mounts the execverb tree onto
// app; its env injections resolve through the shared provider registry.
func mountExec(app *cli.Command, wrap func(verb.Spec) cli.ActionFunc, provs map[string]valuesource.Provider, gfBytes []byte) error {
	gf, err := execverb.Parse(gfBytes)
	if err != nil {
		return fmt.Errorf("parse exec guardfile: %w", err)
	}
	return execverb.Mount(app, execverb.Config{Guardfile: gf, Wrap: wrap, Providers: provs})
}
{{end}}
func auditWriter() *audit.Writer {
	path, err := config.DefaultAuditPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "{{.Binary}}: fatal: resolve audit path: %v\n", err)
		os.Exit(2)
	}
	w := audit.NewWriter(path)
	if err := w.Preflight(); err != nil {
		fmt.Fprintf(os.Stderr, "{{.Binary}}: fatal: %v\n", err)
		os.Exit(2)
	}
	return w
}

func wrapWith(w *audit.Writer) func(verb.Spec) cli.ActionFunc {
	return func(s verb.Spec) cli.ActionFunc { return verb.Wrap(s, w) }
}
{{if .HasTailscale}}
// tailscaleResolver resolves a tailnet device name to its IPv4 via the local
// tailscaled, so a base-url or host need not be committed. Read-only.
func tailscaleResolver(ctx context.Context, device string) (string, error) {
	out, err := exec.CommandContext(ctx, "tailscale", "ip", "-4", device).Output()
	if err != nil {
		return "", fmt.Errorf("tailscale ip -4 %s: %w", device, err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("tailscale returned no address for %q", device)
	}
	return ip, nil
}
{{end}}{{if .HasSSM}}
func ssmTokenResolver(ctx context.Context, ssmPath string) (string, error) {
	val, err := getSSMParam(ctx, ssmPath)
	if err == nil {
		return val, nil
	}
	// Stale static keys in ~/.aws/credentials shadow a same-name SSO profile, so
	// retry without the credentials file. See docs/codegen-ssm-resolver.md.
	if ssoVal, ssoErr := getSSMParam(ctx, ssmPath, awsconfig.WithSharedCredentialsFiles([]string{})); ssoErr == nil {
		fmt.Fprintln(os.Stderr, "{{.Binary}}: note: ignored ~/.aws/credentials (it was shadowing an SSO profile); remove the stale static keys to silence this")
		return ssoVal, nil
	}
	return "", err
}

func getSSMParam(ctx context.Context, ssmPath string, opts ...func(*awsconfig.LoadOptions) error) (string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return "", fmt.Errorf("load aws config: %w", err)
	}
	out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &ssmPath,
		WithDecryption: boolPtr(true),
	})
	if err != nil {
		return "", fmt.Errorf("ssm get-parameter %s: %w", ssmPath, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", errors.New("ssm get-parameter returned no value")
	}
	return *out.Parameter.Value, nil
}

func boolPtr(b bool) *bool { return &b }
{{end}}`))
