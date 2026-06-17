// Package execverb mounts exec-shaped verbs from a KDL Guardfile: the exec
// dialect of the spec-driven design. See docs/execverb.md.
package execverb

import (
	"fmt"
	"strings"

	kdl "github.com/calico32/kdl-go"
)

// Guardfile is the parsed form of one exec-dialect wrap block.
type Guardfile struct {
	Group      []string // command path, e.g. ["ward", "git"]
	Bin        string   // the real binary, fixed at parse
	ArgvPrefix []string // unoverridable leading argv (remote-exec transport)
	Env        []EnvVar // environment vars set on the wrapped process
	Grants     []Grant
}

// EnvVar is one `env` injection: an environment variable set on the wrapped
// process, its value resolved at exec time through a provider. See docs/execverb.md.
type EnvVar struct {
	Name     string
	Provider string // value-source provider name (env|file|literal|consumer-registered)
	Address  string // provider-specific address (SSM path, env var name, literal value)
}

// Providers returns the distinct value-source provider names this guardfile's
// env injections name, so the driver wires the matching resolvers.
func (gf *Guardfile) Providers() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range gf.Env {
		if e.Provider != "" && !seen[e.Provider] {
			seen[e.Provider] = true
			out = append(out, e.Provider)
		}
	}
	return out
}

// Grant is one `can run <subcommand>` sentence plus its flag policy.
type Grant struct {
	Subcommand []string // e.g. ["admin", "user", "list"]
	Wildcard   bool     // `can run *`: the whole binary passes through
	AllowFlags []string // non-empty -> strict flag allowlist
	DenyFlags  []string // default-allow minus these
	Gates      []GateSpec
	Whens      []WhenClause
	Describe   string

	// Argv is the per-grant `argv` override: tokens appended after argv-prefix
	// in place of Subcommand. ArgvSet marks an explicit (maybe empty) override.
	Argv    []string
	ArgvSet bool
}

// ExecArgv returns the tokens appended after the binary and argv-prefix: the
// `argv` override when set (possibly empty, a bare launch), else the subcommand.
func (g Grant) ExecArgv() []string {
	if g.ArgvSet {
		return g.Argv
	}
	return g.Subcommand
}

// WhenClause is a `when` / `deny-when` argv guard in CLI vocabulary.
// Grammar and selector forms: docs/execverb.md.
type WhenClause struct {
	// Selector names the argv slot: flag name, `any-arg`, or `argN`.
	Selector string

	// Patterns are globs matched case-insensitively against the value(s).
	Patterns []string

	// Deny is true for `deny-when` (refuse on match), false for `when`.
	Deny bool

	// OnlyReads scopes the guard to read-only aws operations.
	OnlyReads bool
}

// GateSpec names a registered preflight gate plus its declarative config.
type GateSpec struct {
	Name     string   // registry key, e.g. "aws-read"
	Patterns []string // gate-specific deny globs; empty = gate defaults
	Allow    []string // explicit allow globs
}

// Parse turns exec-dialect Guardfile source into a Guardfile. It fails closed:
// an unknown node, missing exec block, or malformed sentence is an error.
func Parse(src []byte) (*Guardfile, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return nil, fmt.Errorf("execverb: parse KDL: %w", err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return nil, fmt.Errorf("execverb: missing top-level `wrap` node")
	}
	gf := &Guardfile{}
	for _, a := range wrap.Arguments() {
		gf.Group = append(gf.Group, a.String())
	}
	if len(gf.Group) == 0 {
		return nil, fmt.Errorf("execverb: `wrap` needs a command path, e.g. `wrap ward git`")
	}
	for _, n := range wrap.Children().Nodes {
		if err := gf.applyNode(n); err != nil {
			return nil, err
		}
	}
	if gf.Bin == "" {
		return nil, fmt.Errorf("execverb: `exec <bin>` is required")
	}
	if len(gf.Grants) == 0 {
		return nil, fmt.Errorf("execverb: no `can run` grants (nothing to mount)")
	}
	return gf, nil
}

