// Package guardfile parses a KDL Guardfile, the human authoring layer (L2) of
// the specverb engine, into a typed model. KDL is parsed, never evaluated.
package guardfile

import (
	"fmt"

	kdl "github.com/calico32/kdl-go"
)

// Auth describes how the engine authenticates to the target API. Three schemes:
// header-token, bearer, query-param (dual-secret). See docs/specverb.md.
type Auth struct {
	Scheme string
	Header string
	Prefix string // trailing space is significant, e.g. "token "
	SSM    string

	// Params are the query-param scheme's ordered secrets, each injected as a
	// query parameter (Trello's ?key=&token=). Empty for the header schemes.
	Params []QueryAuthParam
}

// QueryAuthParam is one secret of the query-param scheme: a query parameter Name
// whose value is the secret read from SSM.
type QueryAuthParam struct {
	Name string
	SSM  string
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

	// Op is the spec operationId this grant authorizes (grant-body `op "..."`).
	// Required on a `can`; ignored on a deny, which names a verb+resource class.
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
}

// Action is a named composite verb: EITHER a Poll (watch one leaf) or an ordered
// Calls sequence (chain leaves), plus an optional FailWhen exit. See specverb-actions.md.
type Action struct {
	Name     string
	Describe string
	Inputs   []Input
	Poll     *Poll
	Calls    []Call // ordered multi-call sequence; mutually exclusive with Poll
	FailWhen string // JMESPath over the bindings; truthy => non-zero exit
}

// Guardfile is the parsed form of one wrap block.
type Guardfile struct {
	Group    []string // command path, e.g. ["ward", "ops", "forgejo"]
	Spec     string
	BaseURL  string
	Auth     Auth
	Grants   []Grant
	Restrict []Restriction
	Actions  []Action
}

// modals is the closed set of grant verbs; anything else fails closed.
var modals = map[string]bool{"can": true, "cannot": true, "never": true}

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
		if err := gf.applyNode(n); err != nil {
			return nil, err
		}
	}
	return gf, gf.validate()
}

// applyNode dispatches one child of the wrap block onto gf.
func (gf *Guardfile) applyNode(n *kdl.Node) error {
	name := n.Name()
	if modals[name] {
		g, err := parseGrant(n)
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
		v, err := singleArg(n)
		gf.BaseURL = v
		return err
	case "auth":
		a, err := parseAuth(n)
		gf.Auth = a
		return err
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
	default:
		if reservedActionKeywords[name] {
			return fmt.Errorf("guardfile: %q is reserved for a future version and is not implemented in v1 (fail-closed)", name)
		}
		return fmt.Errorf("guardfile: unknown node %q in wrap body (fail-closed)", name)
	}
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
	return nil
}

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

// parseHeaderTokenAuth reads `header-token { header H; prefix "..."; ssm S }`.
func parseHeaderTokenAuth(n *kdl.Node) (Auth, error) {
	a := Auth{Scheme: "header-token"}
	for _, c := range n.Children().Nodes {
		v, ferr := singleArg(c)
		if ferr != nil {
			return Auth{}, fmt.Errorf("guardfile: auth %s: %w", c.Name(), ferr)
		}
		switch c.Name() {
		case "header":
			a.Header = v
		case "prefix":
			a.Prefix = v
		case "ssm":
			a.SSM = v
		default:
			return Auth{}, fmt.Errorf("guardfile: auth: unknown field %q (fail-closed)", c.Name())
		}
	}
	if a.Header == "" || a.SSM == "" {
		return Auth{}, fmt.Errorf("guardfile: auth header-token requires `header` and `ssm`")
	}
	return a, nil
}

// parseBearerAuth reads `bearer { ssm S }`: shorthand for the Authorization
// header with a "Bearer " prefix (Tailscale).
func parseBearerAuth(n *kdl.Node) (Auth, error) {
	a := Auth{Scheme: "bearer", Header: "Authorization", Prefix: "Bearer "}
	for _, c := range n.Children().Nodes {
		v, ferr := singleArg(c)
		if ferr != nil {
			return Auth{}, fmt.Errorf("guardfile: auth %s: %w", c.Name(), ferr)
		}
		if c.Name() != "ssm" {
			return Auth{}, fmt.Errorf("guardfile: auth bearer: unknown field %q (want ssm; fail-closed)", c.Name())
		}
		a.SSM = v
	}
	if a.SSM == "" {
		return Auth{}, fmt.Errorf("guardfile: auth bearer requires `ssm`")
	}
	return a, nil
}

