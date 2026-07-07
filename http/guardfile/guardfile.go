// Package guardfile parses a KDL Guardfile, the human authoring layer (L2) of
// the specverb engine, into a typed model. KDL is parsed, never evaluated.
package guardfile

import (
	"fmt"
	"strings"

	kdl "github.com/calico32/kdl-go"
)

// ValueSource names where a config value is read at request time: a Provider
// (ssm, tailscale, env, ...) and the Address it interprets. See specverb-policy.md.
type ValueSource struct {
	Provider string
	Address  string
}

// IsZero reports whether the source is unset (no provider named).
func (v ValueSource) IsZero() bool { return v.Provider == "" }

// ValueChain is an ordered fallback list of value sources: resolution takes the
// first yielding a non-empty value. See specverb-value-chain.md.
type ValueChain []ValueSource

// IsZero reports whether the chain names no source.
func (c ValueChain) IsZero() bool { return len(c) == 0 }

// String renders the chain as `provider address` sources joined by " | " (the
// symbolic form describe/--dry-run show; "" when zero, never a resolved value).
func (c ValueChain) String() string {
	parts := make([]string, 0, len(c))
	for _, vs := range c {
		parts = append(parts, vs.Provider+" "+vs.Address)
	}
	return strings.Join(parts, " | ")
}

// Auth describes how the engine authenticates to the target API. Three schemes:
// header-token, bearer, query-param (dual-secret). See docs/specverb.md.
type Auth struct {
	Scheme string
	Header string
	Prefix string // trailing space is significant, e.g. "token "
	Value  ValueChain

	// Params are the query-param scheme's ordered secrets, each injected as a
	// query parameter (Trello's ?key=&token=). Empty for the header schemes.
	Params []QueryAuthParam
}

// QueryAuthParam is one secret of the query-param scheme: a query parameter Name
// whose value is read from the named value source.
type QueryAuthParam struct {
	Name  string
	Value ValueChain
}

// Grant is one policy sentence: modal verb resource [qualifiers...] [key=value...].
// Resource is the CLI group, Verb the leaf, Op the operationId. See docs/specverb.md.
type Grant struct {
	Modal      string
	Verb       string
	Resource   string
	Qualifiers []string

	// Props are KDL key=value node properties: structured scoping constraints
	// like `org=acme`. Positional bareword qualifiers stay in Qualifiers.
	Props map[string]string

	// Op is the spec operationId (grant-body `op "..."`). Optional: an empty Op is
	// resolved by convention (specverb.resolveOp); set it to override. Deny ignores it.
	Op string

	// FixedBody is the grant-body `body key=value...` map: a state-toggle leaf that
	// always sends this exact JSON and mounts no body flags. Keeps KDL-native types.
	FixedBody map[string]any

	// Message is the grant-body `message "..."` shown when a deny blocks an
	// invocation - the teaching error. Only meaningful on cannot/never.
	Message string

	// Describe is the optional grant-body `describe "..."` note that enriches
	// the thin upstream spec; it flows into help and the describe verb.
	Describe string

	// Wildcard is set when the resource was authored as the `"*"` sentinel: the
	// engine expands it to one concrete grant per spec resource exposing the verb.
	Wildcard bool

	// Override is set for an `override can <verb> <resource>` grant: the sole
	// construct that crosses an inherited `never`. It carries Modal "can".
	Override bool
}

// Restriction is a wrap-level `restrict <param> matches "<glob>"...` allowlist:
// a {Param}-carrying leaf must match a Glob or fail closed. See docs/specverb.md.
type Restriction struct {
	Param string
	Globs []string
}

// Input is one parameter an Action declares (a positional arg or a flag). Its
// name doubles as the JMESPath `$name` variable in `until`/`fail-when`.
type Input struct {
	Name       string
	Positional bool   // true: a positional arg; false: a --flag
	Required   bool   // enforced at invocation
	Help       string // one-line help, "" if none
	// Default is a JMESPath pre-flight binding for an absent input (poll actions
	// only); mutually exclusive with Required. See specverb-action-defaults.md.
	Default string
}

// ArgBind is one `args { <name> <value> }` binding for the polled leaf. A
// `$input` value references an Input; else it is a literal. See specverb-actions.md.
type ArgBind struct {
	Name  string
	Value string // `$input` reference or a literal
}

// Poll re-fires a granted leaf (Verb+Resource) until Until settles or Timeout
// elapses, sampling Every; the last response binds to As. See specverb-actions.md.
type Poll struct {
	Verb     string
	Resource string
	Args     []ArgBind
	Until    string // JMESPath; truthy ends the loop
	Every    string // sample interval, e.g. "10s" (quoted: KDL rejects bare 10s)
	Timeout  string // wall-clock bound, e.g. "30m"
	As       string // binding name for the final response
}

// Call is one step of a multi-call action: fires a granted leaf with Args, binds
// the response to As for `$As.field` data-flow. See specverb-actions.md.
type Call struct {
	Verb     string
	Resource string
	Args     []ArgBind
	As       string // binding name for this call's response

	// Compensate is the optional rollback step, fired to undo this call when a
	// later step fails. See specverb-rollback.md.
	Compensate *Compensation
}

// Compensation is a call's rollback step: a granted leaf fired (with Args) to
// undo the call when a later step fails. See specverb-rollback.md.
type Compensation struct {
	Verb     string
	Resource string
	Args     []ArgBind
}

// Canary re-samples a granted leaf every Every up to Window after the forward
// steps, rolling back on DegradedWhen mid-window. See specverb-rollback.md.
type Canary struct {
	Verb         string
	Resource     string
	Args         []ArgBind
	Every        string // sample interval, e.g. "5s" (quoted: KDL rejects bare 5s)
	Window       string // watch window, e.g. "1m"; elapsing without degradation passes
	DegradedWhen string // JMESPath; truthy mid-window triggers the rollback path
	HealthyWhen  string // optional JMESPath; truthy ends the watch early as healthy
	As           string // binding name for each sampled response
}

