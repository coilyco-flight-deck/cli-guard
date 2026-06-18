// Spec ingestion: version detection and the Swagger 2.0 reader. A 2.0 document is
// upgraded to the engine's OpenAPI 3.x IR (openapi3.go) via kin-openapi's
// openapi2conv, so the engine binds to one resolved model. See docs/specverb.md.

package specverb

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
)

// parseSwagger reads either a Swagger 2.0 or an OpenAPI 3.x document into the
// engine IR, dispatching on the declared version. Fails closed on neither.
func parseSwagger(raw []byte) (*spec, error) {
	version, err := detectSpecVersion(raw)
	if err != nil {
		return nil, err
	}
	if version == 3 {
		return parseOpenAPI3(raw)
	}
	return parseSwagger2(raw)
}

// detectSpecVersion sniffs the document version: 2 for `swagger: "2.x"`, 3 for
// `openapi: "3.x"`. YAML (3.x only here) routes through the openapi3 loader.
func detectSpecVersion(raw []byte) (int, error) {
	var probe struct {
		Swagger string `json:"swagger"`
		OpenAPI string `json:"openapi"`
	}
	if json.Unmarshal(raw, &probe) == nil {
		if strings.HasPrefix(probe.Swagger, "2.") {
			return 2, nil
		}
		if strings.HasPrefix(probe.OpenAPI, "3.") {
			return 3, nil
		}
	}
	if v, ok := sniffOpenAPI3(raw); ok && v {
		return 3, nil
	}
	return 0, fmt.Errorf("specverb: unrecognized spec version (want swagger 2.x or openapi 3.x)")
}

// parseSwagger2 reads a Swagger 2.0 document and upgrades it to the OpenAPI 3.x IR
// via openapi2conv (body -> requestBody, formData -> multipart). Fails closed off-2.0.
func parseSwagger2(raw []byte) (*spec, error) {
	var doc2 openapi2.T
	if err := json.Unmarshal(raw, &doc2); err != nil {
		return nil, fmt.Errorf("specverb: parse swagger json: %w", err)
	}
	if !strings.HasPrefix(doc2.Swagger, "2.") {
		return nil, fmt.Errorf("specverb: expected swagger 2.0, got %q", doc2.Swagger)
	}
	if len(doc2.Paths) == 0 {
		return nil, fmt.Errorf("specverb: spec has no paths")
	}
	doc3, err := openapi2conv.ToV3(&doc2)
	if err != nil {
		return nil, fmt.Errorf("specverb: upgrade swagger 2.0 to openapi 3.x: %w", err)
	}
	if doc3.Paths == nil || doc3.Paths.Len() == 0 {
		return nil, fmt.Errorf("specverb: spec has no paths")
	}
	return newSpec(doc3), nil
}

// pathParamRe pulls `{name}` tokens out of a path template in declaration order.
var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

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