// parseQueryParamAuth reads `query-param { param <name> { ssm S } ... }`: one or
// more secrets injected as query parameters (Trello's ?key=&token=).
func parseQueryParamAuth(n *kdl.Node) (Auth, error) {
	a := Auth{Scheme: "query-param"}
	for _, c := range n.Children().Nodes {
		if c.Name() != "param" {
			return Auth{}, fmt.Errorf("guardfile: auth query-param: unknown field %q (want param; fail-closed)", c.Name())
		}
		name, err := singleArg(c)
		if err != nil {
			return Auth{}, fmt.Errorf("guardfile: auth query-param: %w (name it: `param key { ssm \"...\" }`)", err)
		}
		p := QueryAuthParam{Name: name}
		for _, cc := range c.Children().Nodes {
			v, ferr := singleArg(cc)
			if ferr != nil {
				return Auth{}, fmt.Errorf("guardfile: auth query-param %s: %w", name, ferr)
			}
			if cc.Name() != "ssm" {
				return Auth{}, fmt.Errorf("guardfile: auth query-param %s: unknown field %q (want ssm)", name, cc.Name())
			}
			p.SSM = v
		}
		if p.SSM == "" {
			return Auth{}, fmt.Errorf("guardfile: auth query-param %q requires `ssm`", name)
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
	if g.Modal == "can" && g.Op == "" {
		return Grant{}, fmt.Errorf("guardfile: `can %s %s` needs an `op \"<operationId>\"` binding (the engine carries no expansion table)", g.Verb, g.Resource)
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

// parseAction reads one `action <name> { ... }` block into an Action. It fails
// closed: an unknown body node, a missing poll, or a reserved keyword is an error.
func parseAction(n *kdl.Node) (Action, error) {
	name, err := singleArg(n)
	if err != nil {
		return Action{}, fmt.Errorf("guardfile: action: %w (name it: `action ci-watch { ... }`)", err)
	}
	act := Action{Name: name}
	for _, c := range n.Children().Nodes {
		if err := applyActionChild(&act, c); err != nil {
			return Action{}, fmt.Errorf("guardfile: action %q: %w", name, err)
		}
	}
	switch {
	case act.Poll == nil && len(act.Calls) == 0:
		return Action{}, fmt.Errorf("guardfile: action %q: needs a `poll` block or at least one `call` step", name)
	case act.Poll != nil && len(act.Calls) > 0:
		return Action{}, fmt.Errorf("guardfile: action %q: `poll` and `call` are mutually exclusive (watch one leaf, or chain leaves)", name)
	}
	return act, nil
}

// applyActionChild dispatches one child node of an action body onto act.
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
	case "poll":
		return addPoll(act, c)
	case "call":
		call, err := parseCall(c)
		if err != nil {
			return err
		}
		act.Calls = append(act.Calls, call)
		return nil
	case "fail-when":
		v, err := singleArg(c)
		if err != nil {
			return fmt.Errorf("fail-when: %w", err)
		}
		act.FailWhen = v
		return nil
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

// parseInput reads one `input <name> { positional|flag; required; help "..." }`
// child. Exactly one of positional/flag must be declared.
func parseInput(n *kdl.Node) (Input, error) {
	name, err := singleArg(n)
	if err != nil {
		return Input{}, fmt.Errorf("input: %w (name it: `input repo { positional }`)", err)
	}
	in := Input{Name: name}
	kindSet := false
	for _, c := range n.Children().Nodes {
		switch c.Name() {
		case "positional":
			in.Positional, kindSet = true, true
		case "flag":
			in.Positional, kindSet = false, true
		case "required":
			in.Required = true
		case "help":
			v, herr := singleArg(c)
			if herr != nil {
				return Input{}, fmt.Errorf("input %q: %w", name, herr)
			}
			in.Help = v
		default:
			return Input{}, fmt.Errorf("input %q: unknown field %q (want positional | flag | required | help; fail-closed)", name, c.Name())
		}
	}
	if !kindSet {
		return Input{}, fmt.Errorf("input %q: declare exactly one of `positional` or `flag`", name)
	}
	return in, nil
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
			for _, a := range c.Children().Nodes {
				v, err := singleArg(a)
				if err != nil {
					return Call{}, fmt.Errorf("call args %q: %w", a.Name(), err)
				}
				cl.Args = append(cl.Args, ArgBind{Name: a.Name(), Value: v})
			}
		case "as":
			v, err := singleArg(c)
			if err != nil {
				return Call{}, fmt.Errorf("call as: %w", err)
			}
			cl.As = v
		default:
			if reservedActionKeywords[c.Name()] {
				return Call{}, fmt.Errorf("call: %q is reserved for a future version (fail-closed)", c.Name())
			}
			return Call{}, fmt.Errorf("call: unknown body node %q (want args | as; fail-closed)", c.Name())
		}
	}
	return cl, nil
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