// Collect walks a paginated granted leaf, incrementing PageParam and appending
// each array response until the page returns fewer than Limit (specverb-actions.md).
type Collect struct {
	Verb         string
	Resource     string
	Args         []ArgBind
	PageParam    string
	LimitParam   string
	Limit        string
	DefaultLimit string
	As           string
	// Cache, when set, is the TTL string (e.g. "10m") serving the accumulated
	// array from the on-disk ttl cache. Collect-only (see docs/specverb-actions.md).
	Cache string
}

// Action is a named composite verb: exactly one of Poll, Calls, or Collect, plus
// an optional FailWhen exit. See specverb-actions.md.
type Action struct {
	Name     string
	Describe string
	Inputs   []Input
	Poll     *Poll
	Calls    []Call // ordered multi-call sequence; mutually exclusive with Poll
	Collect  *Collect
	FailWhen string // JMESPath over the bindings; truthy => non-zero exit

	// Canary is the optional health-window watch fired after the `call` steps;
	// call-only, it rolls them back on degradation. See specverb-rollback.md.
	Canary *Canary

	// MountVerb/MountResource: the two-arg `action <verb> <resource>` mount form
	// shadows that leaf path. Empty for `action <name>`. See specverb-actions.md.
	MountVerb     string
	MountResource string
}

// IsMount reports whether the action shadows a leaf path (two-arg form).
func (a Action) IsMount() bool { return a.MountResource != "" }

// Guardfile is the parsed form of one wrap block.
type Guardfile struct {
	Group   []string // command path, e.g. ["ward", "ops", "forgejo"]
	Spec    string
	BaseURL string
	// BaseURLValue resolves the base-url at request time (block form
	// `base-url { value ... }`), exclusive with BaseURL. See specverb-policy.md.
	BaseURLValue ValueChain
	Auth         Auth
	Grants       []Grant
	Restrict     []Restriction
	Actions      []Action
	DocLinks     []DocLink // `doc-link` footer pointers rendered in the generated reference doc
}

// DocLink is one `doc-link` footer entry: a `## See also` back-pointer from the
// generated reference doc to a hand-written companion doc. See docs/doc-link.md.
type DocLink struct {
	Href string // link target: a relative path or URL
	Text string // link text; defaults to Href when omitted
	Desc string // optional trailing description after the link
}

// modals is the closed set of grant verbs; anything else fails closed.
var modals = map[string]bool{"can": true, "cannot": true, "never": true}

// overrideNode is the escalation directive's node name: `override can <verb>
// <resource>` re-grants a single denied class by name. See specverb-inherit.md.
const overrideNode = "override"

// isDenyModal reports whether a modal denies (cannot/never) rather than grants.
func isDenyModal(modal string) bool { return modal == "cannot" || modal == "never" }

// Parse turns Guardfile source into a Guardfile. It fails closed: an unknown
// node, a missing required field, or a malformed sentence is an error.
func Parse(src []byte) (*Guardfile, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return nil, fmt.Errorf("guardfile: parse KDL: %w", err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return nil, fmt.Errorf("guardfile: missing top-level `wrap` node")
	}
	gf := &Guardfile{}
	for _, a := range wrap.Arguments() {
		gf.Group = append(gf.Group, a.String())
	}
	if len(gf.Group) == 0 {
		return nil, fmt.Errorf("guardfile: `wrap` needs a command path, e.g. `wrap ward ops forgejo`")
	}
	for _, n := range wrap.Children().Nodes {
		if n.Name() == inheritNode {
			// inherit is resolved textually by Flatten/ParseFile before this parse;
			// reaching it here means Parse was handed unflattened source.
			return nil, fmt.Errorf("guardfile: `inherit` needs file-path resolution; load with guardfile.ParseFile (fail-closed)")
		}
		if err := gf.applyNode(n); err != nil {
			return nil, err
		}
	}
	return gf, gf.validate()
}

// applyNode dispatches one child of the wrap block onto gf.
func (gf *Guardfile) applyNode(n *kdl.Node) error {
	name := n.Name()
	if isGrantNode(name) {
		g, err := parseGrantOrOverride(n)
		if err != nil {
			return err
		}
		gf.Grants = append(gf.Grants, g)
		return nil
	}
	switch name {
	case "spec":
		v, err := singleArg(n)
		gf.Spec = v
		return err
	case "base-url":
		return gf.applyBaseURL(n)
	case "auth":
		a, err := parseAuth(n)
		gf.Auth = a
		return err
	default:
		return gf.applyListNode(n, name)
	}
}

// applyListNode handles the repeatable wrap-body nodes (restrict, action,
// doc-link) and the fail-closed unknown fallback, split off to hold the cyclo cap.
func (gf *Guardfile) applyListNode(n *kdl.Node, name string) error {
	switch name {
	case "restrict":
		r, err := parseRestrict(n)
		if err != nil {
			return err
		}
		gf.Restrict = append(gf.Restrict, r)
		return nil
	case "action":
		act, err := parseAction(n)
		if err != nil {
			return err
		}
		gf.Actions = append(gf.Actions, act)
		return nil
	case "doc-link":
		dl, err := parseDocLink(n)
		if err != nil {
			return err
		}
		gf.DocLinks = append(gf.DocLinks, dl)
		return nil
	default:
		return unknownWrapNode(name)
	}
}

