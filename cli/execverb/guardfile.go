// Package execverb mounts exec-shaped verbs from a KDL Guardfile: the exec
// dialect of the spec-driven design. See docs/execverb.md.
package execverb

import (
	"fmt"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/policy"
	kdl "github.com/calico32/kdl-go"
)

// Guardfile is the parsed form of one exec-dialect wrap block.
type Guardfile struct {
	// Description is the optional top-level `description "..."` prose (sibling of
	// `wrap`): standing context in describe + the ref doc. See docs/kdl-description.md.
	Description string

	Group      []string     // command path, e.g. ["<cli>", "git"]
	Bin        string       // the real binary, fixed at parse
	ArgvPrefix []string     // unoverridable leading argv (remote-exec transport)
	Env        []EnvVar     // environment vars set on the wrapped process
	Grants     []Grant      // mounted leaves
	Whens      []WhenClause // wrap-level passthrough guards (never pass/only pass), enforced on every leaf - the host gate
	DocLinks   []DocLink    // `doc-link` footer pointers rendered in the generated reference doc

	// Allow lists bare binaries that each mount as an independent open-passthrough
	// funnel: inspect-list sugar, mutually exclusive with exec/can run (docs/execverb.md).
	Allow []string

	// WrapWhens are wrap-level when/deny-when guards applied to every `allow`
	// funnel - the read-only floor over the whole inspect list (allow only).
	WrapWhens []WhenClause

	// Actions are the declared complex actions: call sequences with compensations
	// and a canary over granted exec leaves. See docs/execverb-actions.md.
	Actions []guardfile.Action

	// passthrough marks the `passthrough <bin>` sugar: exec + an implicit
	// `can run *` funnel. It can never coexist with `exec` or a `can run` grant.
	passthrough bool
}

// DocLink is one `doc-link` footer entry: a `## See also` back-pointer from the
// generated reference doc to a hand-written companion doc. See docs/doc-link.md.
type DocLink struct {
	Href string // link target: a relative path or URL
	Text string // link text; defaults to Href when omitted
	Desc string // optional trailing description after the link
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

	// Sealed forbids trailing caller args: the pinned `argv` forwards exactly,
	// with no caller-supplied tokens appended. Requires ArgvSet (parse-enforced).
	Sealed bool

	// Bin overrides the wrap binary for this grant only (multi-binary pipelines),
	// still fixed at parse: the caller can never substitute it. See docs/.
	Bin string
}

// ExecBin returns the binary this grant runs: its own override, else the wrap's.
func (g Grant) ExecBin(wrapBin string) string {
	if g.Bin != "" {
		return g.Bin
	}
	return wrapBin
}

// argvPrefixFor returns the transport prefix a grant inherits: a `bin`-overridden
// grant does not inherit the wrap's prefix (that prefix pins the wrap binary).
func (gf *Guardfile) argvPrefixFor(g Grant) []string {
	if g.Bin != "" {
		return nil
	}
	return gf.ArgvPrefix
}

// ExecArgv returns the tokens appended after the binary and argv-prefix: the
// `argv` override when set (possibly empty, a bare launch), else the subcommand.
func (g Grant) ExecArgv() []string {
	if g.ArgvSet {
		return g.Argv
	}
	return g.Subcommand
}