// applyNode dispatches one child of the wrap block onto gf.
func (gf *Guardfile) applyNode(n *kdl.Node) error {
	switch n.Name() {
	case "exec":
		return gf.parseExec(n)
	case "can":
		g, err := parseGrant(n)
		if err != nil {
			return err
		}
		gf.Grants = append(gf.Grants, g)
		return nil
	case "never", "cannot":
		// explicit denials parse (documentation value) but mount nothing
		_, err := parseGrant(n)
		return err
	default:
		return fmt.Errorf("execverb: unknown node %q in wrap body (fail-closed)", n.Name())
	}
}

// parseExec reads `exec <bin>` and the optional argv-prefix child.
func (gf *Guardfile) parseExec(n *kdl.Node) error {
	args := n.Arguments()
	if len(args) != 1 {
		return fmt.Errorf("execverb: `exec` expects exactly one binary, got %d", len(args))
	}
	gf.Bin = args[0].String()
	for _, c := range n.Children().Nodes {
		switch c.Name() {
		case "argv-prefix":
			for _, a := range c.Arguments() {
				gf.ArgvPrefix = append(gf.ArgvPrefix, a.String())
			}
		case "env":
			ev, err := parseEnv(c)
			if err != nil {
				return err
			}
			gf.Env = append(gf.Env, ev)
		default:
			return fmt.Errorf("execverb: exec body: unknown node %q (want argv-prefix | env; fail-closed)", c.Name())
		}
	}
	return nil
}

// parseEnv reads an `env "<NAME>" "<literal>"` or `env "<NAME>" { value
// <provider> "<addr>" }` injection; the provider form keeps an opaque value out.
func parseEnv(n *kdl.Node) (EnvVar, error) {
	args := n.Arguments()
	if len(args) < 1 || args[0].String() == "" {
		return EnvVar{}, fmt.Errorf("execverb: `env` needs a name, e.g. `env \"OLLAMA_HOST\" { value ssm \"/path\" }`")
	}
	ev := EnvVar{Name: args[0].String()}
	children := n.Children().Nodes
	switch {
	case len(args) == 2 && len(children) == 0:
		ev.Provider, ev.Address = "literal", args[1].String()
	case len(args) == 1 && len(children) == 1 && children[0].Name() == "value":
		vargs := children[0].Arguments()
		if len(vargs) != 2 || vargs[0].String() == "" || vargs[1].String() == "" {
			return EnvVar{}, fmt.Errorf("execverb: env %q: value needs a non-empty provider and address, e.g. `value ssm \"/path\"`", ev.Name)
		}
		ev.Provider, ev.Address = vargs[0].String(), vargs[1].String()
	default:
		return EnvVar{}, fmt.Errorf("execverb: env %q: want `env \"NAME\" \"literal\"` or `env \"NAME\" { value <provider> \"<addr>\" }` (fail-closed)", ev.Name)
	}
	return ev, nil
}

// parseGrant reads one `can run <subcommand...>` sentence and its policy body.
func parseGrant(n *kdl.Node) (Grant, error) {
	args := n.Arguments()
	if len(args) < 2 || args[0].String() != "run" {
		return Grant{}, fmt.Errorf("execverb: %q grant must read `%s run <subcommand>`", n.Name(), n.Name())
	}
	var g Grant
	for _, a := range args[1:] {
		// a quoted multi-word sentence ("admin user list") splits to path words
		g.Subcommand = append(g.Subcommand, strings.Fields(a.String())...)
	}
	if len(g.Subcommand) == 1 && g.Subcommand[0] == "*" {
		g.Subcommand = nil
		g.Wildcard = true
	}
	for _, c := range n.Children().Nodes {
		if err := g.applyGrantChild(c); err != nil {
			return Grant{}, err
		}
	}
	if g.Wildcard && g.ArgvSet {
		return Grant{}, fmt.Errorf("execverb: `can run *` cannot take an `argv` override (the wildcard funnels the whole binary; fail-closed)")
	}
	return g, nil
}

