// The static request machinery: one generic action assembles, previews
// (--dry-run), fires, and renders every mounted verb. See docs/specverb.md.

package specverb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/respfmt"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// redacted is what a secret header value renders as in a --dry-run preview, so
// the resolved request can be inspected without leaking the token.
const redacted = "<redacted>"

// runtime carries the per-tree request dependencies shared by every leaf.
type runtime struct {
	baseURL  string
	auth     guardfile.Auth
	token    TokenResolver
	client   *http.Client
	wrap     func(verb.Spec) cli.ActionFunc
	restrict []guardfile.Restriction

	// baseURLFn (nil = static baseURL) resolves the host from SSM once, caching it.
	// baseURLSSM names the path for errors. See specverb-policy.md.
	baseURLFn   func(ctx context.Context) (string, error)
	baseURLSSM  string
	baseURLOnce sync.Once
	baseURLVal  string
	baseURLErr  error
}

// baseForRequest returns the request base-url: the static base, or (for a
// `base-url { ssm }` host) a once-resolved SSM value. Dry-run stays offline.
func (rt *runtime) baseForRequest(ctx context.Context, dry bool) (string, error) {
	if rt.baseURLFn == nil {
		return rt.baseURL, nil
	}
	if dry {
		return "{base-url:ssm " + rt.baseURLSSM + "}", nil
	}
	rt.baseURLOnce.Do(func() {
		v, err := rt.baseURLFn(ctx)
		if err != nil {
			rt.baseURLErr = err
			return
		}
		rt.baseURLVal = defaultScheme(strings.TrimRight(v, "/"))
	})
	if rt.baseURLErr != nil {
		return "", exitcode.New(exitcode.Internal, "internal",
			fmt.Errorf("resolve base-url from ssm %s: %w", rt.baseURLSSM, rt.baseURLErr),
			"check the SSM path and credentials")
	}
	return rt.baseURLVal, nil
}

// universal flag names every mounted leaf carries.
const (
	flagDryRun   = "dry-run"
	flagQuery    = "query"
	flagOutput   = "output"
	flagBodyFile = "body-file"
)

// buildLeaf turns one descriptor into a guarded leaf: query + body flags plus
// the universal dry-run/query/output flags, action wrapped in the verb pipeline.
func (rt *runtime) buildLeaf(desc opDescriptor) *cli.Command {
	flags := []cli.Flag{
		&cli.BoolFlag{Name: flagDryRun, Usage: "print the resolved request without firing it"},
		&cli.StringFlag{Name: flagQuery, Usage: "JMESPath projection applied to the response"},
		&cli.StringFlag{Name: flagOutput, Usage: "output format: yaml | yaml-stream | json | text | table"},
	}
	if len(desc.BodyFlags) > 0 {
		flags = append(flags, &cli.StringFlag{Name: flagBodyFile, Usage: "path to a JSON file supplying the full request body (exclusive with the body flags)"})
	}
	flags = append(flags, fieldFlagsToCLI(desc.QueryFlags)...)
	flags = append(flags, fieldFlagsToCLI(desc.BodyFlags)...)
	flags = append(flags, fieldFlagsToCLI(desc.FormFlags)...)

	usage := fmt.Sprintf("%s %s", desc.Method, desc.Path)
	if desc.Destructive {
		usage += " (destructive)"
	}
	return &cli.Command{
		Name:        desc.Leaf,
		Usage:       usage,
		Description: leafDescription(desc),
		ArgsUsage:   argsUsage(desc.PathParams),
		Flags:       flags,
		Action: rt.wrap(verb.Spec{
			Name:     desc.VerbName,
			ArgsFunc: argsFuncFor(desc),
			Action:   rt.actionFor(desc),
		}),
	}
}

// fieldFlagsToCLI maps each promoted spec input to its typed cli.Flag; nothing
// is CLI-required since assembly enforces it (--body-file is a legal source).
func fieldFlagsToCLI(ff []fieldFlag) []cli.Flag {
	var flags []cli.Flag
	for _, f := range ff {
		usage := f.Desc
		switch f.Type {
		case "boolean":
			flags = append(flags, &cli.BoolFlag{Name: f.Name, Usage: usage})
		case "integer":
			flags = append(flags, &cli.IntFlag{Name: f.Name, Usage: usage})
		case "number":
			flags = append(flags, &cli.FloatFlag{Name: f.Name, Usage: usage})
		case "array":
			switch f.Items {
			case "integer":
				flags = append(flags, &cli.IntSliceFlag{Name: f.Name, Usage: usage})
			case "number":
				flags = append(flags, &cli.FloatSliceFlag{Name: f.Name, Usage: usage})
			default: // string
				flags = append(flags, &cli.StringSliceFlag{Name: f.Name, Usage: usage})
			}
		default: // string
			flags = append(flags, &cli.StringFlag{Name: f.Name, Usage: usage})
		}
	}
	return flags
}

