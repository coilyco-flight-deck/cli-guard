package opcore

import (
	"encoding/json"
	"sort"
)

// Schema is the transport-neutral input surface of one Descriptor: every input
// it accepts, keyed by name, plus the subset that must be supplied. See docs.
type Schema struct {
	Properties map[string]Property // keyed by field name
	Required   []string            // names that must be supplied, in a stable order
}

// Property is one input in a Schema: its type (or array element type), the
// neutral where-it-goes hint, and its help text.
type Property struct {
	Type        string // string|boolean|integer|number|array
	Items       string // element type when Type==array
	Location    string // path|query|body|form (neutral hint)
	Description string
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
	s := Schema{Properties: map[string]Property{}}
	for _, name := range d.PathParams {
		s.Properties[name] = Property{Type: "string", Location: LocationPath}
		s.Required = append(s.Required, name)
	}
	add := func(fields []Field, loc string) {
		for _, f := range fields {
			s.Properties[f.Name] = Property{
				Type:        f.Type,
				Items:       f.Items,
				Location:    loc,
				Description: f.Desc,
			}
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

// JSONSchema emits the Schema as a generic draft-07 object, never an MCP tool
// type (that wrapper lives in ward-mcp). The neutral Location hint is omitted.
func (s Schema) JSONSchema() []byte {
	props := map[string]any{}
	for name, p := range s.Properties {
		entry := map[string]any{"type": p.Type}
		if p.Description != "" {
			entry["description"] = p.Description
		}
		if p.Type == "array" {
			items := p.Items
			if items == "" {
				items = "string"
			}
			entry["items"] = map[string]any{"type": items}
		}
		props[name] = entry
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
	out, _ := json.MarshalIndent(doc, "", "  ")
	return out
}
