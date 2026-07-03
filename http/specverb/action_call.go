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
func resolveCallAction(spec *spec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, a guardfile.Action) (actionDescriptor, error) {
	inputNames := map[string]bool{}
	for _, in := range a.Inputs {
		inputNames[in.Name] = true
		if in.Default != "" {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q: input %q: `default` is a poll-action pre-flight binding and is not supported on call actions (fail-closed)", a.Name, in.Name)
		}
	}
	bound := map[string]bool{}
	var steps []callStep
	for i, call := range a.Calls {
		step, err := resolveCallStep(spec, gf, granted, a.Name, i, call, inputNames, bound)
		if err != nil {
			return actionDescriptor{}, err
		}
		steps = append(steps, step)
		if call.As != "" {
			bound[call.As] = true
		}
	}
	if a.FailWhen != "" {
		if err := respfmt.Validate(a.FailWhen); err != nil {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q: fail-when: %w", a.Name, err)
		}
	}
	var canary *canaryStep
	if a.Canary != nil {
		c, cerr := resolveCanary(spec, gf, granted, a.Name, a.Canary, inputNames, bound)
		if cerr != nil {
			return actionDescriptor{}, cerr
		}
		canary = c
	}
	return actionDescriptor{
		Name:          a.Name,
		VerbName:      actionVerbName(gf.Group, a),
		Describe:      a.Describe,
		Inputs:        a.Inputs,
		Calls:         steps,
		Canary:        canary,
		FailWhen:      a.FailWhen,
		MountVerb:     a.MountVerb,
		MountResource: a.MountResource,
		Combine:       a.IsMount(), // a shadow renders the whole chain, not just the last call
	}, nil
}

// resolveCallStep resolves one call step: its granted leaf, arg validation, and
// the optional compensation, all fail-closed. bound is the prior steps' `as` set.
func resolveCallStep(spec *spec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, action string, i int, call guardfile.Call, inputNames, bound map[string]bool) (callStep, error) {
	leaf, err := resolveGrantedLeaf(spec, gf, granted, action, fmt.Sprintf("call %d", i+1), call.Verb, call.Resource)
	if err != nil {
		return callStep{}, err
	}
	if err := validateCallArgs(action, fmt.Sprintf("call %d", i+1), call.Args, leaf, inputNames, bound); err != nil {
		return callStep{}, err
	}
	step := callStep{Leaf: leaf, Args: call.Args, As: call.As}
	if call.Compensate != nil {
		comp, cerr := resolveCompensation(spec, gf, granted, action, i+1, call, inputNames, bound)
		if cerr != nil {
			return callStep{}, cerr
		}
		step.Compensate = comp
	}
	return step, nil
}

// resolveGrantedLeaf recovers the granted leaf a step names, failing closed on a
// (verb, resource) the Guardfile does not `can`-grant. label sits in errors.
func resolveGrantedLeaf(spec *spec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, action, label, verb, resource string) (opDescriptor, error) {
	g, ok := granted[grantKey{Verb: verb, Resource: resource}]
	if !ok {
		return opDescriptor{}, fmt.Errorf("specverb: action %q %s: %q %q which no `can` grant authorizes (deny-by-default; add `can %s %s`)", action, label, verb, resource, verb, resource)
	}
	leaf, err := resolveDescriptor(spec, gf.Group, g)
	if err != nil {
		return opDescriptor{}, fmt.Errorf("specverb: action %q %s: %w", action, label, err)
	}
	return leaf, nil
}

// resolveCompensation resolves a call's rollback step: a granted leaf whose args
// may reference inputs and the bindings live when it ran. See specverb-rollback.md.
func resolveCompensation(spec *spec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, action string, step int, call guardfile.Call, inputNames, bound map[string]bool) (*compensateStep, error) {
	comp := call.Compensate
	label := fmt.Sprintf("call %d compensate", step)
	leaf, err := resolveGrantedLeaf(spec, gf, granted, action, label, comp.Verb, comp.Resource)
	if err != nil {
		return nil, err
	}
	compBound := bound
	if call.As != "" {
		compBound = cloneSet(bound)
		compBound[call.As] = true // the compensation runs after its call, so that `as` is bound
	}
	if err := validateCallArgs(action, label, comp.Args, leaf, inputNames, compBound); err != nil {
		return nil, err
	}
	return &compensateStep{Leaf: leaf, Args: comp.Args}, nil
}