// argsUsage renders the positional path params as `<owner> <repo>`.
func argsUsage(params []string) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = "<" + p + ">"
	}
	return strings.Join(parts, " ")
}

// argsFuncFor extracts the user strings for the policy shell-metachar gate;
// --body-file contributes its path, never its contents (the gate-safe spill).
func argsFuncFor(desc opDescriptor) func(*cli.Command) (map[string]string, []string) {
	return func(c *cli.Command) (map[string]string, []string) {
		named := map[string]string{}
		all := append(append([]fieldFlag{}, desc.QueryFlags...), desc.BodyFlags...)
		for _, f := range append(all, desc.FormFlags...) {
			if c.IsSet(f.Name) {
				named[f.Name] = stringifyFlag(c, f)
			}
		}
		if c.IsSet(flagBodyFile) {
			named[flagBodyFile] = c.String(flagBodyFile)
		}
		return named, c.Args().Slice()
	}
}

// actionFor is the generic action bound to one descriptor.
func (rt *runtime) actionFor(desc opDescriptor) cli.ActionFunc {
	return func(ctx context.Context, c *cli.Command) error {
		positional := c.Args().Slice()
		if len(positional) != len(desc.PathParams) {
			return exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("%s takes %d positional arg(s) %v, got %d", desc.Leaf, len(desc.PathParams), desc.PathParams, len(positional)),
				"supply exactly the path parameters this verb names")
		}
		if err := rt.checkRestrictions(desc.PathParams, positional); err != nil {
			return err
		}
		base, err := rt.baseForRequest(ctx, c.Bool(flagDryRun))
		if err != nil {
			return err
		}
		url := base + fillPath(desc.Path, positional) + assembleQuery(c, desc.QueryFlags)
		var body []byte
		contentType := contentTypeJSON
		var preview any
		switch {
		case len(desc.FormFlags) > 0:
			body, contentType, preview, err = assembleMultipart(c, desc.FormFlags, c.Bool(flagDryRun))
			if err != nil {
				return exitcode.New(exitcode.UserError, "user_error", err, "check the form flag values")
			}
		case len(desc.FixedBody) > 0:
			body, err = json.Marshal(desc.FixedBody)
			if err != nil {
				return exitcode.New(exitcode.Internal, "internal", err, "")
			}
		default:
			body, err = assembleBody(c, desc.BodyFlags)
			if err != nil {
				return exitcode.New(exitcode.UserError, "user_error", err, "check the body flag values")
			}
		}

		if c.Bool(flagDryRun) {
			return rt.renderDryRun(desc.Method, url, body, contentType, preview, c.String(flagOutput))
		}
		return rt.fire(ctx, desc.Method, url, body, contentType, c.String(flagQuery), c.String(flagOutput))
	}
}

// contentTypeJSON is the body content type for every non-multipart verb.
const contentTypeJSON = "application/json"

// assembleMultipart builds a multipart/form-data body from the set form flags,
// streaming "file" params from paths; a dry run returns a part-name preview.
func assembleMultipart(c *cli.Command, flags []fieldFlag, dryRun bool) (body []byte, contentType string, preview any, err error) {
	if dryRun {
		return nil, "multipart/form-data", multipartPreview(c, flags), nil
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range flags {
		if !c.IsSet(f.Name) {
			continue
		}
		if f.Type != "file" {
			if err := w.WriteField(f.Name, c.String(f.Name)); err != nil {
				return nil, "", nil, err
			}
			continue
		}
		if err := writeFilePart(w, f.Name, c.String(f.Name)); err != nil {
			return nil, "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", nil, err
	}
	return buf.Bytes(), w.FormDataContentType(), nil, nil
}

// multipartPreview names each set form part for a dry run, file paths @-marked.
func multipartPreview(c *cli.Command, flags []fieldFlag) map[string]string {
	parts := map[string]string{}
	for _, f := range flags {
		if !c.IsSet(f.Name) {
			continue
		}
		v := c.String(f.Name)
		if f.Type == "file" {
			v = "@" + v
		}
		parts[f.Name] = v
	}
	return parts
}

// writeFilePart streams one file param from its path into the multipart body.
func writeFilePart(w *multipart.Writer, field, path string) error {
	part, err := w.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return err
	}
	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open --%s: %w", field, err)
	}
	_, err = io.Copy(part, src)
	_ = src.Close()
	if err != nil {
		return fmt.Errorf("read --%s: %w", field, err)
	}
	return nil
}