// unknownWrapNode is the fail-closed error for a wrap-body node applyNode does
// not handle, distinguishing a reserved-for-future keyword from a plain unknown.
func unknownWrapNode(name string) error {
	if reservedActionKeywords[name] {
		return fmt.Errorf("guardfile: %q is reserved for a future version and is not implemented in v1 (fail-closed)", name)
	}
	return fmt.Errorf("guardfile: unknown node %q in wrap body (fail-closed)", name)
}

// applyBaseURL reads the base-url node onto gf via the shared ParseBaseURL.
func (gf *Guardfile) applyBaseURL(n *kdl.Node) error {
	raw, chain, err := ParseBaseURL(n)
	if err != nil {
		return err
	}
	// Assign only the form this node carried, so a second base-url node in the
	// other form accumulates into both fields and validate catches the conflict.
	if raw != "" {
		gf.BaseURL = raw
	}
	if !chain.IsZero() {
		gf.BaseURLValue = chain
	}
	return nil
}

// ParseBaseURL reads a base-url node: a bare string (raw) or a `{ value ... }`
// block (chain). Exactly one is non-zero. Shared with the opcore inline source.
func ParseBaseURL(n *kdl.Node) (raw string, chain ValueChain, err error) {
	children := n.Children().Nodes
	if len(children) == 0 {
		v, serr := singleArg(n)
		return v, nil, serr
	}
	for _, c := range children {
		if c.Name() != "value" {
			return "", nil, fmt.Errorf("guardfile: base-url: unknown field %q (want value; fail-closed)", c.Name())
		}
		vc, verr := parseValueChain(c)
		if verr != nil {
			return "", nil, fmt.Errorf("guardfile: base-url: %w", verr)
		}
		chain = vc
	}
	if chain.IsZero() {
		return "", nil, fmt.Errorf("guardfile: base-url block requires `value <provider> \"...\"`")
	}
	return "", chain, nil
}

// parseValueChain reads a `value` node in either form (inline `value <p> "<a>"`
// or a `{ ... }` fallback block) into a ValueChain. See specverb-value-chain.md.
func parseValueChain(n *kdl.Node) (ValueChain, error) {
	_, hasBlock := n.ChildrenInline()
	args := n.Arguments()
	switch {
	case hasBlock && len(args) > 0:
		return nil, fmt.Errorf("value takes either an inline `value <provider> \"<address>\"` or a `{ ... }` fallback block, not both (fail-closed)")
	case hasBlock:
		children := n.Children().Nodes
		if len(children) == 0 {
			return nil, fmt.Errorf("value block is empty; list at least one `<provider> \"<address>\"` fallback source")
		}
		chain := make(ValueChain, 0, len(children))
		for _, c := range children {
			vs, err := valueSourceFromChild(c)
			if err != nil {
				return nil, err
			}
			chain = append(chain, vs)
		}
		return chain, nil
	default:
		vs, err := valueSourceInline(args)
		if err != nil {
			return nil, err
		}
		return ValueChain{vs}, nil
	}
}

// valueSourceInline reads the inline `value <provider> "<address>"` args: exactly
// two, the provider name then the address, both non-empty.
func valueSourceInline(args []kdl.Value) (ValueSource, error) {
	if len(args) != 2 {
		return ValueSource{}, fmt.Errorf("value needs a provider and an address, e.g. `value ssm \"/forgejo/api-token\"` (got %d arg(s))", len(args))
	}
	vs := ValueSource{Provider: args[0].String(), Address: args[1].String()}
	if vs.Provider == "" || vs.Address == "" {
		return ValueSource{}, fmt.Errorf("value needs a non-empty provider and address")
	}
	return vs, nil
}

// valueSourceFromChild reads one `<provider> "<address>"` fallback source inside a
// value block: the node name is the provider, its single argument the address.
func valueSourceFromChild(c *kdl.Node) (ValueSource, error) {
	provider := c.Name()
	args := c.Arguments()
	if len(args) != 1 {
		return ValueSource{}, fmt.Errorf("value source %q needs an address, e.g. `%s \"/forgejo/api-token\"` (got %d arg(s))", provider, provider, len(args))
	}
	if len(c.Children().Nodes) > 0 || len(c.Properties()) > 0 {
		return ValueSource{}, fmt.Errorf("value source %q takes a single address argument, no children or properties (fail-closed)", provider)
	}
	vs := ValueSource{Provider: provider, Address: args[0].String()}
	if vs.Provider == "" || vs.Address == "" {
		return ValueSource{}, fmt.Errorf("value source needs a non-empty provider and address")
	}
	return vs, nil
}

// Providers returns the distinct provider names every value source in gf names,
// so a consumer (or the codegen) can wire exactly the resolvers in use.
func (gf *Guardfile) Providers() []string {
	seen := map[string]bool{}
	var out []string
	add := func(chain ValueChain) {
		for _, vs := range chain {
			if vs.Provider == "" || seen[vs.Provider] {
				continue
			}
			seen[vs.Provider] = true
			out = append(out, vs.Provider)
		}
	}
	add(gf.Auth.Value)
	for _, p := range gf.Auth.Params {
		add(p.Value)
	}
	add(gf.BaseURLValue)
	return out
}

// reservedActionKeywords are the forward-design slots v1 does not implement;
// parsing one is a fail-closed error, not a silent no-op. See specverb-actions.md.
var reservedActionKeywords = map[string]bool{
	"read": true,                 // non-poll single-leaf read (the leaf seam)
	"emit": true, "cursor": true, // per-tick streaming-delta slots on poll
	"each": true, "yield": true, // fan-out body (deferred v2)
	"follow": true, "stream": true, "tail": true, // live log-tail keywords
}

// validate enforces the required header fields.
func (gf *Guardfile) validate() error {
	if gf.Spec == "" {
		return fmt.Errorf("guardfile: `spec` is required")
	}
	if gf.Auth.Scheme == "" {
		return fmt.Errorf("guardfile: `auth` block is required")
	}
	if gf.BaseURL != "" && !gf.BaseURLValue.IsZero() {
		return fmt.Errorf("guardfile: base-url set both as a string and a `{ value }` block; pick one")
	}
	return gf.validateOverrides()
}

