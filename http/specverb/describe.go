// The describe model: one in-engine view of the mounted surface, the shared
// source for rich per-verb help and the `describe` verb. See docs/specverb-describe.md.

package specverb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"github.com/urfave/cli/v3"
)

// Surface is the in-engine model of a mounted command surface: the structural
// truth shared by help, the describe verb, and (later) completions and the skill.
type Surface struct {
	Group   []string   `json:"group"`    // command path, e.g. ["ward","ops","forgejo"]
	BaseURL string     `json:"base_url"` // resolved request base, scheme defaulted
	Auth    AuthInfo   `json:"auth"`     // how the engine authenticates
	Verbs   []VerbInfo `json:"verbs"`    // every mounted leaf, in mount order
}

// AuthInfo is the auth scope a describe consumer sees: scheme, header, and the
// SSM token path. The secret value itself never appears here.
type AuthInfo struct {
	Scheme string `json:"scheme"`
	Header string `json:"header"`
	SSM    string `json:"ssm"` // token path, not the token
}

// VerbInfo is one mounted leaf: its CLI placement, the HTTP op it drives, its
// destructive flag, the grant that authorized it, and the optional human note.
type VerbInfo struct {
	Name        string         `json:"name"`                 // dotted audit name, e.g. ward.ops.forgejo.repo.create
	Group       string         `json:"group"`                // CLI noun, e.g. repo
	Leaf        string         `json:"leaf"`                 // CLI verb, e.g. create
	Method      string         `json:"method"`               // HTTP method
	Path        string         `json:"path"`                 // path template
	Destructive bool           `json:"destructive"`          // mutates irreversibly
	Grant       string         `json:"grant"`                // authorizing grant sentence
	Describe    string         `json:"describe,omitempty"`   // Guardfile describe "..." note
	Params      []ParamInfo    `json:"params,omitempty"`     // path/query/body params, in invocation order
	FixedBody   map[string]any `json:"fixed_body,omitempty"` // exact body a state-toggle leaf sends
}

