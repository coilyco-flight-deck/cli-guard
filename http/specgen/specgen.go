// Package specgen renders a complete consumer `main.go` from a KDL Guardfile,
// so a consumer never hand-writes the specverb wiring. See docs/specverb.md.
package specgen

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
// exec (wrapped binary/execverb). See docs/specverb-driver.md.
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
	SpecLockName  string // committed, embedded spec lock filename, e.g. forgejo.swagger.lock.json
	SpecURL       string // upstream Swagger URL; the `specverb-gen lock` source and bootstrap fallback
	SpecEnvVar    string // env var overriding the embedded lock, e.g. WARD_KDL_SPEC
}

// Plan derives the per-consumer Params from gf without rendering, the shared
// derivation behind both Render and the driver. guardfileName feeds the embed.
func Plan(gf *guardfile.Guardfile, guardfileName string) (Params, error) {
	if gf == nil || len(gf.Group) == 0 {
		return Params{}, fmt.Errorf("specgen: Guardfile has no command group")
	}
	specURL, err := deriveSpecURL(gf.BaseURL)
	if err != nil {
		return Params{}, err
	}
	binary := gf.Group[0]
	return Params{
		Transport:     TransportSpec,
		Binary:        binary,
		GuardfileName: guardfileName,
		SpecLockName:  deriveLockName(gf.Spec),
		SpecURL:       specURL,
		// Keyed on the full wrap group, not the binary, so two specs merged
		// into one binary get distinct overrides (see docs/specverb-driver.md).
		SpecEnvVar: strings.ToUpper(strings.ReplaceAll(strings.Join(gf.Group, "_"), "-", "_")) + "_SPEC",
	}, nil
}

// PlanExec derives the Params for an exec-dialect member from its wrap group;
// it has no upstream spec, so the spec-lock fields stay empty. See specverb-driver.md.
func PlanExec(group []string, guardfileName string) (Params, error) {
	if len(group) == 0 {
		return Params{}, fmt.Errorf("specgen: exec Guardfile has no command group")
	}
	return Params{
		Transport:     TransportExec,
		Binary:        group[0],
		GuardfileName: guardfileName,
	}, nil
}

// SetParams binds the merged-binary template: one shared Binary and N mounts.
// HasSpec/HasExec gate the per-transport imports the template emits.
type SetParams struct {
	Binary  string
	Mounts  []Params
	HasSpec bool
	HasExec bool
}

// PlanSet derives the merged params for guardfiles that share a binary name. It
// fails closed on an empty set or a Group[0] disagreement (one binary per merge).
func PlanSet(gfs []*guardfile.Guardfile, names []string) (SetParams, error) {
	if len(gfs) != len(names) {
		return SetParams{}, fmt.Errorf("specgen: %d guardfiles but %d names", len(gfs), len(names))
	}
	if len(gfs) == 0 {
		return SetParams{}, fmt.Errorf("specgen: no guardfiles to plan")
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
			return SetParams{}, fmt.Errorf("specgen: guardfiles disagree on binary name (%q vs %q); a merged build needs one wrap binary", sp.Binary, p.Binary)
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
		return nil, fmt.Errorf("specgen: no mounts to render")
	}
	var buf bytes.Buffer
	if err := mainTemplate.Execute(&buf, sp); err != nil {
		return nil, fmt.Errorf("specgen: execute template: %w", err)
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("specgen: gofmt generated source: %w", err)
	}
	return out, nil
}

// deriveLockName maps the Guardfile spec filename to the committed lock name (a
// distinct tracked name; the pruned lock is always JSON). See specverb-driver.md.
func deriveLockName(spec string) string {
	if strings.HasSuffix(spec, ".v1.json") {
		return strings.TrimSuffix(spec, ".v1.json") + ".lock.json"
	}
	for _, sfx := range []string{".openapi.json", ".openapi.yaml", ".openapi.yml"} {
		if strings.HasSuffix(spec, sfx) {
			return strings.TrimSuffix(spec, sfx) + ".openapi.lock.json"
		}
	}
	return spec + ".lock"
}