// WhenClause is a `when`/`deny-when` argv guard, or a wrap-level `never pass`/
// `only pass` passthrough guard. Grammar and selectors: docs/passthrough.md.
type WhenClause struct {
	// Selector names the argv slot: flag name, `any-arg`, or `argN`. Empty when
	// SourceCmd is set (the value comes from a shell fact, not argv).
	Selector string

	// SourceCmd is the ambient `shell <cmd>` selector source: exec'd once, its
	// trimmed stdout is the value. nil means the value comes from argv (Selector).
	SourceCmd []string

	// Patterns are globs matched case-insensitively against the value(s).
	Patterns []string

	// Deny is true for `deny-when` / `never pass` (refuse on match), false for
	// `when` / `only pass` (refuse on no match).
	Deny bool

	// OnlyReads scopes the guard to read-only aws operations.
	OnlyReads bool

	// Describe is the optional teaching note rendered in the describe surface.
	Describe string
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
	if err := gf.applyDescription(doc); err != nil {
		return nil, err
	}
	for _, a := range wrap.Arguments() {
		gf.Group = append(gf.Group, a.String())
	}
	if len(gf.Group) == 0 {
		return nil, fmt.Errorf("execverb: `wrap` needs a command path, e.g. `wrap <cli> git`")
	}
	for _, n := range wrap.Children().Nodes {
		if err := gf.applyNode(n); err != nil {
			return nil, err
		}
	}
	if err := gf.validate(); err != nil {
		return nil, err
	}
	return gf, nil
}

// applyDescription reads the optional top-level `description "..."` node (a
// sibling of `wrap`), fail-closing on a bad shape or an empty string.
func (gf *Guardfile) applyDescription(doc *kdl.Document) error {
	n := doc.GetNode("description")
	if n == nil {
		return nil
	}
	args := n.Arguments()
	if len(args) != 1 {
		return fmt.Errorf("execverb: `description` expects exactly one string, got %d (fail-closed)", len(args))
	}
	v := args[0].String()
	if v == "" {
		return fmt.Errorf("execverb: `description` must be a non-empty string (fail-closed)")
	}
	gf.Description = v
	return nil
}

// validate enforces the cross-node invariants after every wrap child is applied:
// `allow` inspect lists and exec/passthrough funnels are exclusive, fail-closed shapes.
func (gf *Guardfile) validate() error {
	if len(gf.Allow) > 0 {
		if gf.Bin != "" {
			return fmt.Errorf("execverb: `allow` is mutually exclusive with `exec` (fail-closed)")
		}
		if len(gf.Grants) > 0 {
			return fmt.Errorf("execverb: `allow` is mutually exclusive with `can run` (fail-closed)")
		}
		if len(gf.Actions) > 0 {
			return fmt.Errorf("execverb: `action` needs named `can run` grants, not an `allow` list (fail-closed)")
		}
		return nil
	}
	if len(gf.WrapWhens) > 0 {
		return fmt.Errorf("execverb: wrap-level `when`/`deny-when` applies only to an `allow` list; scope a grant's guard inside its `can run` (fail-closed)")
	}
	if gf.Bin == "" {
		return fmt.Errorf("execverb: `exec <bin>` or `passthrough <bin>` is required")
	}
	if len(gf.Grants) == 0 {
		return fmt.Errorf("execverb: no `can run` grants (nothing to mount)")
	}
	return nil
}

// applyNode dispatches one child of the wrap block onto gf.
func (gf *Guardfile) applyNode(n *kdl.Node) error {
	switch n.Name() {
	case "exec":
		if gf.passthrough {
			return fmt.Errorf("execverb: `exec` and `passthrough` are mutually exclusive (fail-closed)")
		}
		return gf.parseExec(n)
	case "passthrough":
		return gf.parsePassthrough(n)
	case "can":
		return gf.appendGrant(n)
	case "never", "cannot":
		return gf.applyNever(n)
	case "allow":
		return gf.parseAllow(n)
	case "only":
		return gf.appendPassClause(n, false)
	case "when", "deny-when":
		return gf.appendWrapWhen(n)
	case "doc-link":
		return gf.parseDocLink(n)
	case "action":
		return gf.appendAction(n)
	default:
		return fmt.Errorf("execverb: unknown node %q in wrap body (fail-closed)", n.Name())
	}
}

// appendAction parses an `action` node through the shared grammar and attaches
// it; the exec dialect only runs call actions, enforced at resolve (fail-closed).
func (gf *Guardfile) appendAction(n *kdl.Node) error {
	if gf.passthrough {
		return fmt.Errorf("execverb: `action` cannot appear under `passthrough` - actions compose named `can run` grants (fail-closed)")
	}
	act, err := guardfile.ParseActionNode(n)
	if err != nil {
		return fmt.Errorf("execverb: %w", err)
	}
	gf.Actions = append(gf.Actions, act)
	return nil
}

