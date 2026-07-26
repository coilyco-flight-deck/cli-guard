package opcore

import (
	"encoding/json"
	"sort"
)

// Schema is the transport-neutral input surface of one Descriptor: every input
// it accepts, keyed by name, plus the subset that must be supplied. See docs.
type Schema struct {
	Properties        map[string]Property // keyed by field name
	Required          []string            // names that must be supplied, in a stable order
	MutuallyExclusive [][]string          // groups where at most one property may be supplied
}

// Property is one input in a Schema: its type (or array element type), the
// neutral where-it-goes hint, its help text, and any nested shape.
type Property struct {
	Type         string // string|boolean|integer|number|array
	Items        string // element type when Type==array
	Item         *Property
	Properties   map[string]Property
	Required     []string
	Raw          bool
	Location     string // path|query|body|form (neutral hint)
	UpstreamName string // outgoing query parameter when it differs from the local property name
	Description  string
	Minimum      *float64
	Maximum      *float64
	MinItems     *int
	MaxItems     *int
}

// Location constants label where a Property lowers onto the outgoing request.
const (
	LocationPath  = "path"
	LocationQuery = "query"
	LocationBody  = "body"
	LocationForm  = "form"
)

// InputSchema projects a Descriptor onto its neutral input surface: path params
// as required strings, query/body/form as fieldFlagsToCLI reads them, no FixedBody.
func (d Descriptor) InputSchema() Schema {
	s := Schema{
		Properties:        map[string]Property{},
		MutuallyExclusive: cloneStringGroups(d.QueryExclusive),
	}
	for _, name := range d.PathParams {
		s.Properties[name] = Property{Type: "string", Location: LocationPath}
		s.Required = append(s.Required, name)
	}
	add := func(fields []Field, loc string) {
		for _, f := range fields {
			s.Properties[f.Name] = f.toProperty(loc)
			if f.Required {
				s.Required = append(s.Required, f.Name)
			}
		}
	}
	add(d.QueryFlags, LocationQuery)
	add(d.BodyFlags, LocationBody)
	add(d.FormFlags, LocationForm)
	return s
}

// toProperty lowers one Field into the neutral Schema tree.
func (f Field) toProperty(loc string) Property {
	p := Property{
		Type:         f.Type,
		Items:        f.Items,
		Location:     loc,
		UpstreamName: f.UpstreamName,
		Description:  f.Desc,
		Raw:          f.Raw,
		Minimum:      f.Minimum,
		Maximum:      f.Maximum,
		MinItems:     f.MinItems,
		MaxItems:     f.MaxItems,
	}
	if f.Item != nil {
		item := f.Item.toProperty("")
		p.Item = &item
	}
	if len(f.Fields) > 0 {
		p.Properties = map[string]Property{}
		for _, child := range f.Fields {
			p.Properties[child.Name] = child.toProperty("")
			if child.Required {
				p.Required = append(p.Required, child.Name)
			}
		}
	}
	return p
}

// JSONSchema emits the Schema as a generic draft-07 object, never an MCP tool
// type (that wrapper lives in ward-mcp). The neutral Location hint is omitted.
func (s Schema) JSONSchema() []byte {
	props := map[string]any{}
	for name, p := range s.Properties {
		props[name] = p.jsonSchema()
	}
	doc := map[string]any{
		"$schema":    "http://json-schema.org/draft-07/schema#",
		"type":       "object",
		"properties": props,
	}
	if len(s.Required) > 0 {
		required := append([]string(nil), s.Required...)
		sort.Strings(required)
		doc["required"] = required
	}
	if exclusions := mutuallyExclusiveSchema(s.MutuallyExclusive); len(exclusions) > 0 {
		doc["allOf"] = exclusions
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return out
}

// mutuallyExclusiveSchema lowers each at-most-one group to standard draft-07
// pairwise `not required` constraints.
func mutuallyExclusiveSchema(groups [][]string) []any {
	var out []any
	for _, group := range groups {
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				out = append(out, map[string]any{
					"not": map[string]any{
						"required": []string{group[i], group[j]},
					},
				})
			}
		}
	}
	return out
}

func cloneStringGroups(groups [][]string) [][]string {
	if len(groups) == 0 {
		return nil
	}
	out := make([][]string, len(groups))
	for i, group := range groups {
		out[i] = append([]string(nil), group...)
	}
	return out
}

// jsonSchema emits one Property as a draft-07 fragment.
func (p Property) jsonSchema() map[string]any {
	entry := p.schemaEntry()
	switch p.Type {
	case "array":
		switch {
		case p.Item != nil:
			entry["items"] = p.Item.jsonSchema()
		case p.Raw:
			// raw arrays are open-ended subtrees, so the schema leaves items
			// unconstrained instead of defaulting to string.
		default:
			items := p.Items
			if items == "" {
				items = "string"
			}
			entry["items"] = map[string]any{"type": items}
		}
	case "object":
		if len(p.Properties) > 0 {
			props := map[string]any{}
			for name, child := range p.Properties {
				props[name] = child.jsonSchema()
			}
			entry["properties"] = props
			if len(p.Required) > 0 {
				required := append([]string(nil), p.Required...)
				sort.Strings(required)
				entry["required"] = required
			}
		}
	}
	return entry
}

// schemaEntry emits the common scalar metadata before jsonSchema adds any
// nested object or array shape.
func (p Property) schemaEntry() map[string]any {
	entry := map[string]any{"type": p.Type}
	if p.Description != "" {
		entry["description"] = p.Description
	}
	if p.Raw {
		entry["x-opcore-raw"] = true
	}
	if p.UpstreamName != "" {
		entry["x-opcore-upstream-name"] = p.UpstreamName
	}
	if p.Minimum != nil {
		entry["minimum"] = *p.Minimum
	}
	if p.Maximum != nil {
		entry["maximum"] = *p.Maximum
	}
	if p.MinItems != nil {
		entry["minItems"] = *p.MinItems
	}
	if p.MaxItems != nil {
		entry["maxItems"] = *p.MaxItems
	}
	return entry
}
