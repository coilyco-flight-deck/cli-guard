// Multi-call action execution: an ordered sequence of granted leaf calls with
// `as` bindings and `$step.field` data-flow between them. See docs/specverb-actions.md.

package specverb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/respfmt"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// resolveCallAction resolves a multi-call action, failing closed at each gate
// (granted-only leaf, valid args, valid refs). See docs/specverb-actions.md.
func resolveCallAction(spec *swaggerSpec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, a guardfile.Action) (actionDescriptor, error) {
	inputNames := map[string]bool{}
	for _, in := range a.Inputs {
		inputNames[in.Name] = true
	}
	bound := map[string]bool{}
	var steps []callStep
	for i, call := range a.Calls {
		g, ok := granted[grantKey{Verb: call.Verb, Resource: call.Resource}]
		if !ok {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q call %d: %q %q which no `can` grant authorizes (deny-by-default; add `can %s %s`)", a.Name, i+1, call.Verb, call.Resource, call.Verb, call.Resource)
		}
		leaf, err := resolveDescriptor(spec, gf.Group, g)
		if err != nil {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q call %d: %w", a.Name, i+1, err)
		}
		if err := validateCallArgs(a.Name, i+1, call, leaf, inputNames, bound); err != nil {
			return actionDescriptor{}, err
		}
		steps = append(steps, callStep{Leaf: leaf, Args: call.Args, As: call.As})
		if call.As != "" {
			bound[call.As] = true
		}
	}
	if a.FailWhen != "" {
		if err := respfmt.Validate(a.FailWhen); err != nil {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q: fail-when: %w", a.Name, err)
		}
	}
	return actionDescriptor{
		Name:     a.Name,
		VerbName: strings.Join(gf.Group, ".") + "." + actionGroup + "." + a.Name,
		Describe: a.Describe,
		Inputs:   a.Inputs,
		Calls:    steps,
		FailWhen: a.FailWhen,
	}, nil
}

// validateCallArgs checks one call's args: a `$ref` names a declared input or a
// prior `as` binding, and the arg targets a real path param, flag, or sugar.
func validateCallArgs(action string, step int, call guardfile.Call, leaf opDescriptor, inputNames, bound map[string]bool) error {
	paramNames := map[string]bool{}
	for _, p := range leaf.PathParams {
		paramNames[p] = true
	}
	flagNames := map[string]bool{}
	for _, f := range append(append(append([]fieldFlag{}, leaf.QueryFlags...), leaf.BodyFlags...), leaf.FormFlags...) {
		flagNames[f.Name] = true
	}
	for _, arg := range call.Args {
		if err := validateCallArgRef(action, step, arg, inputNames, bound); err != nil {
			return err
		}
		switch {
		case arg.Name == ownerRepoArg:
			if !paramNames["owner"] || !paramNames["repo"] {
				return fmt.Errorf("specverb: action %q call %d: arg %q needs the leaf to take owner+repo path params, but %s %s does not", action, step, ownerRepoArg, leaf.Method, leaf.Path)
			}
		case paramNames[arg.Name] || flagNames[arg.Name]:
			// binds a real path param or flag
		default:
			return fmt.Errorf("specverb: action %q call %d: arg %q targets nothing on %s %s (not a path param or flag; fail-closed)", action, step, arg.Name, leaf.Method, leaf.Path)
		}
	}
	return nil
}

// validateCallArgRef checks a `$ref` arg value: a bare `$name` is a declared
// input, a `$step.field` names a prior step's `as` binding. Literals pass.
func validateCallArgRef(action string, step int, arg guardfile.ArgBind, inputNames, bound map[string]bool) error {
	if !strings.HasPrefix(arg.Value, "$") {
		return nil
	}
	ref := strings.TrimPrefix(arg.Value, "$")
	if dot := strings.IndexByte(ref, '.'); dot >= 0 {
		head := ref[:dot]
		if !bound[head] {
			return fmt.Errorf("specverb: action %q call %d: arg %q references $%s.* but no prior step binds `as %s`", action, step, arg.Name, head, head)
		}
		return nil
	}
	if !inputNames[ref] {
		return fmt.Errorf("specverb: action %q call %d: arg %q references $%s, which no `input` declares", action, step, arg.Name, ref)
	}
	return nil
}