// validateOverrides fails closed on a no-op `override` (one crossing no deny):
// silently it is a plain `can`. See docs/specverb-override.md.
func (gf *Guardfile) validateOverrides() error {
	for _, g := range gf.Grants {
		if !g.Override {
			continue
		}
		if !gf.denyCovers(g.Verb, g.Resource) {
			return fmt.Errorf("guardfile: `override can %s %s` lifts no `never`/`cannot` (it would be a silent no-op `can`); deny the class first or drop the override",
				g.Verb, g.Resource)
		}
	}
	return nil
}

// denyCovers reports whether some deny grant blocks (verb, resource): a matching
// `never`/`cannot` for the exact resource or for the verb-global `"*"`.
func (gf *Guardfile) denyCovers(verb, resource string) bool {
	for _, d := range gf.Grants {
		if isDenyModal(d.Modal) && d.Verb == verb && (d.Resource == resource || d.Resource == wildcardSentinel) {
			return true
		}
	}
	return false
}

// wildcardSentinel is the verb-global resource sentinel a `*` grant carries.
const wildcardSentinel = "*"

// ParseAuthNode parses one `auth` KDL node into an Auth: the entry the opcore
// inline source shares, so both sources speak one auth grammar.
func ParseAuthNode(n *kdl.Node) (Auth, error) { return parseAuth(n) }

// ParseRestrictNode parses one `restrict` KDL node into a Restriction, shared
// with the opcore inline source so both sources speak one restrict grammar.
func ParseRestrictNode(n *kdl.Node) (Restriction, error) { return parseRestrict(n) }

// parseAuth reads the auth block, dispatching on the named scheme. Three are
// supported: header-token, bearer, query-param. See docs/specverb.md.
func parseAuth(n *kdl.Node) (Auth, error) {
	scheme, err := singleArg(n)
	if err != nil {
		return Auth{}, fmt.Errorf("guardfile: auth: %w", err)
	}
	switch scheme {
	case "header-token":
		return parseHeaderTokenAuth(n)
	case "bearer":
		return parseBearerAuth(n)
	case "query-param":
		return parseQueryParamAuth(n)
	default:
		return Auth{}, fmt.Errorf("guardfile: auth scheme %q unsupported (want header-token | bearer | query-param)", scheme)
	}
}

// parseHeaderTokenAuth reads `header-token { header H; prefix "..."; value P A }`.
func parseHeaderTokenAuth(n *kdl.Node) (Auth, error) {
	a := Auth{Scheme: "header-token"}
	for _, c := range n.Children().Nodes {
		switch c.Name() {
		case "header":
			v, ferr := singleArg(c)
			if ferr != nil {
				return Auth{}, fmt.Errorf("guardfile: auth header: %w", ferr)
			}
			a.Header = v
		case "prefix":
			v, ferr := singleArg(c)
			if ferr != nil {
				return Auth{}, fmt.Errorf("guardfile: auth prefix: %w", ferr)
			}
			a.Prefix = v
		case "value":
			vc, ferr := parseValueChain(c)
			if ferr != nil {
				return Auth{}, fmt.Errorf("guardfile: auth %w", ferr)
			}
			a.Value = vc
		default:
			return Auth{}, fmt.Errorf("guardfile: auth: unknown field %q (fail-closed)", c.Name())
		}
	}
	if a.Header == "" || a.Value.IsZero() {
		return Auth{}, fmt.Errorf("guardfile: auth header-token requires `header` and `value <provider> \"...\"`")
	}
	return a, nil
}

// parseBearerAuth reads `bearer { value <provider> A }`: shorthand for the
// Authorization header with a "Bearer " prefix (Tailscale).
func parseBearerAuth(n *kdl.Node) (Auth, error) {
	a := Auth{Scheme: "bearer", Header: "Authorization", Prefix: "Bearer "}
	for _, c := range n.Children().Nodes {
		if c.Name() != "value" {
			return Auth{}, fmt.Errorf("guardfile: auth bearer: unknown field %q (want value; fail-closed)", c.Name())
		}
		vc, ferr := parseValueChain(c)
		if ferr != nil {
			return Auth{}, fmt.Errorf("guardfile: auth bearer %w", ferr)
		}
		a.Value = vc
	}
	if a.Value.IsZero() {
		return Auth{}, fmt.Errorf("guardfile: auth bearer requires `value <provider> \"...\"`")
	}
	return a, nil
}

// parseQueryParamAuth reads `query-param { param <name> { value <provider> A } ... }`:
// one or more secrets injected as query parameters (Trello's ?key=&token=).
func parseQueryParamAuth(n *kdl.Node) (Auth, error) {
	a := Auth{Scheme: "query-param"}
	for _, c := range n.Children().Nodes {
		if c.Name() != "param" {
			return Auth{}, fmt.Errorf("guardfile: auth query-param: unknown field %q (want param; fail-closed)", c.Name())
		}
		name, err := singleArg(c)
		if err != nil {
			return Auth{}, fmt.Errorf("guardfile: auth query-param: %w (name it: `param key { value <provider> \"...\" }`)", err)
		}
		p := QueryAuthParam{Name: name}
		for _, cc := range c.Children().Nodes {
			if cc.Name() != "value" {
				return Auth{}, fmt.Errorf("guardfile: auth query-param %s: unknown field %q (want value)", name, cc.Name())
			}
			vc, ferr := parseValueChain(cc)
			if ferr != nil {
				return Auth{}, fmt.Errorf("guardfile: auth query-param %s: %w", name, ferr)
			}
			p.Value = vc
		}
		if p.Value.IsZero() {
			return Auth{}, fmt.Errorf("guardfile: auth query-param %q requires `value <provider> \"...\"`", name)
		}
		a.Params = append(a.Params, p)
	}
	if len(a.Params) == 0 {
		return Auth{}, fmt.Errorf("guardfile: auth query-param requires at least one `param`")
	}
	return a, nil
}

