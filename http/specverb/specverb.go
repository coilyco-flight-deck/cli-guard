// Package specverb is the runtime engine of the spec-driven verb design: it
// builds a guarded cli tree from a Guardfile + spec. See docs/specverb.md.
package specverb

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"github.com/urfave/cli/v3"
)

// defaultHTTPTimeout bounds a single live request made through the engine's
// default client (Config.HTTPClient nil).
const defaultHTTPTimeout = 30 * time.Second

// TokenResolver fetches the auth secret named by an SSM path. Injected so the
// AWS SDK stays out of cli-guard; nil is only valid for dry-run-only use.
type TokenResolver func(ctx context.Context, ssmPath string) (string, error)

// Config is everything the engine needs to build a command tree.
type Config struct {
	// Guardfile is the parsed L2 policy: the command path, auth, and grants.
	Guardfile *guardfile.Guardfile

	// Spec is the raw Swagger 2.0 document bytes (embedded by the consumer).
	Spec []byte

	// Wrap adapts a verb.Spec into a guarded cli.ActionFunc (the audit + argv
	// pipeline). nil mounts the bare action, for doc rendering only.
	Wrap func(verb.Spec) cli.ActionFunc

	// Token resolves the auth secret. nil is tolerated for a dry-run-only tree.
	Token TokenResolver

	// HTTPClient fires the live request. nil uses http.DefaultClient.
	HTTPClient *http.Client

	// BaseURL overrides the Guardfile base-url. "" uses the Guardfile value.
	BaseURL string
}

// opDescriptor is the tiny per-operation payload the generic action binds to,
// isolated from the static request machinery.
type opDescriptor struct {
	VerbName    string         // dotted audit name, e.g. ward.ops.forgejo.repo.create
	Group       string         // CLI group noun, e.g. repo
	Leaf        string         // CLI leaf verb, e.g. create
	Method      string         // HTTP method
	Path        string         // path template, e.g. /repos/{owner}/{repo}
	PathParams  []string       // ordered positional args drawn from the path
	BodyFlags   []fieldFlag    // request-body fields promoted to flags
	QueryFlags  []fieldFlag    // scalar query params promoted to flags
	FormFlags   []fieldFlag    // formData params; "file" types take a path
	FixedBody   map[string]any // exact body for state-toggle leaves; no body flags mount
	Destructive bool           // leaf mutates irreversibly (delete)
	Grant       string         // the authorizing grant sentence, e.g. "can delete repos"
	Describe    string         // optional Guardfile describe "..." note, "" if none
}

// fieldFlag is one spec input promoted to a typed CLI flag: a request-body
// field (scalar or array-of-scalar) or a scalar query parameter.
type fieldFlag struct {
	Name     string // json field / query param name, doubling as the flag name
	Type     string // swagger type: string|boolean|integer|number|array
	Items    string // scalar element type when Type is "array"
	Required bool   // required in the schema; enforced at request assembly
	Desc     string
}

// typeLabel renders the flag's type for help and the reference doc.
func (f fieldFlag) typeLabel() string {
	if f.Type == "array" {
		return "[]" + f.Items
	}
	return f.Type
}

// Build assembles the guarded command tree and returns the Guardfile group's
// leaf command (e.g. `forgejo`). Fails closed: an unresolvable grant is an error.
func Build(cfg Config) (*cli.Command, error) {
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
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = gf.BaseURL
	}

	rt := &runtime{
		baseURL: defaultScheme(strings.TrimRight(baseURL, "/")),
		auth:    gf.Auth,
		token:   cfg.Token,
		client:  cfg.HTTPClient,
		wrap:    cfg.Wrap,
	}
	if rt.client == nil {
		rt.client = defaultHTTPClient()
	}
	if rt.wrap == nil {
		// identity: bare action, no audit pipeline. Doc-render path only.
		rt.wrap = func(s verb.Spec) cli.ActionFunc { return s.Action }
	}

	descs, err := resolveDescriptors(spec, gf)
	if err != nil {
		return nil, err
	}
	if len(descs) == 0 {
		return nil, fmt.Errorf("specverb: Guardfile mounted no verbs (no `can` grants resolved)")
	}
	actionDescs, err := resolveActions(spec, gf, grantedGrants(gf))
	if err != nil {
		return nil, err
	}
	denies := denyDescriptors(gf)
	groupCmds := rt.buildGroups(descs, denies)
	if ag := rt.buildActionGroup(actionDescs); ag != nil {
		groupCmds = append(groupCmds, ag)
	}
	surface := buildSurface(gf, rt.baseURL, descs, actionDescs)
	groupCmds = append(groupCmds, rt.buildDescribeLeaf(gf, surface))
	root := &cli.Command{
		Name:     gf.Group[len(gf.Group)-1],
		Usage:    fmt.Sprintf("spec-driven %s verbs", strings.Join(gf.Group, " ")),
		Commands: groupCmds,
	}
	return root, nil
}