// assembleQuery encodes the set query flags as a ?-prefixed query string, ""
// when none are set. url.Values sorts keys, so the result is deterministic.
func assembleQuery(c *cli.Command, flags []fieldFlag) string {
	vals := neturl.Values{}
	for _, f := range flags {
		if !c.IsSet(f.Name) {
			continue
		}
		vals.Set(f.Name, stringifyFlag(c, f))
	}
	if len(vals) == 0 {
		return ""
	}
	return "?" + vals.Encode()
}

// fillPath substitutes the i-th positional value into the i-th `{...}` slot.
// The caller has already enforced len(values) == number of slots.
func fillPath(template string, values []string) string {
	i := 0
	return pathParamRe.ReplaceAllStringFunc(template, func(string) string {
		if i >= len(values) {
			return ""
		}
		v := values[i]
		i++
		return v
	})
}

// assembleBody builds the body JSON from --body-file or the body flags; unset
// optionals are omitted and required fields enforced over whichever source.
func assembleBody(c *cli.Command, flags []fieldFlag) ([]byte, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	obj, err := bodyObject(c, flags)
	if err != nil {
		return nil, err
	}
	for _, f := range flags {
		if f.Required {
			if _, present := obj[f.Name]; !present {
				return nil, fmt.Errorf("required body field %q is missing (set --%s or supply it via --%s)", f.Name, f.Name, flagBodyFile)
			}
		}
	}
	if len(obj) == 0 {
		return nil, nil
	}
	return json.Marshal(obj)
}

// bodyObject collects the body fields from --body-file or the set body flags,
// the two mutually exclusive sources.
func bodyObject(c *cli.Command, flags []fieldFlag) (map[string]any, error) {
	obj := map[string]any{}
	if !c.IsSet(flagBodyFile) {
		for _, f := range flags {
			if c.IsSet(f.Name) {
				obj[f.Name] = flagValue(c, f)
			}
		}
		return obj, nil
	}
	for _, f := range flags {
		if c.IsSet(f.Name) {
			return nil, fmt.Errorf("--%s and --%s are mutually exclusive", flagBodyFile, f.Name)
		}
	}
	raw, err := os.ReadFile(c.String(flagBodyFile))
	if err != nil {
		return nil, fmt.Errorf("read --%s: %w", flagBodyFile, err)
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("--%s must hold a JSON object: %w", flagBodyFile, err)
	}
	return obj, nil
}

// flagValue reads one set flag as the JSON value its swagger type implies.
func flagValue(c *cli.Command, f fieldFlag) any {
	switch f.Type {
	case "boolean":
		return c.Bool(f.Name)
	case "integer":
		return c.Int(f.Name)
	case "number":
		return c.Float(f.Name)
	case "array":
		switch f.Items {
		case "integer":
			return c.IntSlice(f.Name)
		case "number":
			return c.FloatSlice(f.Name)
		default:
			return c.StringSlice(f.Name)
		}
	default:
		return c.String(f.Name)
	}
}

// renderDryRun prints the resolved request without firing it, auth value
// redacted, honoring --output so a dry-run reads the same as a live response.
func (rt *runtime) renderDryRun(method, url string, body []byte, contentType string, bodyPreview any, output string) error {
	preview := map[string]any{
		"method":  method,
		"url":     rt.previewURL(url),
		"headers": rt.previewHeaders(body != nil || bodyPreview != nil, contentType),
	}
	switch {
	case bodyPreview != nil: // multipart: name the parts, never dump encodings
		preview["body"] = bodyPreview
	case body != nil:
		var parsed any
		if err := json.Unmarshal(body, &parsed); err == nil {
			preview["body"] = parsed
		} else {
			preview["body"] = string(body)
		}
	}
	raw, err := json.Marshal(preview)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "")
	}
	rendered, err := respfmt.Render(raw, "", output)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "")
	}
	fmt.Print(string(rendered))
	return nil
}

// previewHeaders builds the header map a dry-run shows, redacting the secret. The
// query-param scheme carries no auth header (it rides the URL); see previewURL.
func (rt *runtime) previewHeaders(hasBody bool, contentType string) map[string]string {
	h := map[string]string{}
	if rt.auth.Header != "" {
		h[rt.auth.Header] = rt.auth.Prefix + redacted
	}
	if hasBody {
		h["Content-Type"] = contentType
	}
	return h
}

