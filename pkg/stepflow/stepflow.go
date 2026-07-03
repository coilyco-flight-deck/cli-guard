// Package stepflow is the transport-agnostic engine behind complex-action call
// sequences: ordered steps, compensating rollback, and the canary health watch.
// A dialect (specverb HTTP, execverb exec/ssh) resolves its granted leaves into
// Steps and supplies a Runner; the engine owns ordering, reverse-order
// compensation, and the canary verdict loop. See docs/specverb-rollback.md.
package stepflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/respfmt"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// Leaf is one resolved, granted step target. The engine only labels it; the
// Runner that resolved it knows its concrete type (HTTP op, exec grant).
type Leaf interface {
	// Label names the leaf in operator-facing errors, e.g. "create snapshot".
	Label() string
}

// Step is one resolved forward step: a granted leaf, its arg bindings, the
// optional `as` binding name, and the optional compensation undoing it.
type Step struct {
	Leaf       Leaf
	Args       []guardfile.ArgBind
	As         string
	Compensate *Compensation
}

// Compensation is a step's rollback: a granted leaf fired (with Args) to undo
// its parent step when a later step or the canary fails.
type Compensation struct {
	Leaf Leaf
	Args []guardfile.ArgBind
}

// Canary re-samples a granted health leaf every Every up to Window after the
// forward steps, driving the rollback path on DegradedWhen mid-window.
type Canary struct {
	Leaf         Leaf
	Args         []guardfile.ArgBind
	Every        time.Duration
	Window       time.Duration
	DegradedWhen string // JMESPath; truthy mid-window triggers rollback
	HealthyWhen  string // optional JMESPath; truthy ends the watch early, clean
	As           string // binding name for each sampled response
}

// Resolve maps one arg value (a literal, $input, or $step.field reference)
// onto its bound string. See ResolveArg.
type Resolve func(string) (string, error)

// Runner fires (or plans) one resolved step: the transport seam. Fire errors
// fail the step; Sample tolerates a degraded probe so the canary can judge it.
type Runner interface {
	// Fire runs a forward or compensation step, returning the decoded response
	// (for $as data-flow) and its raw bytes. An error marks the step failed.
	Fire(ctx context.Context, c *cli.Command, leaf Leaf, args []guardfile.ArgBind,
		resolve Resolve) (decoded any, raw []byte, err error)
	// Sample takes one canary observation. Only a transport-level failure (the
	// probe could not run at all) is an error; a degraded result is data.
	Sample(ctx context.Context, c *cli.Command, leaf Leaf, args []guardfile.ArgBind,
		resolve Resolve) (decoded any, raw []byte, err error)
	// Plan renders a resolved step for a --dry-run, firing nothing.
	Plan(ctx context.Context, leaf Leaf, args []guardfile.ArgBind,
		resolve Resolve) (map[string]any, error)
}

// Run executes the ordered steps, then the optional canary. A step failure
// compensates the completed steps in reverse; the canary drives the same path
// on degradation or a blind (unobservable) sample. Bindings and the last
// response come back for the caller's rendering and fail-when.
func Run(ctx context.Context, c *cli.Command, name string, steps []Step, can *Canary,
	strVars map[string]string, jmesVars map[string]any, r Runner,
) (bindings map[string]any, lastRaw []byte, err error) {
	bindings = map[string]any{}
	var executed []Step
	for i, step := range steps {
		resolve := func(v string) (string, error) { return ResolveArg(v, strVars, bindings) }
		decoded, raw, ferr := r.Fire(ctx, c, step.Leaf, step.Args, resolve)
		if ferr != nil {
			compensated, rbErr := rollback(ctx, c, executed, strVars, bindings, r)
			return bindings, lastRaw, sequenceFailure(i, step, ferr, compensated, rbErr)
		}
		executed = append(executed, step)
		lastRaw = raw
		if step.As != "" {
			bindings[step.As] = decoded
		}
	}
	if can != nil {
		if cerr := runCanary(ctx, c, name, can, executed, strVars, jmesVars, bindings, r); cerr != nil {
			return bindings, lastRaw, cerr
		}
	}
	return bindings, lastRaw, nil
}

