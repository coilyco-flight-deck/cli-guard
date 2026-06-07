// The static request machinery: one generic action assembles, previews
// (--dry-run), fires, and renders every mounted verb. See docs/specverb.md.

package specverb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/exitcode"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/respfmt"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

// redacted is what a secret header value renders as in a --dry-run preview, so
// the resolved request can be inspected without leaking the token.
const redacted = "<redacted>"

// runtime carries the per-tree request dependencies shared by every leaf.
type runtime struct {
	baseURL string
	auth    guardfile.Auth
	token   TokenResolver
	client  *http.Client
	wrap    func(verb.Spec) cli.ActionFunc
}

// universal flag names every mounted leaf carries.
const (
	flagDryRun = "dry-run"
	flagQuery  = "query"
	flagOutput = "output"
)

// buildLeaf turns one descriptor into a guarded leaf: body flags plus the
// universal dry-run/query/output flags, action wrapped in the verb pipeline.
func (rt *runtime) buildLeaf(desc opDescriptor) *cli.Command {
	flags := []cli.Flag{
		&cli.BoolFlag{Name: flagDryRun, Usage: "print the resolved request without firing it"},
		&cli.StringFlag{Name: flagQuery, Usage: "JMESPath projection applied to the response"},
		&cli.StringFlag{Name: flagOutput, Usage: "output format: yaml | yaml-stream | json | text | table"},
	}
	flags = append(flags, bodyFlagsToCLI(desc.BodyFlags)...)

	usage := fmt.Sprintf("%s %s", desc.Method, desc.Path)
	if desc.Destructive {
		usage += " (destructive)"
	}
	return &cli.Command{
		Name:      desc.Leaf,
		Usage:     usage,
		ArgsUsage: argsUsage(desc.PathParams),
		Flags:     flags,
		Action: rt.wrap(verb.Spec{
			Name:     desc.VerbName,
			ArgsFunc: argsFuncFor(desc),
			Action:   rt.actionFor(desc),
		}),
	}
}

// bodyFlagsToCLI maps each promoted body field to its typed cli.Flag.
func bodyFlagsToCLI(bf []bodyFlag) []cli.Flag {
	var flags []cli.Flag
	for _, f := range bf {
		usage := f.Desc
		switch f.Type {
		case "boolean":
			flags = append(flags, &cli.BoolFlag{Name: f.Name, Usage: usage, Required: f.Required})
		case "integer":
			flags = append(flags, &cli.IntFlag{Name: f.Name, Usage: usage, Required: f.Required})
		case "number":
			flags = append(flags, &cli.FloatFlag{Name: f.Name, Usage: usage, Required: f.Required})
		default: // string
			flags = append(flags, &cli.StringFlag{Name: f.Name, Usage: usage, Required: f.Required})
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

// argsFuncFor extracts the user strings for the policy shell-metachar gate:
// every set body flag plus the positional path params.
func argsFuncFor(desc opDescriptor) func(*cli.Command) (map[string]string, []string) {
	return func(c *cli.Command) (map[string]string, []string) {
		named := map[string]string{}
		for _, f := range desc.BodyFlags {
			if c.IsSet(f.Name) {
				named[f.Name] = stringifyFlag(c, f)
			}
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
		url := rt.baseURL + fillPath(desc.Path, positional)
		body, err := assembleBody(c, desc.BodyFlags)
		if err != nil {
			return exitcode.New(exitcode.UserError, "user_error", err, "check the body flag values")
		}

		if c.Bool(flagDryRun) {
			return rt.renderDryRun(desc.Method, url, body, c.String(flagOutput))
		}
		return rt.fire(ctx, desc.Method, url, body, c.String(flagQuery), c.String(flagOutput))
	}
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

// assembleBody builds the request body JSON: a field is included only when it
// is required or explicitly set, so an unset optional is omitted, not zeroed.
func assembleBody(c *cli.Command, flags []bodyFlag) ([]byte, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	obj := map[string]any{}
	for _, f := range flags {
		if !f.Required && !c.IsSet(f.Name) {
			continue
		}
		switch f.Type {
		case "boolean":
			obj[f.Name] = c.Bool(f.Name)
		case "integer":
			obj[f.Name] = c.Int(f.Name)
		case "number":
			obj[f.Name] = c.Float(f.Name)
		default:
			obj[f.Name] = c.String(f.Name)
		}
	}
	if len(obj) == 0 {
		return nil, nil
	}
	return json.Marshal(obj)
}

// renderDryRun prints the resolved request without firing it, auth value
// redacted, honoring --output so a dry-run reads the same as a live response.
func (rt *runtime) renderDryRun(method, url string, body []byte, output string) error {
	preview := map[string]any{
		"method":  method,
		"url":     url,
		"headers": rt.previewHeaders(body),
	}
	if body != nil {
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

// previewHeaders builds the header map a dry-run shows, redacting the secret.
func (rt *runtime) previewHeaders(body []byte) map[string]string {
	h := map[string]string{rt.auth.Header: rt.auth.Prefix + redacted}
	if body != nil {
		h["Content-Type"] = "application/json"
	}
	return h
}

// fire resolves the secret, sends the request, and renders the response.
// Non-2xx becomes an UpstreamFailed coded error carrying the response body.
func (rt *runtime) fire(ctx context.Context, method, url string, body []byte, query, output string) error {
	if rt.token == nil {
		return exitcode.New(exitcode.Internal, "internal",
			fmt.Errorf("no token resolver configured"), "wire a TokenResolver into specverb.Config")
	}
	secret, err := rt.token(ctx, rt.auth.SSM)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal",
			fmt.Errorf("resolve auth secret from %s: %w", rt.auth.SSM, err), "check the SSM path and credentials")
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "")
	}
	req.Header.Set(rt.auth.Header, rt.auth.Prefix+secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := rt.client.Do(req)
	if err != nil {
		return exitcode.New(exitcode.UpstreamFailed, "upstream_failed", err, "the API was unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return exitcode.New(exitcode.UpstreamFailed, "upstream_failed",
			fmt.Errorf("%s %s -> %s: %s", method, url, resp.Status, strings.TrimSpace(string(respBody))),
			"the API rejected the request")
	}

	rendered, err := respfmt.Render(respBody, query, output)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "the response was not valid JSON")
	}
	if len(rendered) == 0 {
		// empty 2xx (204): confirm so the operator sees the call landed.
		fmt.Printf("ok: %s %s -> %s\n", method, url, resp.Status)
		return nil
	}
	fmt.Print(string(rendered))
	return nil
}

// stringifyFlag renders a set flag's value as a string for the policy gate.
func stringifyFlag(c *cli.Command, f bodyFlag) string {
	switch f.Type {
	case "boolean":
		return fmt.Sprintf("%t", c.Bool(f.Name))
	case "integer":
		return fmt.Sprintf("%d", c.Int(f.Name))
	case "number":
		return fmt.Sprintf("%g", c.Float(f.Name))
	default:
		return c.String(f.Name)
	}
}
