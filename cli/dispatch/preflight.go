package dispatch

import (
	"context"
	"fmt"
	"strings"
)

// preflight.go is the consumer-pluggable pre-flight verdict stage, run
// after dispatch's owner + open-state checks. See docs/dispatch-backends.md.

// Verdict is a consumer pre-flight outcome.
type Verdict string

const (
	// VerdictGo proceeds with the dispatch.
	VerdictGo Verdict = "go"
	// VerdictNoGo refuses the dispatch outright (consumer veto).
	VerdictNoGo Verdict = "no-go"
	// VerdictWrongRepo refuses because the ref targets a repo this host
	// does not serve - distinct from NO-GO so a caller can route it.
	VerdictWrongRepo Verdict = "wrong-repo"
)

// PreflightResult is what Config.Preflight returns: a Verdict plus an
// optional human-readable reason folded into the refusal message.
type PreflightResult struct {
	Verdict Verdict
	Reason  string
}

// runPreflight runs the configured pre-flight stage, fail-closed: only
// VerdictGo proceeds, a nil Preflight is an implicit GO.
func (d *Dispatcher) runPreflight(ctx context.Context, ref *IssueRef, issue *Issue) error {
	if d.cfg.Preflight == nil {
		return nil
	}
	res, err := d.cfg.Preflight(ctx, ref, issue)
	if err != nil {
		return fmt.Errorf("dispatch: pre-flight verdict for %s: %w", ref, err)
	}
	switch res.Verdict {
	case VerdictGo:
		return nil
	case VerdictWrongRepo:
		return fmt.Errorf("dispatch: pre-flight verdict WRONG-REPO for %s%s", ref, reasonSuffix(res.Reason))
	case VerdictNoGo:
		return fmt.Errorf("dispatch: pre-flight verdict NO-GO for %s%s", ref, reasonSuffix(res.Reason))
	default:
		// Fail closed: an empty or unknown verdict must not slip through
		// as a silent GO.
		return fmt.Errorf("dispatch: pre-flight returned no GO verdict (%q) for %s%s", res.Verdict, ref, reasonSuffix(res.Reason))
	}
}

// reasonSuffix renders a trailing " (<reason>)" when the consumer gave a
// reason, and nothing when it did not.
func reasonSuffix(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}
