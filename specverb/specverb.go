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

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/verb"
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
	VerbName    string     // dotted audit name, e.g. ward.ops.forgejo.repo.create
	Group       string     // CLI group noun, e.g. repo
	Leaf        string     // CLI leaf verb, e.g. create
	Method      string     // HTTP method
	Path        string     // path template, e.g. /repos/{owner}/{repo}
	PathParams  []string   // ordered positional args drawn from the path
	BodyFlags   []bodyFlag // request-body scalar fields promoted to flags
	Destructive bool       // leaf mutates irreversibly (delete)
	Grant       string     // the authorizing grant sentence, e.g. "can delete repos"
	Describe    string     // optional Guardfile describe "..." note, "" if none
}

// bodyFlag is one request-body scalar field promoted to a typed CLI flag.
type bodyFlag struct {
	Name     string // json field name, doubling as the flag name
	Type     string // swagger scalar type: string|boolean|integer|number
	Required bool   // required schema field -> required flag
	Desc     string
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
	groupCmds := rt.buildGroups(descs)
	surface := buildSurface(gf, rt.baseURL, descs)
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

// resolveDescriptors resolves every `can` grant into a concrete descriptor, in
// first-seen order. Unresolvable = fail-closed error, never a dropped verb.
func resolveDescriptors(spec *swaggerSpec, gf *guardfile.Guardfile) ([]opDescriptor, error) {
	var descs []opDescriptor
	for _, g := range gf.Grants {
		// cannot/never are explicit denials; overlap-removal is an M2 concern.
		if g.Modal != "can" {
			continue
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
// order, mounting each as a guarded leaf under its noun.
func (rt *runtime) buildGroups(descs []opDescriptor) []*cli.Command {
	groups := map[string]*cli.Command{}
	var order []string
	for _, desc := range descs {
		grp, ok := groups[desc.Group]
		if !ok {
			grp = &cli.Command{Name: desc.Group, Usage: fmt.Sprintf("%s operations", desc.Group)}
			groups[desc.Group] = grp
			order = append(order, desc.Group)
		}
		grp.Commands = append(grp.Commands, rt.buildLeaf(desc))
	}
	out := make([]*cli.Command, 0, len(order))
	for _, name := range order {
		out = append(out, groups[name])
	}
	return out
}

// resolveDescriptor turns one grant into a concrete descriptor, failing closed
// at each gate (no table row, or a row naming an op the spec lacks).
func resolveDescriptor(spec *swaggerSpec, group []string, g guardfile.Grant) (opDescriptor, error) {
	entry, ok := lookupExpansion(g.Verb, g.Resource)
	if !ok {
		return opDescriptor{}, fmt.Errorf("specverb: grant %q %q %q has no expansion-table row (deny-by-default)", g.Modal, g.Verb, g.Resource)
	}
	method, path, op, err := spec.findOp(entry.OperationID)
	if err != nil {
		return opDescriptor{}, err
	}
	bodySchema, _, err := spec.bodySchema(op)
	if err != nil {
		return opDescriptor{}, err
	}
	desc := opDescriptor{
		VerbName:    strings.Join(group, ".") + "." + entry.Group + "." + entry.Leaf,
		Group:       entry.Group,
		Leaf:        entry.Leaf,
		Method:      method,
		Path:        path,
		PathParams:  pathParamsInOrder(path),
		BodyFlags:   bodyFlagsFrom(bodySchema),
		Destructive: destructiveLeaves[entry.Leaf],
		Grant:       formatGrant(g),
		Describe:    g.Describe,
	}
	return desc, nil
}

// formatGrant renders the authorizing grant sentence for help and describe,
// e.g. {can, delete, repos, [created-by-me]} -> "can delete repos created-by-me".
func formatGrant(g guardfile.Grant) string {
	parts := append([]string{g.Modal, g.Verb, g.Resource}, g.Qualifiers...)
	return strings.Join(parts, " ")
}

// bodyFlagsFrom promotes a body schema's scalar properties to typed flags
// (required-in-schema -> required flag). Non-scalars are skipped at M0.
func bodyFlagsFrom(schema *swaggerSchema) []bodyFlag {
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
	var flags []bodyFlag
	for _, name := range names {
		prop := schema.Properties[name]
		if !isScalar(prop.Type) {
			continue
		}
		flags = append(flags, bodyFlag{
			Name:     name,
			Type:     prop.Type,
			Required: required[name],
			Desc:     prop.Description,
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