// appendGrant parses a `can run` grant and mounts it; refused under passthrough,
// where the wildcard funnel is the whole surface (fail-closed).
func (gf *Guardfile) appendGrant(n *kdl.Node) error {
	if gf.passthrough {
		return fmt.Errorf("execverb: `can run` cannot appear under `passthrough` - the funnel is the whole surface (fail-closed)")
	}
	g, err := parseGrant(n)
	if err != nil {
		return err
	}
	gf.Grants = append(gf.Grants, g)
	return nil
}

// appendPassClause parses a wrap-level `never pass`/`only pass` guard onto the
// host gate; deny is true for `never pass` (refuse on match).
func (gf *Guardfile) appendPassClause(n *kdl.Node, deny bool) error {
	wc, err := parsePassClause(n, deny)
	if err != nil {
		return err
	}
	gf.Whens = append(gf.Whens, wc)
	return nil
}

// appendWrapWhen parses a wrap-level `when`/`deny-when` guard onto the allow-list
// floor (the read-only guard over every inspect-list leaf).
func (gf *Guardfile) appendWrapWhen(n *kdl.Node) error {
	wc, err := parseWhen(n)
	if err != nil {
		return err
	}
	gf.WrapWhens = append(gf.WrapWhens, wc)
	return nil
}

// applyNever routes a `never`/`cannot` node: `never pass ...` enforces a
// wrap-level guard, `never run ...` is a doc-only grant that mounts nothing.
func (gf *Guardfile) applyNever(n *kdl.Node) error {
	if firstArg(n) == "pass" {
		return gf.appendPassClause(n, true)
	}
	_, err := parseGrant(n)
	return err
}

// parsePassthrough reads `passthrough <bin> [prefix...]`: sets Bin + ArgvPrefix
// and synthesizes the wildcard funnel grant. See docs/passthrough.md.
func (gf *Guardfile) parsePassthrough(n *kdl.Node) error {
	if gf.Bin != "" {
		return fmt.Errorf("execverb: `passthrough` and `exec` are mutually exclusive (fail-closed)")
	}
	if len(gf.Grants) > 0 {
		return fmt.Errorf("execverb: `passthrough` cannot coexist with `can run` grants (fail-closed)")
	}
	args := stringArgs(n)
	if len(args) < 1 {
		return fmt.Errorf("execverb: `passthrough` needs a binary, e.g. `passthrough ssh` or `passthrough tailscale ssh`")
	}
	gf.passthrough = true
	gf.Bin = args[0]
	gf.ArgvPrefix = append(gf.ArgvPrefix, args[1:]...)
	for _, c := range n.Children().Nodes {
		switch c.Name() {
		case "env":
			ev, err := parseEnv(c)
			if err != nil {
				return err
			}
			gf.Env = append(gf.Env, ev)
		default:
			return fmt.Errorf("execverb: passthrough body: unknown node %q (want env; fail-closed)", c.Name())
		}
	}
	gf.Grants = append(gf.Grants, Grant{Wildcard: true})
	return nil
}

// parsePassClause reads a wrap-level `never pass` / `only pass` guard; deny is
// true for `never pass` (refuse on match). Grammar: docs/passthrough.md.
func parsePassClause(n *kdl.Node, deny bool) (WhenClause, error) {
	args := stringArgs(n)
	if len(args) < 1 || args[0] != "pass" {
		return WhenClause{}, fmt.Errorf("execverb: `%s` guard must read `%s pass ...`", n.Name(), n.Name())
	}
	rest := args[1:]
	wc := WhenClause{Deny: deny}
	switch {
	case len(rest) > 0 && rest[0] == "when":
		if err := parseWhenCond(&wc, n.Name(), rest[1:]); err != nil {
			return WhenClause{}, err
		}
	case deny && len(rest) > 0:
		// `never pass <token...>`: deny any positional matching a token.
		wc.Selector = "any-arg"
		wc.Patterns = rest
	default:
		return WhenClause{}, fmt.Errorf("execverb: `%s pass` needs a token or a `when` clause", n.Name())
	}
	if err := parsePassDescribe(&wc, n); err != nil {
		return WhenClause{}, err
	}
	return wc, nil
}

