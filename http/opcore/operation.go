package opcore

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/policy"
)

// Operation is one resolved leaf plus the runtime that fires it: the unit a
// non-CLI consumer (ward-mcp) drives via the self-guarding Execute.
type Operation struct {
	Desc Descriptor
	RT   *Runtime
}

// Args is the operator input to one Operation, split by URL location: Path and
// Query values reach the URL (the injection surface); Body does not.
type Args struct {
	Path  map[string]string
	Query map[string]string
	Body  map[string]any
}

// Request is a resolved-but-unfired call: the exact method, URL, and body the
// runtime would send. Preview returns one for a consumer's dry-run.
type Request struct {
	Method      string
	URL         string
	Body        []byte
	ContentType string
}

// Response is a fired call's outcome: the decoded JSON value, the raw bytes
// (for a consumer's own rendering), and the HTTP status line.
type Response struct {
	Decoded any
	Raw     []byte
	Status  string
}

// Resolve validates, gates, and assembles one request without firing it.
// Consumers use it when they need the resolved request shape.
func (o Operation) Resolve(ctx context.Context, a Args, dry bool) (Request, error) {
	return o.resolve(ctx, a, dry)
}

// Execute runs the leaf under the full security floor (gate, restrict, assemble,
// base-url, auth, fire) and returns the decoded response, rendering nothing.
func (o Operation) Execute(ctx context.Context, a Args) (Response, error) {
	req, err := o.Resolve(ctx, a, false)
	if err != nil {
		return Response{}, err
	}
	decoded, raw, status, err := o.RT.FireCapture(ctx, req.Method, req.URL, req.Body, req.ContentType)
	if err != nil {
		return Response{}, err
	}
	return Response{Decoded: decoded, Raw: raw, Status: status}, nil
}

// Preview resolves the request without firing it (same gate/restrict/assembly as
// Execute) for a dry-run; a value-resolved base-url stays an offline placeholder.
func (o Operation) Preview(a Args) (Request, error) {
	return o.Resolve(context.Background(), a, true)
}

// resolve runs the gate, restrictions, and assembly shared by Execute and
// Preview, returning the resolved request. dry keeps base-url resolution offline.
func (o Operation) resolve(ctx context.Context, a Args, dry bool) (Request, error) {
	d := o.Desc
	// Gate the URL-bound surface (query params, positional path values); body is
	// exempt. Re-runs verb.Wrap's gate for a CLI leaf, idempotent when stacked.
	if err := policy.ValidateArgs(a.Query); err != nil {
		return Request{}, gateDenied(err)
	}
	pathVals, err := o.orderedPathValues(a.Path)
	if err != nil {
		return Request{}, err
	}
	if err := policy.ValidateArgSlice("positional", pathVals); err != nil {
		return Request{}, gateDenied(err)
	}
	if err := o.RT.CheckRestrictions(d.PathParams, pathVals); err != nil {
		return Request{}, err
	}
	body, err := o.assembleBody(a.Body)
	if err != nil {
		return Request{}, err
	}
	base, err := o.RT.BaseForRequest(ctx, dry)
	if err != nil {
		return Request{}, err
	}
	url := base + FillPath(d.Path, pathVals) + assembleQuery(a.Query)
	return Request{Method: d.Method, URL: url, Body: body, ContentType: contentTypeJSON}, nil
}

// orderedPathValues lowers the path-arg map to the leaf's declared path-param
// order, failing closed when any declared param is unbound.
func (o Operation) orderedPathValues(path map[string]string) ([]string, error) {
	vals := make([]string, len(o.Desc.PathParams))
	for i, p := range o.Desc.PathParams {
		v, ok := path[p]
		if !ok || v == "" {
			return nil, exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("%s: path param %q was not supplied", o.Desc.Leaf, p),
				"supply every path parameter this operation names")
		}
		vals[i] = v
	}
	return vals, nil
}

// assembleQuery encodes the query map as a ?-prefixed string, "" when empty.
// url.Values sorts keys, so the result is deterministic.
func assembleQuery(q map[string]string) string {
	if len(q) == 0 {
		return ""
	}
	vals := neturl.Values{}
	for k, v := range q {
		vals.Set(k, v)
	}
	return "?" + vals.Encode()
}

// assembleBody builds the body JSON: a state-toggle's FixedBody wins, else the
// supplied object with required-field enforcement; an empty body marshals to nil.
func (o Operation) assembleBody(body map[string]any) ([]byte, error) {
	if len(o.Desc.FixedBody) > 0 {
		return json.Marshal(o.Desc.FixedBody)
	}
	if err := validateBodyFields(body, o.Desc.BodyFlags, ""); err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	return json.Marshal(body)
}

// validateBodyFields enforces required body fields recursively. Optional
// nested fields only matter when their parent object or array is present.
func validateBodyFields(body map[string]any, fields []Field, prefix string) error {
	for _, f := range fields {
		if err := validateBodyField(body, f, prefix); err != nil {
			return err
		}
	}
	return nil
}

// validateBodyField enforces one required field and recurses into nested shapes.
func validateBodyField(body map[string]any, f Field, prefix string) error {
	path := f.Name
	if prefix != "" {
		path = prefix + "." + f.Name
	}
	v, present := body[f.Name]
	if !present {
		if f.Required {
			return exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("required body field %q is missing", path),
				"supply the required body field")
		}
		return nil
	}
	if f.Raw {
		return nil
	}
	switch f.Type {
	case "object":
		return validateObjectBodyValue(v, f, path)
	case "array":
		return validateArrayBodyValue(v, f, path)
	default:
		return nil
	}
}

// validateObjectBodyValue walks a nested object value when the field declares
// child requirements.
func validateObjectBodyValue(v any, f Field, path string) error {
	child, ok := v.(map[string]any)
	if !ok || len(f.Fields) == 0 {
		return nil
	}
	return validateBodyFields(child, f.Fields, path)
}

// validateArrayBodyValue walks an array of object items when the field declares
// an item schema.
func validateArrayBodyValue(v any, f Field, path string) error {
	if f.Item == nil || len(f.Item.Fields) == 0 {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	for i, item := range items {
		child, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if err := validateBodyFields(child, f.Item.Fields, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}

// gateDenied wraps a shell-metachar rejection as a coded PolicyDenied error.
func gateDenied(err error) error {
	return exitcode.New(exitcode.PolicyDenied, "policy_denied", err,
		"move the argument with the metacharacter into a file and pass it by path, "+
			"or set allow_metacharacters on the verb if it is known-safe")
}