// parseGrant reads one policy sentence: modal verb resource [qualifiers...].
func parseGrant(n *kdl.Node) (Grant, error) {
	args := n.Arguments()
	if len(args) < 2 {
		return Grant{}, fmt.Errorf("guardfile: %q needs a verb and a resource, e.g. `%s create repos`", n.Name(), n.Name())
	}
	g := Grant{Modal: n.Name(), Verb: args[0].String(), Resource: args[1].String()}
	if g.Resource == "*" {
		g.Wildcard = true // verb-global: expanded per resource by the engine
	}
	for _, q := range args[2:] {
		g.Qualifiers = append(g.Qualifiers, q.String())
	}
	for k, v := range n.Properties() {
		if g.Props == nil {
			g.Props = map[string]string{}
		}
		g.Props[k] = v.String()
	}
	for _, c := range n.Children().Nodes {
		if err := applyGrantChild(&g, n.Name(), c); err != nil {
			return Grant{}, err
		}
	}
	return g, nil
}

// parseGrantOrOverride lowers a grant sentence: a modal `can`/`cannot`/`never`,
// or the `override can …` escalation form.
func parseGrantOrOverride(n *kdl.Node) (Grant, error) {
	if n.Name() == overrideNode {
		return parseOverride(n)
	}
	return parseGrant(n)
}

// parseOverride reads `override can <verb> <resource>`: only the `can` form is
// valid and the resource must be named (`*` rejected). See specverb-override.md.
func parseOverride(n *kdl.Node) (Grant, error) {
	args := n.Arguments()
	if len(args) < 3 || args[0].String() != "can" {
		return Grant{}, fmt.Errorf("guardfile: `override` must read `override can <verb> <resource>` (the only escalation form)")
	}
	g := Grant{Modal: "can", Override: true, Verb: args[1].String(), Resource: args[2].String()}
	if g.Resource == "*" {
		return Grant{}, fmt.Errorf("guardfile: `override can %s \"*\"` is not allowed: an override names one resource so every escalation is reviewable by name", g.Verb)
	}
	for _, q := range args[3:] {
		g.Qualifiers = append(g.Qualifiers, q.String())
	}
	for k, v := range n.Properties() {
		if g.Props == nil {
			g.Props = map[string]string{}
		}
		g.Props[k] = v.String()
	}
	for _, c := range n.Children().Nodes {
		if err := applyGrantChild(&g, n.Name(), c); err != nil {
			return Grant{}, err
		}
	}
	return g, nil
}

// applyGrantChild dispatches one grant-body child onto g. modal is the grant's
// node name, used only to enrich error messages.
func applyGrantChild(g *Grant, modal string, c *kdl.Node) error {
	switch c.Name() {
	case "op":
		v, err := singleArg(c)
		if err != nil {
			return fmt.Errorf("guardfile: grant %q: %w", modal, err)
		}
		g.Op = v
	case "body":
		// A fixed-body toggle: `body state="closed"` -> always send that JSON.
		// Properties keep their KDL-native type so booleans stay booleans.
		if len(c.Properties()) == 0 {
			return fmt.Errorf("guardfile: grant %q: `body` needs at least one key=value (e.g. `body state=\"closed\"`)", modal)
		}
		g.FixedBody = map[string]any{}
		for k, val := range c.Properties() {
			g.FixedBody[k] = val.RawValue()
		}
	case "message":
		v, err := singleArg(c)
		if err != nil {
			return fmt.Errorf("guardfile: grant %q: %w", modal, err)
		}
		g.Message = v
	case "describe":
		v, err := singleArg(c)
		if err != nil {
			return fmt.Errorf("guardfile: grant %q: %w", modal, err)
		}
		g.Describe = v
	default:
		return fmt.Errorf("guardfile: grant body: unknown node %q (want op | body | message | describe; fail-closed)", c.Name())
	}
	return nil
}

// parseRestrict reads a `restrict <param> matches "<glob>"...` allowlist clause.
func parseRestrict(n *kdl.Node) (Restriction, error) {
	args := n.Arguments()
	// shape: restrict <param> matches <glob> [<glob>...]
	if len(args) < 3 || args[1].String() != "matches" {
		return Restriction{}, fmt.Errorf("guardfile: restrict needs `restrict <param> matches \"<glob>\"...`")
	}
	r := Restriction{Param: args[0].String()}
	for _, g := range args[2:] {
		r.Globs = append(r.Globs, g.String())
	}
	return r, nil
}

// parseDocLink reads `doc-link "<href>" ["<text>" ["<desc>"]]`: a `## See also`
// footer pointer. href required, text defaults to href, desc optional.
func parseDocLink(n *kdl.Node) (DocLink, error) {
	if children := n.Children(); children != nil && len(children.Nodes) > 0 {
		return DocLink{}, fmt.Errorf("guardfile: `doc-link` takes positional args, not a block (fail-closed)")
	}
	args := n.Arguments()
	if len(args) == 0 || args[0].String() == "" {
		return DocLink{}, fmt.Errorf("guardfile: `doc-link` needs a target, e.g. `doc-link \"ward-kdl.md\" \"ward-kdl.md\"`")
	}
	if len(args) > 3 {
		return DocLink{}, fmt.Errorf("guardfile: `doc-link` takes at most href, text, and description (fail-closed)")
	}
	dl := DocLink{Href: args[0].String(), Text: args[0].String()}
	if len(args) >= 2 && args[1].String() != "" {
		dl.Text = args[1].String()
	}
	if len(args) == 3 {
		dl.Desc = args[2].String()
	}
	return dl, nil
}