// parseWhenCond fills a clause from the tokens after `... pass when`: a selector
// (an argv slot, or `shell <cmd>`), an `is`/`matches` comparator, then globs.
func parseWhenCond(wc *WhenClause, node string, cond []string) error {
	opIdx := indexOfComparator(cond)
	if opIdx < 0 {
		return fmt.Errorf("execverb: `%s pass when` needs an `is`/`matches` comparator", node)
	}
	source, patterns := cond[:opIdx], cond[opIdx+1:]
	if len(source) == 0 || len(patterns) == 0 {
		return fmt.Errorf("execverb: `%s pass when <selector> is <glob...>` needs both a selector and a pattern", node)
	}
	if source[0] == "shell" {
		if len(source) < 2 {
			return fmt.Errorf("execverb: `shell` selector needs a command, e.g. `shell hostname`")
		}
		wc.SourceCmd = source[1:]
	} else {
		if len(source) != 1 {
			return fmt.Errorf("execverb: argv selector %q must be a single token (`any-arg`, `argN`, or a flag name)", strings.Join(source, " "))
		}
		wc.Selector = source[0]
	}
	wc.Patterns = patterns
	return nil
}

// parsePassDescribe reads the optional `{ describe "..." }` body of a pass guard.
func parsePassDescribe(wc *WhenClause, n *kdl.Node) error {
	for _, c := range n.Children().Nodes {
		if c.Name() != "describe" {
			return fmt.Errorf("execverb: %s pass body: unknown node %q (want describe; fail-closed)", n.Name(), c.Name())
		}
		da := c.Arguments()
		if len(da) != 1 {
			return fmt.Errorf("execverb: %s pass: `describe` expects exactly one value", n.Name())
		}
		wc.Describe = da[0].String()
	}
	return nil
}

// firstArg returns the first string argument of n, or "" when it has none.
func firstArg(n *kdl.Node) string {
	a := n.Arguments()
	if len(a) == 0 {
		return ""
	}
	return a[0].String()
}

// stringArgs returns n's arguments as strings, in order.
func stringArgs(n *kdl.Node) []string {
	var out []string
	for _, a := range n.Arguments() {
		out = append(out, a.String())
	}
	return out
}

// indexOfComparator returns the index of the first `is`/`matches` token, or -1.
func indexOfComparator(tokens []string) int {
	for i, t := range tokens {
		if t == "is" || t == "matches" {
			return i
		}
	}
	return -1
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

// parseAllow reads `allow <bin...>`: each bare binary becomes an independent
// open-passthrough funnel. See docs/execverb.md for the rules; all fail closed.
func (gf *Guardfile) parseAllow(n *kdl.Node) error {
	if children := n.Children(); children != nil && len(children.Nodes) > 0 {
		return fmt.Errorf("execverb: `allow` takes a flat binary list, not a block (fail-closed)")
	}
	args := n.Arguments()
	if len(args) == 0 {
		return fmt.Errorf("execverb: `allow` needs at least one binary (empty list; fail-closed)")
	}
	for _, a := range args {
		name := a.String()
		if err := validateAllowName(name); err != nil {
			return err
		}
		for _, existing := range gf.Allow {
			if existing == name {
				return fmt.Errorf("execverb: `allow` lists %q twice (fail-closed)", name)
			}
		}
		gf.Allow = append(gf.Allow, name)
	}
	return nil
}

// parseDocLink reads `doc-link "<href>" ["<text>" ["<desc>"]]`: a `## See also`
// footer pointer. href required, text defaults to href, desc optional.
func (gf *Guardfile) parseDocLink(n *kdl.Node) error {
	if children := n.Children(); children != nil && len(children.Nodes) > 0 {
		return fmt.Errorf("execverb: `doc-link` takes positional args, not a block (fail-closed)")
	}
	args := stringArgs(n)
	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("execverb: `doc-link` needs a target, e.g. `doc-link \"../ward-kdl.md\" \"ward-kdl.md\"`")
	}
	if len(args) > 3 {
		return fmt.Errorf("execverb: `doc-link` takes at most href, text, and description (fail-closed)")
	}
	dl := DocLink{Href: args[0], Text: args[0]}
	if len(args) >= 2 && args[1] != "" {
		dl.Text = args[1]
	}
	if len(args) == 3 {
		dl.Desc = args[2]
	}
	gf.DocLinks = append(gf.DocLinks, dl)
	return nil
}

