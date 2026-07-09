// Package opcore is the urfave/cli-free engine core of the spec-driven verb
// design: the per-operation descriptor, the request runtime, and the
// self-guarding Operation.Execute entrypoint. specverb projects this core onto
// a urfave/cli tree; a non-CLI consumer (ward-mcp) drives Operation.Execute
// directly and is still fully gated. See docs/specverb.md for the projection shape.
package opcore

// Descriptor is the tiny per-operation payload the generic action binds to,
// isolated from the static request machinery.
type Descriptor struct {
	VerbName    string         // dotted audit name, e.g. ward.ops.forgejo.repo.create
	Group       string         // CLI group noun, e.g. repo
	Leaf        string         // CLI leaf verb, e.g. create
	Method      string         // HTTP method
	Path        string         // path template, e.g. /repos/{owner}/{repo}
	PathParams  []string       // ordered positional args drawn from the path
	BodyFlags   []Field        // request-body fields, including nested object/array shapes
	QueryFlags  []Field        // scalar query params promoted to flags
	FormFlags   []Field        // formData params; "file" types take a path
	FixedBody   map[string]any // exact body for state-toggle leaves; no body flags mount
	Destructive bool           // leaf mutates irreversibly (delete)
	Grant       string         // the authorizing grant sentence, e.g. "can delete repos"
	Describe    string         // optional Guardfile describe "..." note, "" if none
}

// Label names the leaf in operator-facing errors, satisfying stepflow.Leaf so a
// resolved Descriptor can drive a complex-action step. See pkg/stepflow.
func (d Descriptor) Label() string { return d.Leaf }

// Field is one spec input. The flat cases lower to CLI flags, while a body
// field may also carry nested object/array shape for neutral schema emission.
type Field struct {
	Name     string // json field / query param name, doubling as the flag name
	Type     string // swagger type: string|boolean|integer|number|array|object
	Items    string // scalar element type when Type is "array" and Item is nil
	Item     *Field // nested element schema when Type is "array" and Item is object/array
	Fields   []Field
	Raw      bool
	Required bool // required in the schema; enforced at request assembly
	Desc     string
}

// TypeLabel renders the flag's type for help and the reference doc.
func (f Field) TypeLabel() string {
	if f.Type == "array" {
		if f.Item != nil {
			return "[]" + f.Item.TypeLabel()
		}
		if f.Items != "" {
			return "[]" + f.Items
		}
		return "[]string"
	}
	return f.Type
}
