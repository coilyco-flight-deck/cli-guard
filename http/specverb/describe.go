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
	Group    []string          `json:"group"`              // command path, e.g. ["ward","ops","forgejo"]
	BaseURL  string            `json:"base_url"`           // resolved request base, scheme defaulted
	Auth     AuthInfo          `json:"auth"`               // how the engine authenticates
	Verbs    []VerbInfo        `json:"verbs"`              // every mounted leaf, in mount order
	Actions  []ActionInfo      `json:"actions,omitempty"`  // complex actions, in declaration order
	Denied   []DenyInfo        `json:"denied,omitempty"`   // blocked classes, in declaration order
	Restrict []RestrictionInfo `json:"restrict,omitempty"` // wrap-level scope allowlists
}

// RestrictionInfo is one wrap-level restrict clause for the describe surface: the
// path param it gates and the globs an argument must match.
type RestrictionInfo struct {
	Param string   `json:"param"`
	Globs []string `json:"globs"`
}

// DenyInfo is one blocked (verb,resource) class for the describe surface: its CLI
// placement and the teaching message an operator sees when they reach for it.
type DenyInfo struct {
	Name    string `json:"name"`    // dotted audit name, e.g. ward.ops.forgejo.orgs.create
	Group   string `json:"group"`   // CLI noun, e.g. orgs
	Leaf    string `json:"leaf"`    // CLI verb, e.g. create
	Message string `json:"message"` // the teaching error
}

// ActionInfo is one mounted complex action for the describe surface: its
// envelope name, the polled leaf, the bounds, and the conditions.
type ActionInfo struct {
	Name     string `json:"name"`                // envelope audit name, e.g. ward.ops.forgejo.action.ci-watch
	Leaf     string `json:"leaf"`                // CLI leaf, e.g. ci-watch
	Describe string `json:"describe,omitempty"`  // optional human note
	Method   string `json:"method"`              // the polled leaf's HTTP method
	Path     string `json:"path"`                // the polled leaf's path template
	Grant    string `json:"grant"`               // the grant that authorizes the polled leaf
	Every    string `json:"every"`               // sample interval
	Timeout  string `json:"timeout"`             // wall-clock bound
	Until    string `json:"until"`               // loop-ending JMESPath
	FailWhen string `json:"fail_when,omitempty"` // non-zero-exit JMESPath

	// Defaults is the pre-flight input bindings: each absent input resolved from
	// the polled leaf before the loop starts. Empty when the action declares none.
	Defaults []ActionDefaultInfo `json:"defaults,omitempty"`

	// MountVerb/MountResource: set when the action shadows a leaf path (mounts at
	// `<resource> <verb>`, not the `action` noun). Empty for a named action.
	MountVerb     string `json:"mount_verb,omitempty"`
	MountResource string `json:"mount_resource,omitempty"`

	// Calls is the resolved step sequence for a multi-call action; empty for a poll.
	Calls []ActionCallInfo `json:"calls,omitempty"`

	// Collect describes an auto-pagination action; nil for poll/call actions.
	Collect *ActionCollectInfo `json:"collect,omitempty"`
}

// ActionDefaultInfo is one input `default` pre-flight binding for the describe
// surface: the input it resolves and the JMESPath evaluated against the poll leaf.
type ActionDefaultInfo struct {
	Input    string `json:"input"`
	JMESPath string `json:"jmespath"`
}

// ActionCallInfo is one step of a multi-call action for the describe surface.
type ActionCallInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Grant  string `json:"grant"`
	As     string `json:"as,omitempty"`
}

// ActionCollectInfo is the resolved auto-pagination leaf for the describe surface.
type ActionCollectInfo struct {
	Method       string `json:"method"`
	Path         string `json:"path"`
	Grant        string `json:"grant"`
	PageParam    string `json:"page_param"`
	LimitParam   string `json:"limit_param"`
	DefaultLimit int    `json:"default_limit"`
	As           string `json:"as"`
}

// AuthInfo is the auth scope a describe consumer sees: scheme, header, and the
// value source (provider + address). The secret value itself never appears here.
type AuthInfo struct {
	Scheme string `json:"scheme"`
	Header string `json:"header"`
	Source string `json:"source"` // value source (provider + address), not the secret
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
	gf, err = expandWildcards(spec, gf)
	if err != nil {
		return nil, err
	}
	descs, err := resolveDescriptors(spec, gf)
	if err != nil {
		return nil, err
	}
	actionDescs, err := resolveActions(spec, gf, grantedGrants(gf))
	if err != nil {
		return nil, err
	}
	// Match Build: a mount action shadows its generated leaf, so the surface
	// must drop that leaf too (the action stands in for it).
	mountActions, _ := splitMountActions(actionDescs)
	descs = suppressShadowed(descs, mountActions)
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = gf.BaseURL
	}
	display := baseURLDisplay(gf, defaultScheme(strings.TrimRight(baseURL, "/")))
	return buildSurface(gf, display, descs, actionDescs), nil
}