// resolveCanary resolves an action's canary watch: a granted leaf, its bounds,
// verdict predicates, and args. bound is the forward steps' `as` set. Nil in, nil out.
func resolveCanary(spec *spec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, action string, can *guardfile.Canary, inputNames, bound map[string]bool) (*canaryStep, error) {
	leaf, err := resolveGrantedLeaf(spec, gf, granted, action, "canary", can.Verb, can.Resource)
	if err != nil {
		return nil, err
	}
	every, err := positiveDuration(can.Every)
	if err != nil {
		return nil, fmt.Errorf("specverb: action %q: canary every: %w", action, err)
	}
	window, err := positiveDuration(can.Window)
	if err != nil {
		return nil, fmt.Errorf("specverb: action %q: canary window: %w", action, err)
	}
	if err := respfmt.Validate(can.DegradedWhen); err != nil {
		return nil, fmt.Errorf("specverb: action %q: canary degraded-when: %w", action, err)
	}
	if can.HealthyWhen != "" {
		if err := respfmt.Validate(can.HealthyWhen); err != nil {
			return nil, fmt.Errorf("specverb: action %q: canary healthy-when: %w", action, err)
		}
	}
	if err := validateCallArgs(action, "canary", can.Args, leaf, inputNames, bound); err != nil {
		return nil, err
	}
	return &canaryStep{
		Leaf: leaf, Args: can.Args, Every: every, Window: window,
		DegradedWhen: can.DegradedWhen, HealthyWhen: can.HealthyWhen, As: can.As,
	}, nil
}

// cloneSet returns a shallow copy of a string set, so a caller can extend the
// scope for one step without mutating the shared bound set.
func cloneSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k := range in {
		out[k] = true
	}
	return out
}

// validateCallArgs checks a step's args: a `$ref` names an input or a bound `as`,
// and the arg targets a real path param, flag, or sugar. where labels the step.
func validateCallArgs(action, where string, args []guardfile.ArgBind, leaf opDescriptor, inputNames, bound map[string]bool) error {
	paramNames := map[string]bool{}
	for _, p := range leaf.PathParams {
		paramNames[p] = true
	}
	flagNames := map[string]bool{}
	for _, f := range append(append(append([]fieldFlag{}, leaf.QueryFlags...), leaf.BodyFlags...), leaf.FormFlags...) {
		flagNames[f.Name] = true
	}
	for _, arg := range args {
		if err := validateCallArgRef(action, where, arg, inputNames, bound); err != nil {
			return err
		}
		switch {
		case arg.Name == ownerRepoArg:
			if !paramNames["owner"] || !paramNames["repo"] {
				return fmt.Errorf("specverb: action %q %s: arg %q needs the leaf to take owner+repo path params, but %s %s does not", action, where, ownerRepoArg, leaf.Method, leaf.Path)
			}
		case paramNames[arg.Name] || flagNames[arg.Name]:
			// binds a real path param or flag
		default:
			return fmt.Errorf("specverb: action %q %s: arg %q targets nothing on %s %s (not a path param or flag; fail-closed)", action, where, arg.Name, leaf.Method, leaf.Path)
		}
	}
	return nil
}

// validateCallArgRef checks a `$ref` arg value: a bare `$name` is a declared
// input, a `$step.field` names a bound `as` binding. Literals pass.
func validateCallArgRef(action, where string, arg guardfile.ArgBind, inputNames, bound map[string]bool) error {
	if !strings.HasPrefix(arg.Value, "$") {
		return nil
	}
	ref := strings.TrimPrefix(arg.Value, "$")
	if dot := strings.IndexByte(ref, '.'); dot >= 0 {
		head := ref[:dot]
		if !bound[head] {
			return fmt.Errorf("specverb: action %q %s: arg %q references $%s.* but no step in scope binds `as %s`", action, where, arg.Name, head, head)
		}
		return nil
	}
	if !inputNames[ref] {
		return fmt.Errorf("specverb: action %q %s: arg %q references $%s, which no `input` declares", action, where, arg.Name, ref)
	}
	return nil
}