// runCallAction runs the ordered call sequence, binding each response under `as`
// for later $step.field data-flow. --dry-run prints the plan. See specverb-actions.md.
func (rt *runtime) runCallAction(ctx context.Context, c *cli.Command, ad actionDescriptor, strVars map[string]string) error {
	bindings := map[string]any{}
	dry := c.Bool(flagDryRun)
	var plan []any
	var lastRaw []byte
	for i, step := range ad.Calls {
		resolve := func(v string) (string, error) {
			if dry {
				return resolveCallArgDry(v, strVars), nil
			}
			return resolveCallArg(v, strVars, bindings)
		}
		method, url, body, contentType, err := rt.buildCallRequest(step.Leaf, step.Args, resolve)
		if err != nil {
			return err
		}
		if dry {
			plan = append(plan, rt.callPlanStep(step, method, url, body, contentType))
			continue
		}
		decoded, raw, ferr := rt.fireCallAudited(ctx, step.Leaf, method, url, body, contentType, c)
		if ferr != nil {
			return exitcode.New(exitcode.UpstreamFailed, "action_failed", fmt.Errorf("call %d (%s): %w", i+1, step.Leaf.Leaf, ferr), "a step in the action sequence failed; nothing after it ran")
		}
		lastRaw = raw
		if step.As != "" {
			bindings[step.As] = decoded
		}
	}
	if dry {
		return rt.renderCallPlan(ad, plan, c.String(flagOutput))
	}
	if err := renderFinal(lastRaw, c.String(flagOutput)); err != nil {
		return err
	}
	return rt.applyFailWhen(ad, lastRaw, bindings)
}

// fireCallAudited fires one call through the verb pipeline so it writes its own
// leaf audit row, returning the decoded response for the next step's data-flow.
func (rt *runtime) fireCallAudited(ctx context.Context, leaf opDescriptor, method, url string, body []byte, contentType string, c *cli.Command) (decoded any, raw []byte, err error) {
	inner := verb.Spec{
		Name:       leaf.VerbName,
		SkipPolicy: true, // the action envelope already gated the operator's argv
		Action: func(ictx context.Context, _ *cli.Command) error {
			var e error
			decoded, raw, _, e = rt.fireCapture(ictx, method, url, body, contentType)
			return e
		},
	}
	err = rt.wrap(inner)(ctx, c)
	return decoded, raw, err
}

// resolveCallArg resolves a call arg value: a literal verbatim, a `$input` from
// the bound inputs, or a `$step.field` projected out of a prior step's response.
func resolveCallArg(value string, inputs map[string]string, bindings map[string]any) (string, error) {
	if !strings.HasPrefix(value, "$") {
		return value, nil
	}
	ref := strings.TrimPrefix(value, "$")
	if dot := strings.IndexByte(ref, '.'); dot >= 0 {
		head, path := ref[:dot], ref[dot+1:]
		data, ok := bindings[head]
		if !ok {
			return "", fmt.Errorf("$%s: step %q is not bound (it runs later or sets no `as`)", ref, head)
		}
		out, err := respfmt.Eval(data, path, nil)
		if err != nil {
			return "", fmt.Errorf("$%s: %w", ref, err)
		}
		if out == nil {
			return "", fmt.Errorf("$%s resolved to null in the prior response", ref)
		}
		return scalarToString(out), nil
	}
	if v, ok := inputs[ref]; ok {
		return v, nil
	}
	return "", fmt.Errorf("$%s is not set (an optional input that was not supplied)", ref)
}

// resolveCallArgDry resolves an arg value for a --dry-run plan: literals and
// bound inputs resolve, a $step.field stays a `${ref}` placeholder (nothing fired).
func resolveCallArgDry(value string, inputs map[string]string) string {
	if !strings.HasPrefix(value, "$") {
		return value
	}
	ref := strings.TrimPrefix(value, "$")
	if !strings.Contains(ref, ".") {
		if v, ok := inputs[ref]; ok {
			return v
		}
	}
	return "${" + ref + "}"
}

// scalarToString renders a JMESPath scalar result for use as a path/query/body
// value: whole-number floats lose the trailing ".0", others format naturally.
func scalarToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// callPlanStep renders one dry-run plan step: the resolved request with the auth
// redacted, so the whole sequence can be inspected before it fires.
func (rt *runtime) callPlanStep(step callStep, method, url string, body []byte, contentType string) map[string]any {
	s := map[string]any{
		"leaf":    step.Leaf.Leaf,
		"method":  method,
		"url":     rt.previewURL(url),
		"headers": rt.previewHeaders(body != nil, contentType),
	}
	if step.As != "" {
		s["as"] = step.As
	}
	if body != nil {
		var parsed any
		if err := json.Unmarshal(body, &parsed); err == nil {
			s["body"] = parsed
		}
	}
	return s
}

// renderCallPlan prints the planned call sequence (--dry-run), firing nothing.
func (rt *runtime) renderCallPlan(ad actionDescriptor, plan []any, output string) error {
	raw, err := json.Marshal(map[string]any{"action": ad.Name, "calls": plan})
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
