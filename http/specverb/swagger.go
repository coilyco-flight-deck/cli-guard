// Minimal Swagger 2.0 reader for M0: reads only what the proving ops need.
// Full kin-openapi 2.0 -> 3.x conversion is M1. See docs/specverb.md.

package specverb

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// swaggerSpec is the subset of a Swagger 2.0 document the engine reads.
type swaggerSpec struct {
	Swagger     string                          `json:"swagger"`
	Paths       map[string]map[string]swaggerOp `json:"paths"`
	Definitions map[string]swaggerSchema        `json:"definitions"`
}

// swaggerOp is one operation under a path+method.
type swaggerOp struct {
	OperationID string         `json:"operationId"`
	Parameters  []swaggerParam `json:"parameters"`
}

// swaggerParam is one parameter; `in: body` carries Schema, others carry Type.
type swaggerParam struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Required    bool           `json:"required"`
	Type        string         `json:"type"`
	Schema      *swaggerSchema `json:"schema"`
	Description string         `json:"description"`
}

// swaggerSchema is a $ref or an inline object whose properties are scalars or
// arrays of scalars. Nested objects are out of scope.
type swaggerSchema struct {
	Ref         string                   `json:"$ref"`
	Type        string                   `json:"type"`
	Required    []string                 `json:"required"`
	Properties  map[string]swaggerSchema `json:"properties"`
	Description string                   `json:"description"`
	Items       *swaggerSchema           `json:"items"`
}

// parseSwagger reads a Swagger 2.0 document, failing closed on anything that is
// not the 2.0 shape the M0 reader understands.
func parseSwagger(raw []byte) (*swaggerSpec, error) {
	var s swaggerSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("specverb: parse swagger json: %w", err)
	}
	if !strings.HasPrefix(s.Swagger, "2.") {
		return nil, fmt.Errorf("specverb: expected swagger 2.0, got %q (openapi 3.x conversion is M1)", s.Swagger)
	}
	if len(s.Paths) == 0 {
		return nil, fmt.Errorf("specverb: spec has no paths")
	}
	return &s, nil
}

// pathParamRe pulls `{name}` tokens out of a path template in declaration order.
var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// findOp locates the operation with the given operationId, failing closed when
// it is absent (an undeclared op can never be mounted).
func (s *swaggerSpec) findOp(operationID string) (method, path string, op swaggerOp, err error) {
	for p, methods := range s.Paths {
		for m, o := range methods {
			if o.OperationID == operationID {
				return strings.ToUpper(m), p, o, nil
			}
		}
	}
	return "", "", swaggerOp{}, fmt.Errorf("specverb: operationId %q not found in spec (fail-closed)", operationID)
}

// pathParamsInOrder returns the `{...}` names in path-template order, which is
// the order the CLI takes them positionally.
func pathParamsInOrder(path string) []string {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// queryParamsOf returns the operation's scalar `in: query` parameters, the
// ones that lower to a typed CLI flag; array query params are skipped.
func queryParamsOf(op swaggerOp) []swaggerParam {
	var out []swaggerParam
	for _, p := range op.Parameters {
		if p.In != "query" || !isScalar(p.Type) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// bodySchema resolves an operation's request-body schema. ok is false when the
// op takes no body (a normal case); a $ref resolves one hop into definitions.
func (s *swaggerSpec) bodySchema(op swaggerOp) (schema *swaggerSchema, ok bool, err error) {
	for _, p := range op.Parameters {
		if p.In != "body" || p.Schema == nil {
			continue
		}
		if p.Schema.Ref == "" {
			return p.Schema, true, nil
		}
		name := strings.TrimPrefix(p.Schema.Ref, "#/definitions/")
		def, found := s.Definitions[name]
		if !found {
			return nil, false, fmt.Errorf("specverb: body $ref %q not found in definitions", p.Schema.Ref)
		}
		return &def, true, nil
	}
	return nil, false, nil
}
