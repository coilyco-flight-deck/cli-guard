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

// Grant is one policy sentence: modal verb resource [qualifiers...].
// "can delete labels created-by-me" -> {can, delete, labels, [created-by-me]}.
type Grant struct {
	Modal      string
	Verb       string
	Resource   string
	Qualifiers []string

	// Describe is the optional grant-body `describe "..."` note that enriches
	// the thin upstream spec; it flows into help and the describe verb.
	Describe string
}

// Guardfile is the parsed form of one wrap block.
type Guardfile struct {
	Group   []string // command path, e.g. ["ward", "ops", "forgejo"]
	Spec    string
	BaseURL string
	Auth    Auth
	Grants  []Grant
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
	default:
		return fmt.Errorf("guardfile: unknown node %q in wrap body (fail-closed)", name)
	}
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

// singleArg returns the lone argument of a node, verbatim (significant spaces
// kept), erroring unless there is exactly one.
func singleArg(n *kdl.Node) (string, error) {
	args := n.Arguments()
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects exactly one value, got %d", n.Name(), len(args))
	}
	return args[0].String(), nil
}
