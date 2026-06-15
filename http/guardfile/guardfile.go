// Package guardfile parses a KDL Guardfile, the human authoring layer (L2) of
// the specverb engine, into a typed model. KDL is parsed, never evaluated.
package guardfile

import (
	"fmt"

	kdl "github.com/calico32/kdl-go"
)

// Auth describes how the engine authenticates to the target API. Only the
// header-token scheme (Forgejo's "Authorization: token <key>") is modeled.
type Auth struct {
	Scheme string
	Header string
	Prefix string // trailing space is significant, e.g. "token "
	SSM    string
}

// Grant is one policy sentence: modal verb resource [qualifiers...] [key=value...].
// "can delete repos org=acme" -> {can, delete, repos, Props{org: acme}}.
type Grant struct {
	Modal      string
	Verb       string
	Resource   string
	Qualifiers []string

	// Props are KDL key=value node properties: structured scoping constraints
	// like `org=acme`. Positional bareword qualifiers stay in Qualifiers.
	Props map[string]string

	// Describe is the optional grant-body `describe "..."` note that enriches
	// the thin upstream spec; it flows into help and the describe verb.
	Describe string
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

// Action is a named composite verb inside a wrap block: sugar over the
// allowlist. v1 carries one Poll plus an optional FailWhen exit predicate.
type Action struct {
	Name     string
	Describe string
	Inputs   []Input
	Poll     *Poll
	FailWhen string // JMESPath over the bindings; truthy => non-zero exit
}

// Guardfile is the parsed form of one wrap block.
type Guardfile struct {
	Group   []string // command path, e.g. ["ward", "ops", "forgejo"]
	Spec    string
	BaseURL string
	Auth    Auth
	Grants  []Grant
	Actions []Action
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
	"call": true, "read": true, // non-poll leaf calls (the leaf seam)
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

// parseAuth reads the auth header block; each field is a single-arg child node.
func parseAuth(n *kdl.Node) (Auth, error) {
	scheme, err := singleArg(n)
	if err != nil {
		return Auth{}, fmt.Errorf("guardfile: auth: %w", err)
	}
	if scheme != "header-token" {
		return Auth{}, fmt.Errorf("guardfile: auth scheme %q unsupported (only header-token)", scheme)
	}
	a := Auth{Scheme: scheme}
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
		switch c.Name() {
		case "describe":
			v, err := singleArg(c)
			if err != nil {
				return Grant{}, fmt.Errorf("guardfile: grant %q: %w", n.Name(), err)
			}
			g.Describe = v
		default:
			return Grant{}, fmt.Errorf("guardfile: grant body: unknown node %q (only `describe` is allowed; fail-closed)", c.Name())
		}
	}
	return g, nil
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
	if act.Poll == nil {
		return Action{}, fmt.Errorf("guardfile: action %q: a `poll` block is required in v1", name)
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
		if act.Poll != nil {
			return fmt.Errorf("v1 allows exactly one `poll` per action")
		}
		p, err := parsePoll(c)
		if err != nil {
			return err
		}
		act.Poll = &p
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
