// Package specverb is the runtime engine of the spec-driven verb design: it
// builds a guarded cli tree from a Guardfile + spec. See docs/specverb.md.
package specverb

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

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
		baseURL: strings.TrimRight(baseURL, "/"),
		auth:    gf.Auth,
		token:   cfg.Token,
		client:  cfg.HTTPClient,
		wrap:    cfg.Wrap,
	}
	if rt.client == nil {
		rt.client = http.DefaultClient
	}
	if rt.wrap == nil {
		// identity: bare action, no audit pipeline. Doc-render path only.
		rt.wrap = func(s verb.Spec) cli.ActionFunc { return s.Action }
	}

	groupCmds, err := rt.mountGrants(spec, gf)
	if err != nil {
		return nil, err
	}
	if len(groupCmds) == 0 {
		return nil, fmt.Errorf("specverb: Guardfile mounted no verbs (no `can` grants resolved)")
	}
	root := &cli.Command{
		Name:     gf.Group[len(gf.Group)-1],
		Usage:    fmt.Sprintf("spec-driven %s verbs", strings.Join(gf.Group, " ")),
		Commands: groupCmds,
	}
	return root, nil
}

// mountGrants resolves every `can` grant into a guarded leaf, bucketed into
// resource-group commands in first-seen order. Unresolvable = fail-closed error.
func (rt *runtime) mountGrants(spec *swaggerSpec, gf *guardfile.Guardfile) ([]*cli.Command, error) {
	groups := map[string]*cli.Command{}
	var order []string
	for _, g := range gf.Grants {
		// cannot/never are explicit denials; overlap-removal is an M2 concern.
		if g.Modal != "can" {
			continue
		}
		desc, err := resolveDescriptor(spec, gf.Group, g)
		if err != nil {
			return nil, err
		}
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
	return out, nil
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
	}
	return desc, nil
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