// applyGrantChild dispatches one child of a `can run` grant: a gate, a
// when/deny-when guard, or a flag-policy/describe node.
func (g *Grant) applyGrantChild(c *kdl.Node) error {
	switch c.Name() {
	case "gate":
		gs, err := parseGate(c)
		if err != nil {
			return err
		}
		g.Gates = append(g.Gates, gs)
		return nil
	case "when", "deny-when":
		wc, err := parseWhen(c)
		if err != nil {
			return err
		}
		g.Whens = append(g.Whens, wc)
		return nil
	case "argv":
		if g.ArgvSet {
			return fmt.Errorf("execverb: grant %q: duplicate `argv` override (fail-closed)", g.subcommandLabel())
		}
		g.ArgvSet = true
		for _, a := range c.Arguments() {
			g.Argv = append(g.Argv, a.String())
		}
		return nil
	default:
		return g.applyPolicyNode(c)
	}
}

// parseWhen reads a `when|deny-when <selector> matches <glob...>` guard and its
// optional `{ only-reads }` qualifier block.
func parseWhen(c *kdl.Node) (WhenClause, error) {
	args := c.Arguments()
	if len(args) < 3 || args[1].String() != "matches" {
		return WhenClause{}, fmt.Errorf("execverb: %q must read `%s <selector> matches <glob...>`", c.Name(), c.Name())
	}
	wc := WhenClause{Selector: args[0].String(), Deny: c.Name() == "deny-when"}
	for _, a := range args[2:] {
		wc.Patterns = append(wc.Patterns, a.String())
	}
	for _, n := range c.Children().Nodes {
		switch n.Name() {
		case "only-reads":
			if len(n.Arguments()) != 0 {
				return WhenClause{}, fmt.Errorf("execverb: %s: `only-reads` takes no value", c.Name())
			}
			wc.OnlyReads = true
		default:
			return WhenClause{}, fmt.Errorf("execverb: %s: unknown qualifier %q (fail-closed)", c.Name(), n.Name())
		}
	}
	return wc, nil
}

// parseGate reads a `gate <name> { pattern|allow ... }` child.
func parseGate(c *kdl.Node) (GateSpec, error) {
	args := c.Arguments()
	if len(args) != 1 {
		return GateSpec{}, fmt.Errorf("execverb: `gate` expects exactly one name, got %d", len(args))
	}
	gs := GateSpec{Name: args[0].String()}
	for _, n := range c.Children().Nodes {
		na := n.Arguments()
		if len(na) != 1 {
			return GateSpec{}, fmt.Errorf("execverb: gate %s: %q expects exactly one value", gs.Name, n.Name())
		}
		v := na[0].String()
		switch n.Name() {
		case "pattern":
			gs.Patterns = append(gs.Patterns, v)
		case "allow":
			gs.Allow = append(gs.Allow, v)
		default:
			return GateSpec{}, fmt.Errorf("execverb: gate %s: unknown node %q (fail-closed)", gs.Name, n.Name())
		}
	}
	return gs, nil
}

// applyPolicyNode reads one flag-policy or describe child of a grant.
func (g *Grant) applyPolicyNode(c *kdl.Node) error {
	args := c.Arguments()
	if len(args) != 1 {
		return fmt.Errorf("execverb: grant %q: %q expects exactly one value", strings.Join(g.Subcommand, " "), c.Name())
	}
	v := args[0].String()
	switch c.Name() {
	case "deny-flag":
		g.DenyFlags = append(g.DenyFlags, v)
	case "allow-flag":
		g.AllowFlags = append(g.AllowFlags, v)
	case "describe":
		g.Describe = v
	default:
		return fmt.Errorf("execverb: grant body: unknown node %q (fail-closed)", c.Name())
	}
	return nil
}

// subcommandLabel renders the grant's path for error messages.
func (g Grant) subcommandLabel() string {
	if g.Wildcard {
		return "*"
	}
	return strings.Join(g.Subcommand, " ")
}
