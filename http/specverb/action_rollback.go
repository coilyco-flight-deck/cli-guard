// Guarded-rollback primitives: a transport-agnostic step abstraction, a
// reverse-order compensating rollback, and a canary health-window watch that
// drives the rollback path on mid-window degradation. See specverb-rollback.md.

package specverb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/respfmt"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// stepRunner fires (or plans) one resolved action step, the seam that decouples
// the sequence/rollback/canary engine from the transport. See specverb-rollback.md.
type stepRunner interface {
	// fireStep fires a step's granted leaf, returning its decoded response and
	// raw bytes for later data-flow. resolve maps an arg value to its bound string.
	fireStep(ctx context.Context, c *cli.Command, leaf opDescriptor, args []guardfile.ArgBind, resolve func(string) (string, error)) (decoded any, raw []byte, err error)
	// planStep renders a resolved step for a --dry-run, firing nothing.
	planStep(ctx context.Context, leaf opDescriptor, args []guardfile.ArgBind, resolve func(string) (string, error)) (map[string]any, error)
}

// fireStep is the runtime's HTTP implementation of stepRunner: it assembles the
// leaf request and fires it through the audited verb pipeline.
func (rt *runtime) fireStep(ctx context.Context, c *cli.Command, leaf opDescriptor, args []guardfile.ArgBind, resolve func(string) (string, error)) (any, []byte, error) {
	method, url, body, contentType, err := rt.buildCallRequest(ctx, false, leaf, args, resolve)
	if err != nil {
		return nil, nil, err
	}
	return rt.fireCallAudited(ctx, leaf, method, url, body, contentType, c)
}

// planStep is the runtime's HTTP implementation of the dry-run seam: it builds
// the request offline and renders it, resolving no secret and firing nothing.
func (rt *runtime) planStep(ctx context.Context, leaf opDescriptor, args []guardfile.ArgBind, resolve func(string) (string, error)) (map[string]any, error) {
	method, url, body, contentType, err := rt.buildCallRequest(ctx, true, leaf, args, resolve)
	if err != nil {
		return nil, err
	}
	return rt.leafPlan(leaf, method, url, body, contentType), nil
}

// rollback fires each executed step's compensation in reverse. Best-effort: a
// failed compensation is collected, not fatal. See specverb-rollback.md.
func (rt *runtime) rollback(ctx context.Context, c *cli.Command, executed []callStep, strVars map[string]string, bindings map[string]any) (int, error) {
	var done int
	var errs []error
	for k := len(executed) - 1; k >= 0; k-- {
		comp := executed[k].Compensate
		if comp == nil {
			continue
		}
		resolve := func(v string) (string, error) { return resolveCallArg(v, strVars, bindings) }
		if _, _, err := rt.stepRun.fireStep(ctx, c, comp.Leaf, comp.Args, resolve); err != nil {
			errs = append(errs, fmt.Errorf("compensate %s: %w", comp.Leaf.Leaf, err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// sequenceFailure builds the non-zero exit for a failed step, folding in the
// rollback outcome so the operator sees both the trigger and what was undone.
func sequenceFailure(i int, step callStep, stepErr error, compensated int, rbErr error) error {
	msg := fmt.Sprintf("call %d (%s): %v", i+1, step.Leaf.Leaf, stepErr)
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

// runCanary re-samples the canary leaf over its window: degraded-when rolls back,
// healthy-when passes early, an elapsed window passes. See specverb-rollback.md.
func (rt *runtime) runCanary(ctx context.Context, c *cli.Command, ad actionDescriptor, executed []callStep, strVars map[string]string, jmesVars, bindings map[string]any) error {
	can := ad.Canary
	loopCtx, cancel := context.WithTimeout(ctx, can.Window)
	defer cancel()
	ticker := time.NewTicker(can.Every)
	defer ticker.Stop()
	resolve := func(v string) (string, error) { return resolveCallArg(v, strVars, bindings) }
	for {
		decoded, _, err := rt.stepRun.fireStep(loopCtx, c, can.Leaf, can.Args, resolve)
		if err != nil {
			if loopCtx.Err() != nil {
				return nil // the window elapsed mid-sample: the canary held
			}
			return err
		}
		bindings[can.As] = decoded
		verdict, verr := rt.canaryVerdict(can, decoded, condScope(jmesVars, bindings))
		if verr != nil {
			return verr
		}
		switch verdict {
		case canaryDegraded:
			return rt.canaryRollback(ctx, c, ad, executed, strVars, bindings)
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
func (rt *runtime) canaryVerdict(can *canaryStep, decoded any, vars map[string]any) (canaryOutcome, error) {
	degraded, eerr := respfmt.EvalBool(decoded, can.DegradedWhen, vars)
	if eerr != nil {
		return canaryWatching, exitcode.New(exitcode.UserError, "user_error", eerr, "check the canary `degraded-when` JMESPath against the response shape")
	}
	if degraded {
		return canaryDegraded, nil
	}
	if can.HealthyWhen != "" {
		healthy, herr := respfmt.EvalBool(decoded, can.HealthyWhen, vars)
		if herr != nil {
			return canaryWatching, exitcode.New(exitcode.UserError, "user_error", herr, "check the canary `healthy-when` JMESPath against the response shape")
		}
		if healthy {
			return canaryHealthy, nil
		}
	}
	return canaryWatching, nil
}

// canaryRollback fires the executed steps' compensations after the canary saw
// degradation, then exits non-zero naming the rollback outcome.
func (rt *runtime) canaryRollback(ctx context.Context, c *cli.Command, ad actionDescriptor, executed []callStep, strVars map[string]string, bindings map[string]any) error {
	compensated, rbErr := rt.rollback(ctx, c, executed, strVars, bindings)
	base := fmt.Sprintf("action %q: canary degraded (%s)", ad.Name, ad.Canary.DegradedWhen)
	if rbErr != nil {
		return exitcode.New(exitcode.UpstreamFailed, "action_failed",
			fmt.Errorf("%s; rolled back %d step(s), but a compensation failed: %w", base, compensated, rbErr),
			"the canary degraded and a compensating rollback also failed; inspect both above")
	}
	return exitcode.New(exitcode.UpstreamFailed, "canary_degraded",
		fmt.Errorf("%s; rolled back %d completed step(s)", base, compensated),
		"the canary watch saw degradation; the engine rolled the completed steps back")
}

// canaryPlan renders the canary block for a --dry-run: the watched leaf, the
// bounds, and the verdict predicates. resolve leaves $step.field as a placeholder.
func (rt *runtime) canaryPlan(ctx context.Context, can *canaryStep, resolve func(string) (string, error)) (map[string]any, error) {
	leafPlan, err := rt.stepRun.planStep(ctx, can.Leaf, can.Args, resolve)
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

// condScope merges the coerced inputs and the step bindings into one JMESPath
// variable scope; a step binding wins over an equally-named input.
func condScope(jmesVars, bindings map[string]any) map[string]any {
	out := make(map[string]any, len(jmesVars)+len(bindings))
	for k, v := range jmesVars {
		out[k] = v
	}
	for k, v := range bindings {
		out[k] = v
	}
	return out
}