// ParseActionNode parses one `action` KDL node into an Action: the entry the
// exec dialect shares, so both dialects speak one action grammar.
func ParseActionNode(n *kdl.Node) (Action, error) { return parseAction(n) }

// parseAction reads one `action <name> { ... }` block into an Action. It fails
// closed: an unknown body node, a missing poll, or a reserved keyword is an error.
func parseAction(n *kdl.Node) (Action, error) {
	act, err := actionHeader(n)
	if err != nil {
		return Action{}, err
	}
	for _, c := range n.Children().Nodes {
		if err := applyActionChild(&act, c); err != nil {
			return Action{}, fmt.Errorf("guardfile: action %q: %w", act.Name, err)
		}
	}
	switch {
	case act.Poll == nil && len(act.Calls) == 0 && act.Collect == nil:
		return Action{}, fmt.Errorf("guardfile: action %q: needs a `poll`, `collect`, or at least one `call` step", act.Name)
	case countActionKinds(act) > 1:
		return Action{}, fmt.Errorf("guardfile: action %q: `poll`, `collect`, and `call` are mutually exclusive", act.Name)
	case act.Canary != nil && len(act.Calls) == 0:
		return Action{}, fmt.Errorf("guardfile: action %q: `canary` needs at least one `call` step to guard (it rolls back the executed compensations on degradation)", act.Name)
	}
	return act, nil
}

func countActionKinds(act Action) int {
	var n int
	if act.Poll != nil {
		n++
	}
	if len(act.Calls) > 0 {
		n++
	}
	if act.Collect != nil {
		n++
	}
	return n
}

// actionHeader reads an action's header: one arg is `action <name>` (mounts under
// the `action` noun), two is the `action <verb> <resource>` mount form.
func actionHeader(n *kdl.Node) (Action, error) {
	args := n.Arguments()
	switch len(args) {
	case 1:
		return Action{Name: args[0].String()}, nil
	case 2:
		verb, resource := args[0].String(), args[1].String()
		if verb == "" || resource == "" {
			return Action{}, fmt.Errorf("guardfile: action: mount form needs a non-empty verb and resource (`action view issue { ... }`)")
		}
		return Action{Name: verb + "-" + resource, MountVerb: verb, MountResource: resource}, nil
	default:
		return Action{}, fmt.Errorf("guardfile: action: name it `action ci-watch { ... }` or mount it `action view issue { ... }` (got %d header arg(s))", len(args))
	}
}

// applyActionChild dispatches one child node of an action body onto act; the
// step kinds (poll/collect/call/canary) split off to applyActionStep.
func applyActionChild(act *Action, c *kdl.Node) error {
	switch c.Name() {
	case "describe":
		v, err := singleArg(c)
		act.Describe = v
		return err
	case "input":
		in, err := parseInput(c)
		if err != nil {
			return err
		}
		act.Inputs = append(act.Inputs, in)
		return nil
	case "fail-when":
		v, err := singleArg(c)
		if err != nil {
			return fmt.Errorf("fail-when: %w", err)
		}
		act.FailWhen = v
		return nil
	default:
		return applyActionStep(act, c)
	}
}

// applyActionStep dispatches the step-kind children of an action body: the
// poll/collect/call primitives and the canary watch, fail-closed on the rest.
func applyActionStep(act *Action, c *kdl.Node) error {
	switch c.Name() {
	case "poll":
		return addPoll(act, c)
	case "collect":
		return addCollect(act, c)
	case "call":
		call, err := parseCall(c)
		if err != nil {
			return err
		}
		act.Calls = append(act.Calls, call)
		return nil
	case "canary":
		return addCanary(act, c)
	default:
		if reservedActionKeywords[c.Name()] {
			return fmt.Errorf("%q is reserved for a future version, not implemented in v1 (fail-closed)", c.Name())
		}
		return fmt.Errorf("unknown body node %q (fail-closed)", c.Name())
	}
}

// addPoll parses a poll child and attaches it to act, rejecting a second poll.
func addPoll(act *Action, c *kdl.Node) error {
	if act.Poll != nil {
		return fmt.Errorf("v1 allows exactly one `poll` per action")
	}
	p, err := parsePoll(c)
	if err != nil {
		return err
	}
	act.Poll = &p
	return nil
}

// addCanary parses a canary child and attaches it to act, rejecting a second one.
func addCanary(act *Action, c *kdl.Node) error {
	if act.Canary != nil {
		return fmt.Errorf("v1 allows exactly one `canary` per action")
	}
	can, err := parseCanary(c)
	if err != nil {
		return err
	}
	act.Canary = &can
	return nil
}

// addCollect parses a collect child and attaches it to act, rejecting a second
// collect block.
func addCollect(act *Action, c *kdl.Node) error {
	if act.Collect != nil {
		return fmt.Errorf("v1 allows exactly one `collect` per action")
	}
	col, err := parseCollect(c)
	if err != nil {
		return err
	}
	act.Collect = &col
	return nil
}

// parseInput reads one `input <name> { ... }` child: exactly one of
// positional/flag is required; required and default are mutually exclusive.
func parseInput(n *kdl.Node) (Input, error) {
	name, err := singleArg(n)
	if err != nil {
		return Input{}, fmt.Errorf("input: %w (name it: `input repo { positional }`)", err)
	}
	in := Input{Name: name}
	kindSet := false
	for _, c := range n.Children().Nodes {
		set, ferr := applyInputField(&in, c)
		if ferr != nil {
			return Input{}, ferr
		}
		kindSet = kindSet || set
	}
	if !kindSet {
		return Input{}, fmt.Errorf("input %q: declare exactly one of `positional` or `flag`", name)
	}
	if in.Required && in.Default != "" {
		return Input{}, fmt.Errorf("input %q: `required` and `default` are mutually exclusive (a default only resolves when the input is absent)", name)
	}
	return in, nil
}