// runCallAction runs the ordered call sequence: a mid-sequence failure rolls the
// completed steps back and a canary guards the result. See specverb-rollback.md.
func (rt *runtime) runCallAction(ctx context.Context, c *cli.Command, ad actionDescriptor, strVars map[string]string, jmesVars map[string]any) error {
	if c.Bool(flagDryRun) {
		return rt.renderCallActionPlan(ctx, c, ad, strVars)
	}
	bindings := map[string]any{}
	var executed []callStep
	var lastRaw []byte
	for i, step := range ad.Calls {
		resolve := func(v string) (string, error) { return resolveCallArg(v, strVars, bindings) }
		decoded, raw, ferr := rt.stepRun.fireStep(ctx, c, step.Leaf, step.Args, resolve)
		if ferr != nil {
			compensated, rbErr := rt.rollback(ctx, c, executed, strVars, bindings)
			return sequenceFailure(i, step, ferr, compensated, rbErr)
		}
		executed = append(executed, step)
		lastRaw = raw
		if step.As != "" {
			bindings[step.As] = decoded
		}
	}
	if ad.Canary != nil {
		if err := rt.runCanary(ctx, c, ad, executed, strVars, jmesVars, bindings); err != nil {
			return err
		}
	}
	if err := rt.renderCallResult(ad, bindings, lastRaw, c.String(flagOutput)); err != nil {
		return err
	}
	return rt.applyFailWhen(ad, lastRaw, condScope(jmesVars, bindings))
}

// renderCallResult prints a call action's output: a mount action (Combine) emits
// every `as` binding as one object; a named action prints just its final response.
func (rt *runtime) renderCallResult(ad actionDescriptor, bindings map[string]any, lastRaw []byte, output string) error {
	if !ad.Combine {
		return renderFinal(lastRaw, output)
	}
	combined := map[string]any{}
	for _, step := range ad.Calls {
		if step.As != "" {
			combined[step.As] = bindings[step.As]
		}
	}
	raw, err := json.Marshal(combined)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "")
	}
	return renderFinal(raw, output)
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

// renderCallActionPlan prints the planned call sequence (--dry-run), each step's
// resolved request with its compensation, plus the canary block. Fires nothing.
func (rt *runtime) renderCallActionPlan(ctx context.Context, c *cli.Command, ad actionDescriptor, strVars map[string]string) error {
	resolve := func(v string) (string, error) { return resolveCallArgDry(v, strVars), nil }
	plan := make([]any, 0, len(ad.Calls))
	for _, step := range ad.Calls {
		stepPlan, err := rt.stepRun.planStep(ctx, step.Leaf, step.Args, resolve)
		if err != nil {
			return err
		}
		if step.As != "" {
			stepPlan["as"] = step.As
		}
		if step.Compensate != nil {
			compPlan, err := rt.stepRun.planStep(ctx, step.Compensate.Leaf, step.Compensate.Args, resolve)
			if err != nil {
				return err
			}
			stepPlan["compensate"] = compPlan
		}
		plan = append(plan, stepPlan)
	}
	out := map[string]any{"action": ad.Name, "calls": plan}
	if ad.Canary != nil {
		canPlan, err := rt.canaryPlan(ctx, ad.Canary, resolve)
		if err != nil {
			return err
		}
		out["canary"] = canPlan
	}
	return rt.renderPlanJSON(out, c.String(flagOutput))
}

// leafPlan renders one resolved leaf request for a --dry-run plan: the request
// with its auth redacted, no `as`/compensate framing (the caller adds those).
func (rt *runtime) leafPlan(leaf opDescriptor, method, url string, body []byte, contentType string) map[string]any {
	s := map[string]any{
		"leaf":    leaf.Leaf,
		"method":  method,
		"url":     rt.previewURL(url),
		"headers": rt.previewHeaders(body != nil, contentType),
	}
	if body != nil {
		var parsed any
		if err := json.Unmarshal(body, &parsed); err == nil {
			s["body"] = parsed
		}
	}
	return s
}

// renderPlanJSON marshals a plan object and prints it through respfmt, honoring
// --output so a dry-run reads the same as a live response.
func (rt *runtime) renderPlanJSON(plan map[string]any, output string) error {
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
