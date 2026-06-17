// OpenAPI 3.x reader: parses a 3.0 or 3.1 document (JSON or YAML) via
// kin-openapi and adapts it into the engine's internal model. See docs/specverb.md.

package specverb

import (
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// jsonMediaTypes are the request-body content types the reader prefers, in
// order; the first present one supplies the body schema (Tailscale ACL: hujson).
var jsonMediaTypes = []string{"application/json", "application/hujson", "application/json5"}

// sniffOpenAPI3 reports whether raw loads as an OpenAPI 3.x document, the YAML
// fallback path when JSON version-sniffing did not settle the version.
func sniffOpenAPI3(raw []byte) (is3 bool, ok bool) {
	doc, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil || doc == nil {
		return false, false
	}
	return strings.HasPrefix(doc.OpenAPI, "3."), true
}

// parseOpenAPI3 loads an OpenAPI 3.x document and adapts it into a swaggerSpec.
// kin-openapi dereferences internal $refs as it loads. See docs/specverb.md.
func parseOpenAPI3(raw []byte) (*swaggerSpec, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(raw)
	if err != nil {
		return nil, fmt.Errorf("specverb: parse openapi 3.x: %w", err)
	}
	if doc.Paths == nil || doc.Paths.Len() == 0 {
		return nil, fmt.Errorf("specverb: spec has no paths")
	}
	spec := &swaggerSpec{Swagger: doc.OpenAPI, Paths: map[string]map[string]swaggerOp{}}
	for path, item := range doc.Paths.Map() {
		methods := map[string]swaggerOp{}
		for method, op := range item.Operations() {
			methods[strings.ToLower(method)] = adaptOperation(item, op)
		}
		spec.Paths[path] = methods
	}
	return spec, nil
}

// adaptOperation lowers one OpenAPI 3.x operation into a swaggerOp: path-item
// and operation parameters, plus the request body flattened to an `in: body` param.
func adaptOperation(item *openapi3.PathItem, op *openapi3.Operation) swaggerOp {
	sop := swaggerOp{OperationID: op.OperationID}
	for _, ref := range append(append(openapi3.Parameters{}, item.Parameters...), op.Parameters...) {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		sop.Parameters = append(sop.Parameters, swaggerParam{
			Name:        p.Name,
			In:          p.In,
			Required:    p.Required,
			Type:        schemaRefType(p.Schema),
			Description: p.Description,
		})
	}
	if body := requestBodyParam(op); body != nil {
		sop.Parameters = append(sop.Parameters, *body)
	}
	return sop
}

// requestBodyParam flattens an operation's requestBody into an `in: body` param
// carrying the inline-converted schema; nil when the op takes no JSON body.
func requestBodyParam(op *openapi3.Operation) *swaggerParam {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	content := op.RequestBody.Value.Content
	for _, mt := range jsonMediaTypes {
		if media, ok := content[mt]; ok && media.Schema != nil && media.Schema.Value != nil {
			return &swaggerParam{In: "body", Required: op.RequestBody.Value.Required, Schema: convertSchema(media.Schema.Value)}
		}
	}
	return nil
}

// convertSchema lowers a resolved openapi3.Schema into the engine's swaggerSchema,
// one level deep: object properties (scalar or array-of-scalar) and array items.
func convertSchema(s *openapi3.Schema) *swaggerSchema {
	if s == nil {
		return nil
	}
	out := &swaggerSchema{Type: schemaType(s), Description: s.Description, Required: s.Required}
	if out.Type == "array" && s.Items != nil && s.Items.Value != nil {
		out.Items = &swaggerSchema{Type: schemaType(s.Items.Value)}
	}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]swaggerSchema, len(s.Properties))
		for name, ref := range s.Properties {
			if ref == nil || ref.Value == nil {
				continue
			}
			child := convertSchema(ref.Value)
			out.Properties[name] = *child
		}
	}
	return out
}

// schemaRefType returns the scalar type of a parameter's schema, "string" when
// the schema is absent so a bare param still lowers to a usable flag.
func schemaRefType(ref *openapi3.SchemaRef) string {
	if ref == nil || ref.Value == nil {
		return "string"
	}
	return schemaType(ref.Value)
}

// schemaType reads the first non-null type from a schema, collapsing the OpenAPI
// 3.1 type-list (e.g. ["string","null"]) back to the single type the engine models.
func schemaType(s *openapi3.Schema) string {
	if s == nil || s.Type == nil {
		return ""
	}
	for _, t := range s.Type.Slice() {
		if t != "null" {
			return t
		}
	}
	return ""
}
