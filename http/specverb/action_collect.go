// Collect action execution: auto-paginate a granted list leaf and emit one
// accumulated JSON array. See docs/specverb-actions.md.

package specverb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/respfmt"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

func resolveCollectAction(spec *spec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, a guardfile.Action) (actionDescriptor, error) {
	col := a.Collect
	inputNames := map[string]bool{}
	for _, in := range a.Inputs {
		inputNames[in.Name] = true
		if in.Default != "" {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q: input %q: `default` is a poll-action pre-flight binding and is not supported on collect actions (fail-closed)", a.Name, in.Name)
		}
	}
	g, ok := granted[grantKey{Verb: col.Verb, Resource: col.Resource}]
	if !ok {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q collects %q %q which no `can` grant authorizes (deny-by-default; add `can %s %s`)", a.Name, col.Verb, col.Resource, col.Verb, col.Resource)
	}
	leaf, err := resolveDescriptor(spec, gf.Group, g)
	if err != nil {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q: %w", a.Name, err)
	}
	limit, err := strconv.Atoi(col.DefaultLimit)
	if err != nil || limit <= 0 {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q: default-limit %q must be a positive integer", a.Name, col.DefaultLimit)
	}
	if err := validateCollectArgs(a.Name, *col, leaf, inputNames); err != nil {
		return actionDescriptor{}, err
	}
	if a.FailWhen != "" {
		if err := respfmt.Validate(a.FailWhen); err != nil {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q: fail-when: %w", a.Name, err)
		}
	}
	return actionDescriptor{
		Name:          a.Name,
		VerbName:      actionVerbName(gf.Group, a),
		Describe:      a.Describe,
		Inputs:        a.Inputs,
		Collect:       &collectStep{Leaf: leaf, Args: col.Args, PageParam: col.PageParam, LimitParam: col.LimitParam, Limit: col.Limit, DefaultLimit: limit, As: col.As},
		FailWhen:      a.FailWhen,
		MountVerb:     a.MountVerb,
		MountResource: a.MountResource,
	}, nil
}

func validateCollectArgs(action string, col guardfile.Collect, leaf opDescriptor, inputNames map[string]bool) error {
	queryNames := map[string]bool{}
	for _, f := range leaf.QueryFlags {
		queryNames[f.Name] = true
	}
	if !queryNames[col.PageParam] {
		return fmt.Errorf("specverb: action %q: page-param %q is not a query flag on %s %s", action, col.PageParam, leaf.Method, leaf.Path)
	}
	if !queryNames[col.LimitParam] {
		return fmt.Errorf("specverb: action %q: limit-param %q is not a query flag on %s %s", action, col.LimitParam, leaf.Method, leaf.Path)
	}
	if col.Limit != "" {
		if err := validateCollectRef(action, "limit", col.Limit, inputNames); err != nil {
			return err
		}
	}
	call := guardfile.Call{Args: col.Args}
	return validateCallArgs(action, 1, call, leaf, inputNames, nil)
}

func validateCollectRef(action, field, value string, inputNames map[string]bool) error {
	if !strings.HasPrefix(value, "$") {
		return nil
	}
	ref := strings.TrimPrefix(value, "$")
	if !inputNames[ref] {
		return fmt.Errorf("specverb: action %q: %s references $%s, which no `input` declares", action, field, ref)
	}
	return nil
}

