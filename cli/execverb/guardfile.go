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
	Grants     []Grant
}

// Grant is one `can run <subcommand>` sentence plus its flag policy.
type Grant struct {
	Subcommand []string // e.g. ["admin", "user", "list"]
	AllowFlags []string // non-empty -> strict flag allowlist
	DenyFlags  []string // default-allow minus these
	Describe   string
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
		if c.Name() != "argv-prefix" {
			return fmt.Errorf("execverb: exec body: unknown node %q (fail-closed)", c.Name())
		}
		for _, a := range c.Arguments() {
			gf.ArgvPrefix = append(gf.ArgvPrefix, a.String())
		}
	}
	return nil
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
	for _, c := range n.Children().Nodes {
		if err := g.applyPolicyNode(c); err != nil {
			return Grant{}, err
		}
	}
	return g, nil
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
