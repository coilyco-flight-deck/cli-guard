// Package opcore is the urfave/cli-free engine core of the spec-driven verb
// design: the per-operation descriptor, the request runtime, and the
// self-guarding Operation.Execute entrypoint. specverb projects this core onto
// a urfave/cli tree; a non-CLI consumer (ward-mcp) drives Operation.Execute
// directly and is still fully gated. See docs/specverb.md and cli-guard#196.
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
	BodyFlags   []Field        // request-body fields promoted to flags
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

// Field is one spec input promoted to a typed CLI flag: a request-body field
// (scalar or array-of-scalar) or a scalar query parameter.
type Field struct {
	Name     string // json field / query param name, doubling as the flag name
	Type     string // swagger type: string|boolean|integer|number|array
	Items    string // scalar element type when Type is "array"
	Required bool   // required in the schema; enforced at request assembly
	Desc     string
}

// TypeLabel renders the flag's type for help and the reference doc.
func (f Field) TypeLabel() string {
	if f.Type == "array" {
		return "[]" + f.Items
	}
	return f.Type
}