func (rt *runtime) runCollectAction(ctx context.Context, c *cli.Command, ad actionDescriptor, strVars map[string]string, jmesVars map[string]any) error {
	dry := c.Bool(flagDryRun)
	limit, err := collectLimit(ad, strVars)
	if err != nil {
		return err
	}
	if dry {
		method, url, contentType, berr := rt.buildCollectRequest(ctx, dry, ad.Collect, strVars, 1, limit)
		if berr != nil {
			return berr
		}
		return rt.renderCollectPlan(ad, method, url, nil, contentType, limit, c.String(flagOutput))
	}

	var all []any
	for page := 1; ; page++ {
		method, url, contentType, berr := rt.buildCollectRequest(ctx, dry, ad.Collect, strVars, page, limit)
		if berr != nil {
			return berr
		}
		decoded, _, ferr := rt.fireCallAudited(ctx, ad.Collect.Leaf, method, url, nil, contentType, c)
		if ferr != nil {
			return exitcode.New(exitcode.UpstreamFailed, "action_failed", fmt.Errorf("collect page %d (%s): %w", page, ad.Collect.Leaf.Leaf, ferr), "a page in the collection failed; the accumulated result was not emitted")
		}
		items, ok := decoded.([]any)
		if !ok {
			return exitcode.New(exitcode.UpstreamFailed, "action_failed", fmt.Errorf("collect page %d: response is not a JSON array", page), "collect actions require array responses")
		}
		all = append(all, items...)
		if len(items) < limit {
			break
		}
	}
	jmesVars[ad.Collect.As] = all
	raw, err := json.Marshal(all)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "")
	}
	if err := renderFinal(raw, c.String(flagOutput)); err != nil {
		return err
	}
	return rt.applyFailWhen(ad, raw, jmesVars)
}

func collectLimit(ad actionDescriptor, strVars map[string]string) (int, error) {
	limit := ad.Collect.DefaultLimit
	if ad.Collect.Limit != "" {
		v, ok := resolveCollectArgValue(ad.Collect.Limit, strVars)
		if !ok {
			return limit, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, exitcode.New(exitcode.UserError, "user_error", fmt.Errorf("collect limit %q must resolve to a positive integer", v), "pass a positive --limit")
		}
		limit = n
	}
	return limit, nil
}

func (rt *runtime) buildCollectRequest(ctx context.Context, dry bool, col *collectStep, strVars map[string]string, page, limit int) (method, url, contentType string, err error) {
	b := newArgBinder(col.Leaf)
	for _, arg := range col.Args {
		val, ok := resolveCollectArgValue(arg.Value, strVars)
		if !ok {
			continue
		}
		if berr := b.bind(arg.Name, val); berr != nil {
			return "", "", "", berr
		}
	}
	if berr := b.bind(col.PageParam, strconv.Itoa(page)); berr != nil {
		return "", "", "", berr
	}
	if berr := b.bind(col.LimitParam, strconv.Itoa(limit)); berr != nil {
		return "", "", "", berr
	}
	if berr := b.requireAllPaths(); berr != nil {
		return "", "", "", berr
	}
	if rerr := rt.checkRestrictions(col.Leaf.PathParams, b.pathVals); rerr != nil {
		return "", "", "", rerr
	}
	qs := ""
	if len(b.query) > 0 {
		qs = "?" + b.query.Encode()
	}
	base, berr := rt.baseForRequest(ctx, dry)
	if berr != nil {
		return "", "", "", berr
	}
	url = base + fillPath(col.Leaf.Path, b.pathVals) + qs
	return col.Leaf.Method, url, contentTypeJSON, nil
}

func resolveCollectArgValue(value string, strVars map[string]string) (resolved string, ok bool) {
	if !strings.HasPrefix(value, "$") {
		return value, true
	}
	ref := strings.TrimPrefix(value, "$")
	v, ok := strVars[ref]
	if !ok {
		return "", false
	}
	return v, true
}

func (rt *runtime) renderCollectPlan(ad actionDescriptor, method, url string, body []byte, contentType string, limit int, output string) error {
	collect := map[string]any{
		"method":      method,
		"url":         rt.previewURL(url),
		"headers":     rt.previewHeaders(body != nil, contentType),
		"page_param":  ad.Collect.PageParam,
		"limit_param": ad.Collect.LimitParam,
		"limit":       limit,
		"as":          ad.Collect.As,
		"stop":        "short_page",
	}
	plan := map[string]any{
		"action":  ad.Name,
		"collect": collect,
	}
	if ad.FailWhen != "" {
		plan["fail_when"] = ad.FailWhen
	}
	raw, err := json.Marshal(plan)
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