// previewURL returns the URL a dry-run shows: for the query-param scheme it
// appends each auth parameter with a redacted value; other schemes pass through.
func (rt *runtime) previewURL(url string) string {
	if rt.auth.Scheme != "query-param" || len(rt.auth.Params) == 0 {
		return url
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	parts := make([]string, len(rt.auth.Params))
	for i, p := range rt.auth.Params {
		parts[i] = p.Name + "=" + redacted
	}
	return url + sep + strings.Join(parts, "&")
}

// authorize resolves the scheme's secret(s) and applies them to req: a header
// for header-token/bearer, or query parameters for query-param.
func (rt *runtime) authorize(ctx context.Context, req *http.Request) error {
	if rt.auth.Scheme == "query-param" {
		q := req.URL.Query()
		for _, p := range rt.auth.Params {
			secret, err := rt.resolveSecret(ctx, p.SSM)
			if err != nil {
				return err
			}
			q.Set(p.Name, secret)
		}
		req.URL.RawQuery = q.Encode()
		return nil
	}
	secret, err := rt.resolveSecret(ctx, rt.auth.SSM)
	if err != nil {
		return err
	}
	req.Header.Set(rt.auth.Header, rt.auth.Prefix+secret)
	return nil
}

// resolveSecret reads one secret from SSM through the configured resolver.
func (rt *runtime) resolveSecret(ctx context.Context, ssmPath string) (string, error) {
	secret, err := rt.token(ctx, ssmPath)
	if err != nil {
		return "", exitcode.New(exitcode.Internal, "internal",
			fmt.Errorf("resolve auth secret from %s: %w", ssmPath, err), "check the SSM path and credentials")
	}
	return secret, nil
}

// fire resolves the secret, sends the request, and renders the response.
// Non-2xx becomes an UpstreamFailed coded error carrying the response body.
func (rt *runtime) fire(ctx context.Context, method, url string, body []byte, contentType, query, output string) error {
	_, respBody, status, err := rt.fireCapture(ctx, method, url, body, contentType)
	if err != nil {
		return err
	}
	rendered, rerr := respfmt.Render(respBody, query, output)
	if rerr != nil {
		return exitcode.New(exitcode.Internal, "internal", rerr, "the response was not valid JSON")
	}
	if len(rendered) == 0 {
		// empty 2xx (204): confirm so the operator sees the call landed.
		fmt.Printf("ok: %s %s -> %s\n", method, url, status)
		return nil
	}
	fmt.Print(string(rendered))
	return nil
}

// fireCapture sends one request and returns the decoded JSON value plus raw
// body, rendering nothing: the "fire and capture" path complex actions feed on.
func (rt *runtime) fireCapture(ctx context.Context, method, url string, body []byte, contentType string) (decoded any, raw []byte, status string, err error) {
	if rt.token == nil {
		return nil, nil, "", exitcode.New(exitcode.Internal, "internal",
			fmt.Errorf("no token resolver configured"), "wire a TokenResolver into specverb.Config")
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, rerr := http.NewRequestWithContext(ctx, method, url, reqBody)
	if rerr != nil {
		return nil, nil, "", exitcode.New(exitcode.Internal, "internal", rerr, "")
	}
	if aerr := rt.authorize(ctx, req); aerr != nil {
		return nil, nil, "", aerr
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, derr := rt.client.Do(req)
	if derr != nil {
		return nil, nil, "", exitcode.New(exitcode.UpstreamFailed, "upstream_failed", derr, "the API was unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, nil, resp.Status, exitcode.New(exitcode.UpstreamFailed, "upstream_failed",
			fmt.Errorf("%s %s -> %s: %s", method, url, resp.Status, strings.TrimSpace(string(respBody))),
			"the API rejected the request")
	}

	if len(bytes.TrimSpace(respBody)) == 0 {
		return nil, respBody, resp.Status, nil
	}
	if jerr := json.Unmarshal(respBody, &decoded); jerr != nil {
		return nil, respBody, resp.Status, exitcode.New(exitcode.Internal, "internal", jerr, "the response was not valid JSON")
	}
	return decoded, respBody, resp.Status, nil
}

// stringifyFlag renders a set flag's value as a string for the policy gate
// and the query encoder; array values join with commas.
func stringifyFlag(c *cli.Command, f fieldFlag) string {
	switch f.Type {
	case "boolean":
		return fmt.Sprintf("%t", c.Bool(f.Name))
	case "integer":
		return fmt.Sprintf("%d", c.Int(f.Name))
	case "number":
		return fmt.Sprintf("%g", c.Float(f.Name))
	case "array":
		parts := []string{}
		for _, v := range anySlice(c, f) {
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		return strings.Join(parts, ",")
	default:
		return c.String(f.Name)
	}
}

// anySlice reads an array flag's elements as []any for stringification.
func anySlice(c *cli.Command, f fieldFlag) []any {
	var out []any
	switch f.Items {
	case "integer":
		for _, v := range c.IntSlice(f.Name) {
			out = append(out, v)
		}
	case "number":
		for _, v := range c.FloatSlice(f.Name) {
			out = append(out, v)
		}
	default:
		for _, v := range c.StringSlice(f.Name) {
			out = append(out, v)
		}
	}
	return out
}