// applyInputField applies one child field of an input block to in, reporting
// whether it declared the positional/flag kind. An unknown field fails closed.
func applyInputField(in *Input, c *kdl.Node) (kindSet bool, err error) {
	switch c.Name() {
	case "positional":
		in.Positional = true
		return true, nil
	case "flag":
		in.Positional = false
		return true, nil
	case "required":
		in.Required = true
		return false, nil
	case "help":
		v, herr := singleArg(c)
		if herr != nil {
			return false, fmt.Errorf("input %q: %w", in.Name, herr)
		}
		in.Help = v
		return false, nil
	case "default":
		v, derr := singleArg(c)
		if derr != nil {
			return false, fmt.Errorf("input %q: %w", in.Name, derr)
		}
		in.Default = v
		return false, nil
	default:
		return false, fmt.Errorf("input %q: unknown field %q (want positional | flag | required | help | default; fail-closed)", in.Name, c.Name())
	}
}

// parseCall reads a `call <verb> <resource> { args {...}; as <name> }` step of a
// multi-call action. Args and As are both optional. See specverb-actions.md.
func parseCall(n *kdl.Node) (Call, error) {
	args := n.Arguments()
	if len(args) != 2 {
		return Call{}, fmt.Errorf("call needs a verb and a resource, e.g. `call view issues { ... }`")
	}
	cl := Call{Verb: args[0].String(), Resource: args[1].String()}
	for _, c := range n.Children().Nodes {
		switch c.Name() {
		case "args":
			binds, err := parseArgBinds(c, "call")
			if err != nil {
				return Call{}, err
			}
			cl.Args = binds
		case "as":
			v, err := singleArg(c)
			if err != nil {
				return Call{}, fmt.Errorf("call as: %w", err)
			}
			cl.As = v
		case "compensate":
			comp, err := parseCompensation(c)
			if err != nil {
				return Call{}, err
			}
			cl.Compensate = &comp
		default:
			if reservedActionKeywords[c.Name()] {
				return Call{}, fmt.Errorf("call: %q is reserved for a future version (fail-closed)", c.Name())
			}
			return Call{}, fmt.Errorf("call: unknown body node %q (want args | as | compensate; fail-closed)", c.Name())
		}
	}
	return cl, nil
}

// parseArgBinds reads an `args` block, two forms: named children (spec dialect)
// or positional values (exec dialect, Name empty). Mixing them fails closed.
func parseArgBinds(n *kdl.Node, label string) ([]ArgBind, error) {
	var binds []ArgBind
	if args := n.Arguments(); len(args) > 0 {
		if len(n.Children().Nodes) > 0 {
			return nil, fmt.Errorf("%s args: positional values and named children cannot mix (fail-closed)", label)
		}
		for _, a := range args {
			binds = append(binds, ArgBind{Value: a.String()})
		}
		return binds, nil
	}
	for _, a := range n.Children().Nodes {
		v, err := singleArg(a)
		if err != nil {
			return nil, fmt.Errorf("%s args %q: %w", label, a.Name(), err)
		}
		binds = append(binds, ArgBind{Name: a.Name(), Value: v})
	}
	return binds, nil
}

// parseCompensation reads a `compensate <verb> <resource> { args {...} }` child:
// the granted leaf fired to undo its parent call on a later failure.
func parseCompensation(n *kdl.Node) (Compensation, error) {
	args := n.Arguments()
	if len(args) != 2 {
		return Compensation{}, fmt.Errorf("compensate needs a verb and a resource, e.g. `compensate delete snapshot { ... }`")
	}
	comp := Compensation{Verb: args[0].String(), Resource: args[1].String()}
	for _, c := range n.Children().Nodes {
		if c.Name() != "args" {
			return Compensation{}, fmt.Errorf("compensate: unknown body node %q (want args; fail-closed)", c.Name())
		}
		binds, err := parseArgBinds(c, "compensate")
		if err != nil {
			return Compensation{}, err
		}
		comp.Args = binds
	}
	return comp, nil
}

// parseCanary reads a `canary <verb> <resource> { ... }` block. Every, Window,
// DegradedWhen, and As are mandatory (an unbounded watch is unreviewable).
func parseCanary(n *kdl.Node) (Canary, error) {
	args := n.Arguments()
	if len(args) != 2 {
		return Canary{}, fmt.Errorf("canary needs a verb and a resource, e.g. `canary get health { ... }`")
	}
	can := Canary{Verb: args[0].String(), Resource: args[1].String()}
	for _, c := range n.Children().Nodes {
		if err := applyCanaryChild(&can, c); err != nil {
			return Canary{}, err
		}
	}
	switch {
	case can.Every == "":
		return Canary{}, fmt.Errorf("canary: `every` is required (the sample interval)")
	case can.Window == "":
		return Canary{}, fmt.Errorf("canary: `window` is required (no unbounded watch exists in the grammar)")
	case can.DegradedWhen == "":
		return Canary{}, fmt.Errorf("canary: `degraded-when` is required (the JMESPath that triggers rollback)")
	case can.As == "":
		return Canary{}, fmt.Errorf("canary: `as` is required (the binding name for each sampled response)")
	}
	return can, nil
}

