package opcore

import (
	"fmt"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	kdl "github.com/calico32/kdl-go"
)

// ParseInline states the ward-mcp inline grammar as the same []Descriptor the
// OpenAPI source feeds, plus the request RuntimeConfig. See docs/opcore-inline.md.
func ParseInline(src []byte) ([]Descriptor, RuntimeConfig, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return nil, RuntimeConfig{}, fmt.Errorf("opcore: parse KDL: %w", err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return nil, RuntimeConfig{}, fmt.Errorf("opcore: missing top-level `wrap` node")
	}
	p := &inlineParser{}
	for _, a := range wrap.Arguments() {
		p.group = append(p.group, a.String())
	}
	if len(p.group) == 0 {
		return nil, RuntimeConfig{}, fmt.Errorf("opcore: `wrap` needs a command path, e.g. `wrap ward mcp forgejo`")
	}
	for _, n := range wrap.Children().Nodes {
		if aerr := p.applyNode(n); aerr != nil {
			return nil, RuntimeConfig{}, aerr
		}
	}
	if verr := p.validate(); verr != nil {
		return nil, RuntimeConfig{}, verr
	}
	return p.descs, p.cfg, nil
}

// inlineParser accumulates the wrap header, the RuntimeConfig, and the stated
// descriptors as it walks the wrap body.
type inlineParser struct {
	group []string
	cfg   RuntimeConfig
	descs []Descriptor
}

// applyNode dispatches one child of the wrap block, failing closed on anything
// outside the frozen grammar.
func (p *inlineParser) applyNode(n *kdl.Node) error {
	switch n.Name() {
	case "base-url":
		raw, chain, err := guardfile.ParseBaseURL(n)
		if err != nil {
			return err
		}
		// Assign only the form this node carried, so a second base-url node in the
		// other form accumulates into both fields and validate catches the conflict.
		if raw != "" {
			p.cfg.BaseURL = raw
		}
		if !chain.IsZero() {
			p.cfg.BaseURLValue = chain
		}
		return nil
	case "auth":
		a, err := guardfile.ParseAuthNode(n)
		if err != nil {
			return err
		}
		p.cfg.Auth = a
		return nil
	case "restrict":
		r, err := guardfile.ParseRestrictNode(n)
		if err != nil {
			return err
		}
		p.cfg.Restrict = append(p.cfg.Restrict, r)
		return nil
	case "can":
		return p.parseGrant(n)
	default:
		return fmt.Errorf("opcore: unknown node %q in wrap body (want base-url | auth | restrict | can; fail-closed)", n.Name())
	}
}

// validate enforces the cross-node invariants after every wrap child is applied.
func (p *inlineParser) validate() error {
	if p.cfg.Auth.Scheme == "" {
		return fmt.Errorf("opcore: `auth` block is required")
	}
	if p.cfg.BaseURL != "" && !p.cfg.BaseURLValue.IsZero() {
		return fmt.Errorf("opcore: base-url set both as a string and a `{ value }` block; pick one")
	}
	if len(p.descs) == 0 {
		return fmt.Errorf("opcore: no `can` operations (nothing to mount)")
	}
	return nil
}

// parseGrant states one `can <verb> <resource> { ... }` operation as a Descriptor:
// method from the verb, path params from the `{template}`, children to the shape.
func (p *inlineParser) parseGrant(n *kdl.Node) error {
	args := n.Arguments()
	if len(args) != 2 {
		return fmt.Errorf("opcore: `can` needs a verb and a resource, e.g. `can create issue { ... }` (got %d arg(s))", len(args))
	}
	verb, resource := args[0].String(), args[1].String()
	if verb == "" || resource == "" {
		return fmt.Errorf("opcore: `can` needs a non-empty verb and resource")
	}
	method, _ := MethodForVerb(verb)
	d := Descriptor{
		VerbName:    strings.Join(p.group, ".") + "." + resource + "." + verb,
		Group:       resource,
		Leaf:        verb,
		Method:      method,
		Destructive: DestructiveVerb(verb),
		Grant:       "can " + verb + " " + resource,
	}
	for _, c := range n.Children().Nodes {
		if err := applyInlineGrantChild(&d, c); err != nil {
			return fmt.Errorf("opcore: can %s %s: %w", verb, resource, err)
		}
	}
	if d.Path == "" {
		return fmt.Errorf("opcore: can %s %s: `path \"...\"` is required (the request path template)", verb, resource)
	}
	d.PathParams = PathParamsInOrder(d.Path)
	if len(d.FixedBody) > 0 {
		// a state toggle owns its body: no body flags mount alongside a `set`.
		d.BodyFlags = nil
	}
	if err := CheckFlagCollisions(d); err != nil {
		return err
	}
	p.descs = append(p.descs, d)
	return nil
}

// applyInlineGrantChild dispatches one child of a `can` operation body onto d,
// failing closed on anything outside path | query | body | set.
func applyInlineGrantChild(d *Descriptor, c *kdl.Node) error {
	switch c.Name() {
	case "path":
		if d.Path != "" {
			return fmt.Errorf("duplicate `path` (fail-closed)")
		}
		v, err := singleInlineArg(c, "path")
		if err != nil {
			return err
		}
		d.Path = v
	case "query":
		fields, err := inlineFields(c, "query")
		if err != nil {
			return err
		}
		d.QueryFlags = append(d.QueryFlags, fields...)
	case "body":
		fields, err := inlineFields(c, "body")
		if err != nil {
			return err
		}
		d.BodyFlags = append(d.BodyFlags, fields...)
	case "set":
		return applyInlineSet(d, c)
	default:
		return fmt.Errorf("unknown node %q (want path | query | body | set; fail-closed)", c.Name())
	}
	return nil
}

// applyInlineSet reads `set k=v...` into FixedBody, keeping each value's
// KDL-native type (a boolean stays a boolean) via RawValue.
func applyInlineSet(d *Descriptor, c *kdl.Node) error {
	props := c.Properties()
	if len(props) == 0 {
		return fmt.Errorf("`set` needs at least one key=value (e.g. `set state=\"closed\"`)")
	}
	if d.FixedBody == nil {
		d.FixedBody = map[string]any{}
	}
	for k, val := range props {
		d.FixedBody[k] = val.RawValue()
	}
	return nil
}

// inlineFields reads a `query`/`body` node's flat string arguments into Fields.
// An inline field carries no schema, so it types as a plain string.
func inlineFields(c *kdl.Node, kind string) ([]Field, error) {
	if children := c.Children(); children != nil && len(children.Nodes) > 0 {
		return nil, fmt.Errorf("`%s` takes flat field names, not a block (fail-closed)", kind)
	}
	args := c.Arguments()
	if len(args) == 0 {
		return nil, fmt.Errorf("`%s` needs at least one field name (e.g. `%s \"state\"`)", kind, kind)
	}
	out := make([]Field, 0, len(args))
	for _, a := range args {
		name := a.String()
		if name == "" {
			return nil, fmt.Errorf("`%s` field name is empty (fail-closed)", kind)
		}
		out = append(out, Field{Name: name, Type: "string"})
	}
	return out, nil
}

// singleInlineArg reads the lone string argument of an inline grant child.
func singleInlineArg(c *kdl.Node, kind string) (string, error) {
	args := c.Arguments()
	if len(args) != 1 || args[0].String() == "" {
		return "", fmt.Errorf("`%s` expects exactly one non-empty value", kind)
	}
	return args[0].String(), nil
}

// PathParamsInOrder returns the `{...}` names in path-template order, the order
// the leaf takes them positionally. Shared with the CLI projection's binder.
func PathParamsInOrder(path string) []string {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}