// Mount builds the guarded group and grafts it onto root, generating the
// intermediate path groups the Guardfile names. See docs/specverb.md.
func Mount(root *cli.Command, cfg Config) error {
	if root == nil {
		return fmt.Errorf("specverb: Mount root is nil")
	}
	group, err := Build(cfg)
	if err != nil {
		return err
	}
	// Group [ward, ops, forgejo]: index 0 is root, the last is group.Name, the
	// middle is the path to find-or-create under root.
	path := cfg.Guardfile.Group
	parent := root
	if len(path) > 1 {
		for _, seg := range path[1 : len(path)-1] {
			parent = findOrCreateGroup(parent, seg)
		}
	}
	parent.Commands = append(parent.Commands, group)
	return nil
}

// findOrCreateGroup returns parent's child named name, creating an empty group
// command for it when absent so an intermediate path segment is mounted once.
func findOrCreateGroup(parent *cli.Command, name string) *cli.Command {
	for _, c := range parent.Commands {
		if c.Name == name {
			return c
		}
	}
	g := &cli.Command{Name: name, Usage: name + " operations"}
	parent.Commands = append(parent.Commands, g)
	return g
}

// defaultScheme prepends https:// to a base-url that carries no scheme, so a
// Guardfile may write `base-url "host/api/v1"` and rely on TLS by default.
func defaultScheme(baseURL string) string {
	if baseURL == "" || strings.Contains(baseURL, "://") {
		return baseURL
	}
	return "https://" + baseURL
}

// defaultHTTPClient (used when Config.HTTPClient is nil) follows GET/HEAD
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
			return fmt.Errorf("specverb: refusing to follow a %s redirect to %s; a mutating verb must not be silently downgraded", via[0].Method, req.URL)
		},
	}
}

// grantKey is the (verb-class, resource) pair an authored grant names: the CLI
// placement (Resource is the group, Verb is the leaf).
type grantKey struct {
	Verb     string
	Resource string
}

// destructiveVerbs names the irreversibly-mutating verb classes. Generic, not
// API-specific: `delete` is destructive whatever the resource.
var destructiveVerbs = map[string]bool{"delete": true}

// grantedGrants maps each `can` grant by its (verb, resource) placement, so an
// action can recover the grant - and its op - for a leaf it polls or calls.
func grantedGrants(gf *guardfile.Guardfile) map[grantKey]guardfile.Grant {
	denied := deniedKeys(gf)
	keys := map[grantKey]guardfile.Grant{}
	for _, g := range gf.Grants {
		if g.Modal != "can" {
			continue
		}
		k := grantKey{Verb: g.Verb, Resource: g.Resource}
		if _, blocked := denied[k]; blocked {
			continue // a denied leaf is not pollable by an action
		}
		keys[k] = g
	}
	return keys
}

// resolveDescriptors resolves every `can` grant into a concrete descriptor, in
// first-seen order, dropping any `can` a deny also names (deny beats allow).
func resolveDescriptors(spec *swaggerSpec, gf *guardfile.Guardfile) ([]opDescriptor, error) {
	denied := deniedKeys(gf)
	var descs []opDescriptor
	for _, g := range gf.Grants {
		if g.Modal != "can" {
			continue
		}
		if _, blocked := denied[grantKey{Verb: g.Verb, Resource: g.Resource}]; blocked {
			continue // a cannot/never for this class beats the allow
		}
		desc, err := resolveDescriptor(spec, gf.Group, g)
		if err != nil {
			return nil, err
		}
		descs = append(descs, desc)
	}
	return descs, nil
}

// buildGroups buckets the descriptors into resource-group commands in first-seen
// order. Deny leaves mount beside the allowed leaves. See docs/specverb.md.
func (rt *runtime) buildGroups(descs []opDescriptor, denies []denyDescriptor) []*cli.Command {
	groups := map[string]*cli.Command{}
	var order []string
	groupFor := func(name string) *cli.Command {
		grp, ok := groups[name]
		if !ok {
			grp = &cli.Command{Name: name, Usage: fmt.Sprintf("%s operations", name)}
			groups[name] = grp
			order = append(order, name)
		}
		return grp
	}
	for _, desc := range descs {
		grp := groupFor(desc.Group)
		grp.Commands = append(grp.Commands, rt.buildLeaf(desc))
	}
	for _, d := range denies {
		grp := groupFor(d.Group)
		grp.Commands = append(grp.Commands, rt.buildDenyLeaf(d))
	}
	out := make([]*cli.Command, 0, len(order))
	for _, name := range order {
		out = append(out, groups[name])
	}
	return out
}