// validateAllowName enforces bare-binary names for `allow`: non-empty, no path
// separator (`/`), no shell metacharacter. A path or metachar fails closed.
func validateAllowName(name string) error {
	if name == "" {
		return fmt.Errorf("execverb: `allow` binary name is empty (fail-closed)")
	}
	if strings.ContainsRune(name, '/') {
		return fmt.Errorf("execverb: `allow` binary %q must be a bare name, not a path (fail-closed)", name)
	}
	if err := policy.ValidateArg("allow", name); err != nil {
		return fmt.Errorf("execverb: `allow` binary %q rejected (fail-closed): %w", name, err)
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
	if err := g.validateShape(); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// validateShape enforces the cross-child grant invariants after parse.
func (g Grant) validateShape() error {
	if g.Wildcard && g.ArgvSet {
		return fmt.Errorf("execverb: `can run *` cannot take an `argv` override (the wildcard funnels the whole binary; fail-closed)")
	}
	if g.Wildcard && g.Bin != "" {
		return fmt.Errorf("execverb: `can run *` cannot take a `bin` override (the wildcard IS the wrap binary's funnel; fail-closed)")
	}
	if g.Sealed && !g.ArgvSet {
		return fmt.Errorf("execverb: grant %q: `sealed` requires a pinned `argv` (nothing to seal without it; fail-closed)", g.subcommandLabel())
	}
	return nil
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
	case "argv", "sealed", "bin":
		return g.applyGrantPin(c)
	default:
		return g.applyPolicyNode(c)
	}
}

// applyGrantPin reads the invocation-pinning children of a grant: the `argv`
// override, the `sealed` marker, and the `bin` binary override.
func (g *Grant) applyGrantPin(c *kdl.Node) error {
	switch c.Name() {
	case "argv":
		if g.ArgvSet {
			return fmt.Errorf("execverb: grant %q: duplicate `argv` override (fail-closed)", g.subcommandLabel())
		}
		g.ArgvSet = true
		for _, a := range c.Arguments() {
			g.Argv = append(g.Argv, a.String())
		}
	case "sealed":
		if len(c.Arguments()) != 0 {
			return fmt.Errorf("execverb: grant %q: `sealed` takes no value (fail-closed)", g.subcommandLabel())
		}
		g.Sealed = true
	case "bin":
		if g.Bin != "" {
			return fmt.Errorf("execverb: grant %q: duplicate `bin` override (fail-closed)", g.subcommandLabel())
		}
		v, err := singleGrantArg(c)
		if err != nil {
			return err
		}
		g.Bin = v
	}
	return nil
}

// singleGrantArg reads the single string argument of a grant child node.
func singleGrantArg(c *kdl.Node) (string, error) {
	args := c.Arguments()
	if len(args) != 1 || args[0].String() == "" {
		return "", fmt.Errorf("execverb: %q expects exactly one non-empty value", c.Name())
	}
	return args[0].String(), nil
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