// deriveSpecURL turns the Guardfile base-url into the Swagger fetch URL: the
// host root's /swagger.v1.json (Forgejo's convention), scheme defaulted to https.
func deriveSpecURL(baseURL string) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("specgen: Guardfile has no base-url to derive the spec URL from")
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("specgen: parse base-url %q: %w", baseURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("specgen: base-url %q has no host", baseURL)
	}
	return u.Scheme + "://" + u.Host + "/swagger.v1.json", nil
}

// mainTemplate is the consumer main.go, materialized out-of-band into the cache.
// It branches per transport; the spec-only and execverb imports are gated.
var mainTemplate = template.Must(template.New("main").Parse(`// Code generated by specverb-gen; DO NOT EDIT.
// Merged from each consumer guardfile. Regenerate with 'specverb-gen gen' (or 'run').
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
{{if .HasSpec}}	"errors"
	"io"
	"net/http"
	"time"
{{end}}
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/config"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
{{if .HasSpec}}	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specverb"
{{end}}{{if .HasExec}}	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/execverb"
{{end}}	"github.com/urfave/cli/v3"
{{if .HasSpec}}	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
{{end}})

// Each member embeds its policy; a spec member also embeds its committed spec
// lock. Refresh both with 'specverb-gen lock'.
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
	app := &cli.Command{Name: "{{.Binary}}", Usage: "guarded verbs generated by specverb-gen", Version: Version}
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
{{range $i, $m := .Mounts}}{{if eq $m.Transport "spec"}}	if err := mountSpec(app, wrap, embeddedGuardfile{{$i}}, embeddedSpec{{$i}}, "{{$m.SpecURL}}", "{{$m.SpecEnvVar}}"); err != nil {
		return err
	}
{{else}}	if err := mountExec(app, wrap, embeddedGuardfile{{$i}}); err != nil {
		return err
	}
{{end}}{{end}}	return nil
}
{{if .HasSpec}}
// mountSpec parses one spec member's policy, resolves its spec, and mounts the
// specverb command tree onto app.
func mountSpec(app *cli.Command, wrap func(verb.Spec) cli.ActionFunc, gfBytes, specLock []byte, specURL, specEnv string) error {
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
		Token:     ssmTokenResolver,
	})
}

// resolveSpec prefers the override, then the embedded lock, then a live-fetch
// bootstrap used only before the first 'specverb-gen lock'.
func resolveSpec(specLock []byte, specURL, specEnv string) ([]byte, error) {
	if path := os.Getenv(specEnv); path != "" {
		return os.ReadFile(path) //nolint:gosec // operator-supplied dev/skew override
	}
	if len(specLock) > 0 {
		return specLock, nil
	}
	fmt.Fprintf(os.Stderr, "{{.Binary}}: no embedded spec lock; fetching %s (run 'specverb-gen lock')\n", specURL)
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
// mountExec parses one exec member's policy and mounts the execverb command
// tree onto app. An exec member has no spec lock and no auth token.
func mountExec(app *cli.Command, wrap func(verb.Spec) cli.ActionFunc, gfBytes []byte) error {
	gf, err := execverb.Parse(gfBytes)
	if err != nil {
		return fmt.Errorf("parse exec guardfile: %w", err)
	}
	return execverb.Mount(app, execverb.Config{Guardfile: gf, Wrap: wrap})
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
{{if .HasSpec}}
func ssmTokenResolver(ctx context.Context, ssmPath string) (string, error) {
	val, err := getSSMParam(ctx, ssmPath)
	if err == nil {
		return val, nil
	}
	// Stale static keys in ~/.aws/credentials shadow a same-name SSO profile, so
	// retry without the credentials file. See docs/specgen-ssm-resolver.md.
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