// applyCanaryChild dispatches one child node of a canary body onto can; the
// scalar fields share one single-arg path keyed by a table.
func applyCanaryChild(can *Canary, c *kdl.Node) error {
	if c.Name() == "args" {
		binds, err := parseArgBinds(c, "canary")
		if err != nil {
			return err
		}
		can.Args = binds
		return nil
	}
	scalars := map[string]*string{
		"every": &can.Every, "window": &can.Window,
		"degraded-when": &can.DegradedWhen, "healthy-when": &can.HealthyWhen, "as": &can.As,
	}
	target, ok := scalars[c.Name()]
	if !ok {
		if reservedActionKeywords[c.Name()] {
			return fmt.Errorf("canary: %q is reserved for a future version (fail-closed)", c.Name())
		}
		return fmt.Errorf("canary: unknown body node %q (want args | every | window | degraded-when | healthy-when | as; fail-closed)", c.Name())
	}
	v, err := singleArg(c)
	if err != nil {
		return fmt.Errorf("canary %s: %w (durations quote: `every \"5s\"`)", c.Name(), err)
	}
	*target = v
	return nil
}

// parsePoll reads a `poll <verb> <resource> { args {...}; until; every; timeout;
// as }` block. Every, Timeout, Until, and As are mandatory: the bound is grammar.
func parsePoll(n *kdl.Node) (Poll, error) {
	args := n.Arguments()
	if len(args) != 2 {
		return Poll{}, fmt.Errorf("poll needs a verb and a resource, e.g. `poll list tasks { ... }`")
	}
	p := Poll{Verb: args[0].String(), Resource: args[1].String()}
	for _, c := range n.Children().Nodes {
		if err := applyPollChild(&p, c); err != nil {
			return Poll{}, err
		}
	}
	switch {
	case p.Until == "":
		return Poll{}, fmt.Errorf("poll: `until` is required (the JMESPath that ends the loop)")
	case p.Every == "":
		return Poll{}, fmt.Errorf("poll: `every` is required (no unbounded poll exists in the grammar)")
	case p.Timeout == "":
		return Poll{}, fmt.Errorf("poll: `timeout` is required (no unbounded poll exists in the grammar)")
	case p.As == "":
		return Poll{}, fmt.Errorf("poll: `as` is required (the binding name for the final response)")
	}
	return p, nil
}

// parseCollect reads a `collect <verb> <resource> { args {...}; page-param;
// limit-param; limit; default-limit; as; cache }` block.
func parseCollect(n *kdl.Node) (Collect, error) {
	args := n.Arguments()
	if len(args) != 2 {
		return Collect{}, fmt.Errorf("collect needs a verb and a resource, e.g. `collect list issues { ... }`")
	}
	col := Collect{Verb: args[0].String(), Resource: args[1].String()}
	for _, c := range n.Children().Nodes {
		if err := applyCollectChild(&col, c); err != nil {
			return Collect{}, err
		}
	}
	switch {
	case col.PageParam == "":
		return Collect{}, fmt.Errorf("collect: `page-param` is required")
	case col.LimitParam == "":
		return Collect{}, fmt.Errorf("collect: `limit-param` is required")
	case col.DefaultLimit == "":
		return Collect{}, fmt.Errorf("collect: `default-limit` is required")
	case col.As == "":
		return Collect{}, fmt.Errorf("collect: `as` is required (the binding name for the accumulated array)")
	}
	return col, nil
}

func applyCollectChild(col *Collect, c *kdl.Node) error {
	if c.Name() == "args" {
		for _, a := range c.Children().Nodes {
			v, err := singleArg(a)
			if err != nil {
				return fmt.Errorf("collect args %q: %w", a.Name(), err)
			}
			col.Args = append(col.Args, ArgBind{Name: a.Name(), Value: v})
		}
		return nil
	}
	scalars := map[string]*string{
		"page-param":    &col.PageParam,
		"limit-param":   &col.LimitParam,
		"limit":         &col.Limit,
		"default-limit": &col.DefaultLimit,
		"as":            &col.As,
		"cache":         &col.Cache,
	}
	target, ok := scalars[c.Name()]
	if !ok {
		if reservedActionKeywords[c.Name()] {
			return fmt.Errorf("collect: %q is reserved for a future version (fail-closed)", c.Name())
		}
		return fmt.Errorf("collect: unknown body node %q (want args | page-param | limit-param | limit | default-limit | as | cache; fail-closed)", c.Name())
	}
	v, err := singleArg(c)
	if err != nil {
		return fmt.Errorf("collect %s: %w", c.Name(), err)
	}
	*target = v
	return nil
}

// applyPollChild dispatches one child node of a poll body onto p; the scalar
// fields (until/every/timeout/as) share one single-arg path keyed by a table.
func applyPollChild(p *Poll, c *kdl.Node) error {
	if c.Name() == "args" {
		for _, a := range c.Children().Nodes {
			v, err := singleArg(a)
			if err != nil {
				return fmt.Errorf("poll args %q: %w", a.Name(), err)
			}
			p.Args = append(p.Args, ArgBind{Name: a.Name(), Value: v})
		}
		return nil
	}
	scalars := map[string]*string{"until": &p.Until, "every": &p.Every, "timeout": &p.Timeout, "as": &p.As}
	target, ok := scalars[c.Name()]
	if !ok {
		if reservedActionKeywords[c.Name()] {
			return fmt.Errorf("poll: %q is reserved for a future version, not implemented in v1 (fail-closed)", c.Name())
		}
		return fmt.Errorf("poll: unknown body node %q (want args | until | every | timeout | as; fail-closed)", c.Name())
	}
	v, err := singleArg(c)
	if err != nil {
		return fmt.Errorf("poll %s: %w (durations quote: `every \"10s\"`)", c.Name(), err)
	}
	*target = v
	return nil
}

// singleArg returns the lone argument of a node, verbatim (significant spaces
// kept), erroring unless there is exactly one.
func singleArg(n *kdl.Node) (string, error) {
	args := n.Arguments()
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects exactly one value, got %d", n.Name(), len(args))
	}
	return args[0].String(), nil
}