// rollback fires each executed step's compensation in reverse. Best-effort: a
// failed compensation is collected, not fatal, so the rest still run.
func rollback(ctx context.Context, c *cli.Command, executed []Step,
	strVars map[string]string, bindings map[string]any, r Runner,
) (int, error) {
	var done int
	var errs []error
	for k := len(executed) - 1; k >= 0; k-- {
		comp := executed[k].Compensate
		if comp == nil {
			continue
		}
		resolve := func(v string) (string, error) { return ResolveArg(v, strVars, bindings) }
		if _, _, err := r.Fire(ctx, c, comp.Leaf, comp.Args, resolve); err != nil {
			errs = append(errs, fmt.Errorf("compensate %s: %w", comp.Leaf.Label(), err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// sequenceFailure builds the non-zero exit for a failed step, folding in the
// rollback outcome so the operator sees the trigger and what was undone.
func sequenceFailure(i int, step Step, stepErr error, compensated int, rbErr error) error {
	msg := fmt.Sprintf("call %d (%s): %v", i+1, step.Leaf.Label(), stepErr)
	switch {
	case rbErr != nil:
		return exitcode.New(exitcode.UpstreamFailed, "action_failed",
			fmt.Errorf("%s; rolled back %d step(s), but a compensation failed: %w", msg, compensated, rbErr),
			"the step failed and a compensating rollback also failed; inspect both above")
	case compensated > 0:
		return exitcode.New(exitcode.UpstreamFailed, "action_rolled_back",
			fmt.Errorf("%s; rolled back %d completed step(s)", msg, compensated),
			"a step failed; the engine compensated the completed steps in reverse order")
	default:
		return exitcode.New(exitcode.UpstreamFailed, "action_failed",
			errors.New(msg), "a step in the action sequence failed; nothing after it ran")
	}
}

// runCanary re-samples the canary leaf over its window: degraded-when rolls
// back, healthy-when passes early, an elapsed window passes. A sample the
// runner cannot take at all (blind) also rolls back: an unobservable target is
// never left promoted. See docs/specverb-rollback.md.
func runCanary(ctx context.Context, c *cli.Command, name string, can *Canary,
	executed []Step, strVars map[string]string, jmesVars, bindings map[string]any, r Runner,
) error {
	loopCtx, cancel := context.WithTimeout(ctx, can.Window)
	defer cancel()
	ticker := time.NewTicker(can.Every)
	defer ticker.Stop()
	resolve := func(v string) (string, error) { return ResolveArg(v, strVars, bindings) }
	for {
		decoded, _, err := r.Sample(loopCtx, c, can.Leaf, can.Args, resolve)
		if err != nil {
			if loopCtx.Err() != nil {
				return nil // the window elapsed mid-sample: the canary held
			}
			return canaryFailure(ctx, c, name, "canary_blind",
				fmt.Sprintf("canary sample failed (%v)", err), executed, strVars, bindings, r)
		}
		bindings[can.As] = decoded
		verdict, verr := canaryVerdict(can, decoded, CondScope(jmesVars, bindings))
		if verr != nil {
			return verr
		}
		switch verdict {
		case canaryDegraded:
			return canaryFailure(ctx, c, name, "canary_degraded",
				fmt.Sprintf("canary degraded (%s)", can.DegradedWhen), executed, strVars, bindings, r)
		case canaryHealthy:
			return nil
		case canaryWatching:
			// keep sampling until the window elapses
		}
		select {
		case <-loopCtx.Done():
			return nil // window elapsed with no degradation: the canary held
		case <-ticker.C:
		}
	}
}

// canaryOutcome is one tick's verdict: keep watching, roll back, or pass early.
type canaryOutcome int

const (
	canaryWatching canaryOutcome = iota
	canaryDegraded
	canaryHealthy
)

// canaryVerdict evaluates one sample: degraded-when first (it drives rollback),
// then the optional healthy-when early exit. vars carries inputs and bindings.
func canaryVerdict(can *Canary, decoded any, vars map[string]any) (canaryOutcome, error) {
	degraded, eerr := respfmt.EvalBool(decoded, can.DegradedWhen, vars)
	if eerr != nil {
		return canaryWatching, exitcode.New(exitcode.UserError, "user_error", eerr,
			"check the canary `degraded-when` JMESPath against the response shape")
	}
	if degraded {
		return canaryDegraded, nil
	}
	if can.HealthyWhen != "" {
		healthy, herr := respfmt.EvalBool(decoded, can.HealthyWhen, vars)
		if herr != nil {
			return canaryWatching, exitcode.New(exitcode.UserError, "user_error", herr,
				"check the canary `healthy-when` JMESPath against the response shape")
		}
		if healthy {
			return canaryHealthy, nil
		}
	}
	return canaryWatching, nil
}

// canaryFailure fires the executed steps' compensations after a degraded or
// blind canary, then exits non-zero naming the rollback outcome. code is the
// exit tag (canary_degraded, canary_blind); reason names the trigger.
func canaryFailure(ctx context.Context, c *cli.Command, name, code, reason string,
	executed []Step, strVars map[string]string, bindings map[string]any, r Runner,
) error {
	compensated, rbErr := rollback(ctx, c, executed, strVars, bindings, r)
	base := fmt.Sprintf("action %q: %s", name, reason)
	if rbErr != nil {
		return exitcode.New(exitcode.UpstreamFailed, "action_failed",
			fmt.Errorf("%s; rolled back %d step(s), but a compensation failed: %w", base, compensated, rbErr),
			"the canary tripped and a compensating rollback also failed; inspect both above")
	}
	return exitcode.New(exitcode.UpstreamFailed, code,
		fmt.Errorf("%s; rolled back %d completed step(s)", base, compensated),
		"the canary watch tripped; the engine rolled the completed steps back")
}

// PlanCalls renders each step (with its compensation) for a --dry-run through
// the Runner's Plan, firing nothing. resolve leaves $step.field placeholders.
func PlanCalls(ctx context.Context, steps []Step, resolve Resolve, r Runner) ([]any, error) {
	plan := make([]any, 0, len(steps))
	for _, step := range steps {
		stepPlan, err := r.Plan(ctx, step.Leaf, step.Args, resolve)
		if err != nil {
			return nil, err
		}
		if step.As != "" {
			stepPlan["as"] = step.As
		}
		if step.Compensate != nil {
			compPlan, cerr := r.Plan(ctx, step.Compensate.Leaf, step.Compensate.Args, resolve)
			if cerr != nil {
				return nil, cerr
			}
			stepPlan["compensate"] = compPlan
		}
		plan = append(plan, stepPlan)
	}
	return plan, nil
}

// PlanCanary renders the canary block for a --dry-run: the watched leaf, the
// bounds, and the verdict predicates. Fires nothing.
func PlanCanary(ctx context.Context, can *Canary, resolve Resolve, r Runner) (map[string]any, error) {
	leafPlan, err := r.Plan(ctx, can.Leaf, can.Args, resolve)
	if err != nil {
		return nil, err
	}
	plan := map[string]any{
		"watch":         leafPlan,
		"every":         can.Every.String(),
		"window":        can.Window.String(),
		"degraded_when": can.DegradedWhen,
		"as":            can.As,
	}
	if can.HealthyWhen != "" {
		plan["healthy_when"] = can.HealthyWhen
	}
	return plan, nil
}

// ResolveArg resolves a step arg value: a literal verbatim, a `$input` from
// the bound inputs, or a `$step.field` projected out of a prior step's response.
func ResolveArg(value string, inputs map[string]string, bindings map[string]any) (string, error) {
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
		return ScalarToString(out), nil
	}
	if v, ok := inputs[ref]; ok {
		return v, nil
	}
	return "", fmt.Errorf("$%s is not set (an optional input that was not supplied)", ref)
}

// ResolveArgDry resolves an arg value for a --dry-run plan: literals and bound
// inputs resolve, a $step.field stays a `${ref}` placeholder (nothing fired).
func ResolveArgDry(value string, inputs map[string]string) string {
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

// CondScope merges the coerced inputs and the step bindings into one JMESPath
// variable scope; a step binding wins over an equally-named input.
func CondScope(jmesVars, bindings map[string]any) map[string]any {
	out := make(map[string]any, len(jmesVars)+len(bindings))
	for k, v := range jmesVars {
		out[k] = v
	}
	for k, v := range bindings {
		out[k] = v
	}
	return out
}

// CoerceScalar lowers an input string to a number where it parses as one (so
// `run_number==$run` compares number to number), otherwise the string itself.
func CoerceScalar(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return float64(i) // JSON numbers decode to float64; match that
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// ScalarToString renders a JMESPath scalar result for use as an argv or URL
// value: whole-number floats lose the trailing ".0", others format naturally.
func ScalarToString(v any) string {
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