// resolveDescriptor turns one grant into a concrete descriptor, failing closed at
// each gate (no `op`, or an op the spec lacks). Resource is the group, Verb the leaf.
func resolveDescriptor(spec *swaggerSpec, group []string, g guardfile.Grant) (opDescriptor, error) {
	if g.Op == "" {
		return opDescriptor{}, fmt.Errorf("specverb: grant %q %q %q has no `op` binding (deny-by-default)", g.Modal, g.Verb, g.Resource)
	}
	method, path, op, err := spec.findOp(g.Op)
	if err != nil {
		return opDescriptor{}, err
	}
	bodySchema, _, err := spec.bodySchema(op)
	if err != nil {
		return opDescriptor{}, err
	}
	desc := opDescriptor{
		VerbName:    strings.Join(group, ".") + "." + g.Resource + "." + g.Verb,
		Group:       g.Resource,
		Leaf:        g.Verb,
		Method:      method,
		Path:        path,
		PathParams:  pathParamsInOrder(path),
		BodyFlags:   bodyFlagsFrom(bodySchema),
		QueryFlags:  queryFlagsFrom(op),
		FormFlags:   formFlagsFrom(op),
		FixedBody:   g.FixedBody,
		Destructive: destructiveVerbs[g.Verb],
		Grant:       formatGrant(g),
		Describe:    g.Describe,
	}
	if len(desc.FixedBody) > 0 {
		// a state toggle owns its body: the spec's edit fields stay unmounted
		desc.BodyFlags = nil
	}
	if err := checkFlagCollisions(desc); err != nil {
		return opDescriptor{}, err
	}
	return desc, nil
}

// reservedFlagNames are the universal per-leaf flags no spec input may shadow.
var reservedFlagNames = map[string]bool{
	flagDryRun: true, flagQuery: true, flagOutput: true, flagBodyFile: true,
}

// checkFlagCollisions fails closed when a promoted spec input would shadow a
// universal flag or another promoted input on the same leaf.
func checkFlagCollisions(desc opDescriptor) error {
	seen := map[string]bool{}
	all := append(append([]fieldFlag{}, desc.QueryFlags...), desc.BodyFlags...)
	for _, f := range append(all, desc.FormFlags...) {
		if reservedFlagNames[f.Name] {
			return fmt.Errorf("specverb: %s: spec input %q collides with a reserved engine flag (fail-closed)", desc.VerbName, f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("specverb: %s: two spec inputs both name %q (fail-closed)", desc.VerbName, f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// formatGrant renders the authorizing grant sentence for help and describe,
// e.g. {can, delete, repos, [created-by-me]} -> "can delete repos created-by-me".
func formatGrant(g guardfile.Grant) string {
	parts := append([]string{g.Modal, g.Verb, g.Resource}, g.Qualifiers...)
	return strings.Join(parts, " ")
}

// bodyFlagsFrom promotes a body schema's scalar and array-of-scalar properties
// to typed flags; required-ness is enforced at assembly, not the CLI layer.
func bodyFlagsFrom(schema *swaggerSchema) []fieldFlag {
	if schema == nil {
		return nil
	}
	required := map[string]bool{}
	for _, r := range schema.Required {
		required[r] = true
	}
	// Stable order: sort property names so the flag set and help are
	// deterministic across runs (Go map iteration is randomized).
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	var flags []fieldFlag
	for _, name := range names {
		if f, ok := fieldFlagFor(name, schema.Properties[name], required[name]); ok {
			flags = append(flags, f)
		}
	}
	return flags
}

// fieldFlagFor lowers one body property to a flag; ok=false for shapes only
// --body-file can express (nested objects).
func fieldFlagFor(name string, prop swaggerSchema, required bool) (fieldFlag, bool) {
	f := fieldFlag{Name: name, Required: required, Desc: prop.Description}
	switch {
	case isScalar(prop.Type):
		f.Type = prop.Type
	case prop.Type == "array" && prop.Items != nil && isScalar(prop.Items.Type):
		f.Type = "array"
		f.Items = prop.Items.Type
	case prop.Type == "array" && prop.Items != nil && prop.Items.Type == "":
		// untyped union items (forgejo's "label ids or names") lower to strings
		f.Type = "array"
		f.Items = "string"
	default:
		return fieldFlag{}, false
	}
	return f, true
}

// queryFlagsFrom promotes an operation's scalar query parameters to flags.
func queryFlagsFrom(op swaggerOp) []fieldFlag {
	var flags []fieldFlag
	for _, p := range queryParamsOf(op) {
		flags = append(flags, fieldFlag{
			Name:     p.Name,
			Type:     p.Type,
			Required: p.Required,
			Desc:     p.Description,
		})
	}
	return flags
}

// formFlagsFrom promotes an operation's formData parameters to flags; a "file"
// param mounts as a path-taking string flag the action streams into multipart.
func formFlagsFrom(op swaggerOp) []fieldFlag {
	var flags []fieldFlag
	for _, p := range formParamsOf(op) {
		flags = append(flags, fieldFlag{
			Name:     p.Name,
			Type:     p.Type,
			Required: p.Required,
			Desc:     p.Description,
		})
	}
	return flags
}

// isScalar reports whether a swagger type lowers to a single CLI flag value.
func isScalar(t string) bool {
	switch t {
	case "string", "boolean", "integer", "number":
		return true
	}
	return false
}