// ParamInfo is one input to a verb, tagged by kind so help can always show the
// structure the engine knows even where the upstream spec carries no description.
type ParamInfo struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`           // "path" | "query" | "body" | "form"
	Type     string `json:"type"`           // swagger type; arrays render as []elem
	Required bool   `json:"required"`       // path params and required-schema fields
	Desc     string `json:"desc,omitempty"` // upstream spec description, often blank
}

// Describe builds the surface model for cfg without mounting a command tree, so a
// caller (skill, completions, docs) can read it directly. Fails closed like Build.
func Describe(cfg Config) (*Surface, error) {
	if cfg.Guardfile == nil {
		return nil, fmt.Errorf("specverb: Config.Guardfile is nil")
	}
	gf := cfg.Guardfile
	if len(gf.Group) == 0 {
		return nil, fmt.Errorf("specverb: Guardfile has no command group")
	}
	spec, err := parseSwagger(cfg.Spec)
	if err != nil {
		return nil, err
	}
	descs, err := resolveDescriptors(spec, gf)
	if err != nil {
		return nil, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = gf.BaseURL
	}
	return buildSurface(gf, defaultScheme(strings.TrimRight(baseURL, "/")), descs), nil
}

// buildSurface assembles the model from the already-resolved descriptors, so the
// description can never name a verb the runtime did not mount.
func buildSurface(gf *guardfile.Guardfile, baseURL string, descs []opDescriptor) *Surface {
	s := &Surface{
		Group:   gf.Group,
		BaseURL: baseURL,
		Auth:    AuthInfo{Scheme: gf.Auth.Scheme, Header: gf.Auth.Header, SSM: gf.Auth.SSM},
	}
	for _, d := range descs {
		s.Verbs = append(s.Verbs, VerbInfo{
			Name:        d.VerbName,
			Group:       d.Group,
			Leaf:        d.Leaf,
			Method:      d.Method,
			Path:        d.Path,
			Destructive: d.Destructive,
			Grant:       d.Grant,
			Describe:    d.Describe,
			Params:      paramsOf(d),
			FixedBody:   d.FixedBody,
		})
	}
	return s
}

// paramsOf flattens path params (positional, required), query flags, and body
// flags into one tagged list, path params first to match invocation order.
func paramsOf(d opDescriptor) []ParamInfo {
	var params []ParamInfo
	for _, p := range d.PathParams {
		params = append(params, ParamInfo{Name: p, Kind: "path", Type: "string", Required: true})
	}
	for _, f := range d.QueryFlags {
		params = append(params, ParamInfo{Name: f.Name, Kind: "query", Type: f.typeLabel(), Required: f.Required, Desc: f.Desc})
	}
	for _, f := range d.BodyFlags {
		params = append(params, ParamInfo{Name: f.Name, Kind: "body", Type: f.typeLabel(), Required: f.Required, Desc: f.Desc})
	}
	for _, f := range d.FormFlags {
		params = append(params, ParamInfo{Name: f.Name, Kind: "form", Type: f.typeLabel(), Required: f.Required, Desc: f.Desc})
	}
	return params
}

// buildDescribeLeaf mounts `describe` as a stdout-only reference verb; the build
// emits the committed doc beside the Guardfile. See docs/specverb-describe.md.
func (rt *runtime) buildDescribeLeaf(gf *guardfile.Guardfile, surface *Surface) *cli.Command {
	name := strings.Join(gf.Group, ".") + ".describe"
	return &cli.Command{
		Name:  "describe",
		Usage: "print the mounted surface as a readable reference",
		Action: rt.wrap(verb.Spec{
			Name: name,
			Action: func(_ context.Context, _ *cli.Command) error {
				fmt.Print(surface.Markdown())
				return nil
			},
		}),
	}
}

// Markdown renders the surface as the readable reference doc: the same artifact
// the describe verb prints and the driver writes beside the Guardfile at build time.
func (s *Surface) Markdown() string {
	return renderProse(s)
}

// renderProse renders the Surface as valid Markdown: header, auth sentence, a
// stanza per verb, each fact a blank-line-separated block. See docs/specverb-describe.md.
func renderProse(s *Surface) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.Join(s.Group, " "))
	fmt.Fprintf(&b, "Spec-driven CLI. Every verb issues an HTTP request against the API base %s.\n\n", s.BaseURL)
	fmt.Fprintf(&b, "%s\n", authSentence(s.Auth))

	prefix := strings.Join(s.Group, " ")
	for _, v := range s.Verbs {
		heading := fmt.Sprintf("## %s %s %s", prefix, v.Group, v.Leaf)
		if v.Describe != "" { // the Guardfile note adds intent the path can't; bare heading otherwise
			heading += " - " + v.Describe
		}
		fmt.Fprintf(&b, "\n%s\n\n", heading)
		fmt.Fprintf(&b, "`%s %s`\n\n", v.Method, v.Path)
		dest := "Not destructive."
		if v.Destructive {
			dest = "Destructive - mutates irreversibly."
		}
		fmt.Fprintf(&b, "Authorized by grant: %s. %s\n", v.Grant, dest)
		if line := fixedBodySentence(v.FixedBody); line != "" {
			fmt.Fprintf(&b, "\n%s\n", line)
		}
		writeParamSections(&b, v.Params)
	}
	return b.String()
}

// writeParamSections prints a verb's params as two blank-line-fronted Markdown
// lists, positional and options; a verb with neither says so, empties omitted.
func writeParamSections(b *strings.Builder, params []ParamInfo) {
	var positional, options []ParamInfo
	for _, p := range params {
		if p.Kind == "path" {
			positional = append(positional, p)
		} else {
			options = append(options, p)
		}
	}
	if len(positional) == 0 && len(options) == 0 {
		b.WriteString("\nTakes no arguments.\n")
		return
	}
	if len(positional) > 0 {
		fmt.Fprintf(b, "\nPositional arguments (%d):\n\n", len(positional))
		for _, line := range positionalLines(positional) {
			fmt.Fprintf(b, "- %s\n", line)
		}
	}
	if len(options) > 0 {
		fmt.Fprintf(b, "\nOptions (%d):\n\n", len(options))
		for _, line := range optionLines(options) {
			fmt.Fprintf(b, "- %s\n", line)
		}
	}
}

// fixedBodySentence states the exact body a state-toggle leaf sends, in sorted
// key order so the sentence is deterministic; "" for ordinary leaves.
func fixedBodySentence(fixed map[string]any) string {
	if len(fixed) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fixed))
	for k := range fixed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		v, _ := json.Marshal(fixed[k])
		parts[i] = fmt.Sprintf("%q: %s", k, v)
	}
	return fmt.Sprintf("Always sends the fixed body {%s}; takes no body flags.", strings.Join(parts, ", "))
}

// authSentence states how the engine authenticates in plain language, naming the
// SSM path but never the secret it holds.
func authSentence(a AuthInfo) string {
	if a.Scheme == "" {
		return "No authentication is configured."
	}
	return fmt.Sprintf("Authenticates with the %q header (scheme %s), reading the token from SSM at %s. The token value is never shown.", a.Header, a.Scheme, a.SSM)
}

// positionalLines renders each path param as a Markdown bullet body: code-spanned
// <name> then its type in parens. Always required by construction, left implicit.
func positionalLines(params []ParamInfo) []string {
	lines := make([]string, len(params))
	for i, p := range params {
		lines[i] = fmt.Sprintf("`<%s>` (%s)", p.Name, p.Type)
	}
	return lines
}

// optionLines renders each body flag as a Markdown bullet body: code-spanned --name,
// type and requiredness in parens, then any upstream description after a colon.
func optionLines(params []ParamInfo) []string {
	lines := make([]string, len(params))
	for i, p := range params {
		req := "optional"
		if p.Required {
			req = "required"
		}
		line := fmt.Sprintf("`--%s` (%s, %s)", p.Name, p.Type, req)
		if p.Desc != "" {
			line += ": " + p.Desc
		}
		lines[i] = line
	}
	return lines
}

// leafDescription is the rich per-verb help body, always populated even where the
// spec description is blank because the structure is what the engine always knows.
func leafDescription(desc opDescriptor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", desc.Method, desc.Path)
	if desc.Destructive {
		b.WriteString(" (destructive)")
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "Authorized by: %s\n", desc.Grant)
	if desc.Describe != "" {
		fmt.Fprintf(&b, "%s\n", desc.Describe)
	}

	params := paramsOf(desc)
	if len(params) > 0 {
		b.WriteString("\nParameters:\n")
		for _, line := range paramHelpLines(params) {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	if len(desc.BodyFlags) > 0 {
		b.WriteString("\n--body-file <path> supplies the full JSON body instead of the body flags.\n")
	}
	if line := fixedBodySentence(desc.FixedBody); line != "" {
		fmt.Fprintf(&b, "\n%s\n", line)
	}
	b.WriteString("\nUse --dry-run to print the resolved request without firing it.")
	return b.String()
}

// paramHelpLines renders each param as an aligned `name (kind, type) req desc`
// row. Path params show as <name>, body params as --name, mirroring invocation.
func paramHelpLines(params []ParamInfo) []string {
	labels := make([]string, len(params))
	width := 0
	for i, p := range params {
		display := "--" + p.Name
		if p.Kind == "path" {
			display = "<" + p.Name + ">"
		}
		labels[i] = fmt.Sprintf("%s (%s, %s)", display, p.Kind, p.Type)
		if len(labels[i]) > width {
			width = len(labels[i])
		}
	}
	lines := make([]string, len(params))
	for i, p := range params {
		req := "optional"
		if p.Required {
			req = "required"
		}
		line := fmt.Sprintf("%-*s  %s", width, labels[i], req)
		if p.Desc != "" {
			line += "  " + p.Desc
		}
		lines[i] = line
	}
	return lines
}