// buildSurface assembles the model from the already-resolved descriptors, so the
// description can never name a verb the runtime did not mount.
func buildSurface(gf *guardfile.Guardfile, baseURL string, descs []opDescriptor, actions []actionDescriptor) *Surface {
	s := &Surface{
		Group:   gf.Group,
		BaseURL: baseURL,
		Auth:    AuthInfo{Scheme: gf.Auth.Scheme, Header: gf.Auth.Header, Source: authSourceDisplay(gf.Auth)},
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
	for _, a := range actions {
		info := ActionInfo{Name: a.VerbName, Leaf: a.Name, Describe: a.Describe, FailWhen: a.FailWhen, MountVerb: a.MountVerb, MountResource: a.MountResource}
		if a.isCall() {
			for _, step := range a.Calls {
				info.Calls = append(info.Calls, ActionCallInfo{Method: step.Leaf.Method, Path: step.Leaf.Path, Grant: step.Leaf.Grant, As: step.As})
			}
		} else if a.isCollect() {
			info.Collect = &ActionCollectInfo{
				Method:       a.Collect.Leaf.Method,
				Path:         a.Collect.Leaf.Path,
				Grant:        a.Collect.Leaf.Grant,
				PageParam:    a.Collect.PageParam,
				LimitParam:   a.Collect.LimitParam,
				DefaultLimit: a.Collect.DefaultLimit,
				As:           a.Collect.As,
			}
		} else {
			info.Method, info.Path, info.Grant = a.Leaf.Method, a.Leaf.Path, a.Leaf.Grant
			info.Every, info.Timeout, info.Until = a.Every.String(), a.Timeout.String(), a.Until
			for _, in := range defaultedInputs(a) {
				info.Defaults = append(info.Defaults, ActionDefaultInfo{Input: in.Name, JMESPath: in.Default})
			}
		}
		s.Actions = append(s.Actions, info)
	}
	for _, d := range denyDescriptors(gf) {
		s.Denied = append(s.Denied, DenyInfo{Name: d.VerbName, Group: d.Group, Leaf: d.Leaf, Message: d.Message})
	}
	for _, r := range gf.Restrict {
		s.Restrict = append(s.Restrict, RestrictionInfo{Param: r.Param, Globs: r.Globs})
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
	writeActions(&b, prefix, s.Actions)
	writeRestrictions(&b, s.Restrict)
	writeDenied(&b, prefix, s.Denied)
	return b.String()
}

// writeRestrictions renders the wrap-level scope allowlists: each gated param and
// the globs an argument must match, so the reference names the enforced scope.
func writeRestrictions(b *strings.Builder, restrict []RestrictionInfo) {
	if len(restrict) == 0 {
		return
	}
	b.WriteString("\n## Scope restrictions\n\n")
	b.WriteString("Every verb whose path carries one of these parameters must supply a value matching a glob below, or it fails closed.\n")
	for _, r := range restrict {
		fmt.Fprintf(b, "\n- `%s` must match: %s\n", r.Param, strings.Join(r.Globs, ", "))
	}
}

// writeDenied renders the blocked-class stanzas: one heading per deny with its
// teaching message, so the reference documents what the guardrail forbids and why.
func writeDenied(b *strings.Builder, prefix string, denied []DenyInfo) {
	if len(denied) == 0 {
		return
	}
	b.WriteString("\n## Denied operations\n")
	for _, d := range denied {
		fmt.Fprintf(b, "\n### %s %s %s (denied)\n\n", prefix, d.Group, d.Leaf)
		fmt.Fprintf(b, "%s\n", d.Message)
	}
}

// writeActions renders the complex-action stanzas after the leaf verbs, then a
// closing note naming the condition language - the one surface a reader meets it.
func writeActions(b *strings.Builder, prefix string, actions []ActionInfo) {
	if len(actions) == 0 {
		return
	}
	for _, a := range actions {
		heading := actionHeading(prefix, a)
		if a.Describe != "" {
			heading += " - " + a.Describe
		}
		fmt.Fprintf(b, "\n%s\n\n", heading)
		if a.MountResource != "" {
			fmt.Fprintf(b, "Shadows the generated `%s %s` leaf: invoking it runs this composite in the leaf's place.\n\n", a.MountResource, a.MountVerb)
		}
		if len(a.Calls) > 0 {
			writeCallAction(b, a)
		} else if a.Collect != nil {
			writeCollectAction(b, a)
		} else {
			fmt.Fprintf(b, "Complex action. Polls `%s %s` every %s, up to %s, until:\n\n", a.Method, a.Path, a.Every, a.Timeout)
			fmt.Fprintf(b, "    %s\n\n", a.Until)
			fmt.Fprintf(b, "Authorized by grant: %s.\n", a.Grant)
			if len(a.Defaults) > 0 {
				b.WriteString("\nPre-flight defaults, resolved against the polled leaf when the input is absent:\n\n")
				for _, d := range a.Defaults {
					fmt.Fprintf(b, "- `%s` <- `%s`\n", d.Input, d.JMESPath)
				}
			}
		}
		if a.FailWhen != "" {
			fmt.Fprintf(b, "\nExits non-zero when:\n\n    %s\n", a.FailWhen)
		}
	}
	b.WriteString(conditionLanguageNote)
}

// actionHeading is an action stanza's title: a mount action reads at the leaf
// path it shadows (`<prefix> <resource> <verb>`), a named action under `action`.
func actionHeading(prefix string, a ActionInfo) string {
	if a.MountResource != "" {
		return fmt.Sprintf("## %s %s %s", prefix, a.MountResource, a.MountVerb)
	}
	return fmt.Sprintf("## %s %s %s", prefix, actionGroup, a.Leaf)
}

// writeCallAction renders a multi-call action stanza: the ordered call sequence,
// each step's method/path, the grant that authorizes it, and its `as` binding.
func writeCallAction(b *strings.Builder, a ActionInfo) {
	fmt.Fprintf(b, "Complex action. Runs %d granted calls in order, threading $step.field data between them:\n\n", len(a.Calls))
	for i, s := range a.Calls {
		line := fmt.Sprintf("%d. `%s %s`", i+1, s.Method, s.Path)
		if s.As != "" {
			line += fmt.Sprintf(" - binds the response as `%s`", s.As)
		}
		fmt.Fprintf(b, "%s\n", line)
	}
}

// writeCollectAction renders an auto-pagination action stanza.
func writeCollectAction(b *strings.Builder, a ActionInfo) {
	c := a.Collect
	fmt.Fprintf(b, "Complex action. Collects every page from `%s %s`, incrementing `%s` and appending array responses until a page returns fewer than `%d` item(s).\n\n", c.Method, c.Path, c.PageParam, c.DefaultLimit)
	fmt.Fprintf(b, "Authorized by grant: %s.\n", c.Grant)
}

// conditionLanguageNote names the until/fail-when dialect: JMESPath Community
// Edition, not the original spec. See docs/specverb-actions.md for the why.
const conditionLanguageNote = "\n## Condition language\n\n" +
	"The `until` and `fail-when` expressions above are [JMESPath, Community Edition](https://jmespath.site), " +
	"evaluated against the polled response as the root. A `$name` is a bound input or `as` capture, supplied " +
	"through the Community Edition's variable scope - baseline JMESPath (https://jmespath.org) has no `$variable` syntax, " +
	"so these expressions are not portable to an original-spec evaluator.\n"

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

// authSourceDisplay renders the value source(s) a describe surface shows: a single
// `provider address`, or `name=provider address` pairs for the query-param scheme.
func authSourceDisplay(a guardfile.Auth) string {
	if a.Scheme != "query-param" {
		return valueSourceDisplay(a.Value)
	}
	parts := make([]string, len(a.Params))
	for i, p := range a.Params {
		parts[i] = p.Name + "=" + valueSourceDisplay(p.Value)
	}
	return strings.Join(parts, ", ")
}

// valueSourceDisplay renders one source as `provider address`, or "" when unset.
func valueSourceDisplay(vs guardfile.ValueSource) string {
	if vs.IsZero() {
		return ""
	}
	return vs.Provider + " " + vs.Address
}

// authSentence states how the engine authenticates in plain language, naming the
// value source(s) but never the secret(s) they hold.
func authSentence(a AuthInfo) string {
	switch a.Scheme {
	case "":
		return "No authentication is configured."
	case "query-param":
		return fmt.Sprintf("Authenticates with query parameters (scheme %s), reading each secret from %s. The secret values are never shown.", a.Scheme, a.Source)
	default:
		return fmt.Sprintf("Authenticates with the %q header (scheme %s), reading the token from %s. The token value is never shown.", a.Header, a.Scheme, a.Source)
	}
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
