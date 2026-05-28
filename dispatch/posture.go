package dispatch

import (
	"fmt"
	"sort"
	"strings"
)

// Posture is the consult-posture axis: how readily a dispatched agent
// stops to involve the operator. Orthogonal to surface. coily#130.
type Posture string

const (
	// PostureHeadless never consults: complete the work end to end. The
	// posture the detached headless surface always runs under.
	PostureHeadless Posture = "headless"

	// PostureWatch proceeds in auto mode; operator may watch but is not
	// consulted. Historical interactive behavior, default for that surface.
	PostureWatch Posture = "watch"

	// PostureConsult proceeds but with a raised interruption budget:
	// encouraged to surface judgment calls. A soft expectation, not plan mode.
	PostureConsult Posture = "consult"
)

// interactivePostures are the postures selectable on the interactive
// surface. headless is excluded: a detached run has no operator to consult.
var interactivePostures = []Posture{PostureWatch, PostureConsult}

// parseInteractivePosture validates a --posture flag for the interactive
// surface. Empty defaults to watch (the historical behavior).
func parseInteractivePosture(s string) (Posture, error) {
	if strings.TrimSpace(s) == "" {
		return PostureWatch, nil
	}
	p := Posture(strings.ToLower(strings.TrimSpace(s)))
	for _, valid := range interactivePostures {
		if p == valid {
			return p, nil
		}
	}
	names := make([]string, len(interactivePostures))
	for i, valid := range interactivePostures {
		names[i] = string(valid)
	}
	sort.Strings(names)
	return "", fmt.Errorf("invalid --posture %q (valid values: %s)", s, strings.Join(names, " | "))
}

// posturePreamble is the per-posture prompt preamble prepended to the
// seeded work instruction. Budget lives in language, not a flag. coily#130.
func posturePreamble(p Posture) string {
	switch p {
	case PostureConsult:
		return "You are in auto mode. The operator has this tab open and is reachable. " +
			"When you hit a judgment call worth their input - a naming choice, an irreversible " +
			"schema or data decision, two viable designs with no clear winner - you are encouraged " +
			"to surface it and wait for their answer rather than guess. This is a soft expectation, " +
			"not a hard stop: keep moving on everything that does not need them. This is not plan " +
			"mode; you are not blocked from acting, just welcome to ask."
	case PostureHeadless:
		return "Complete this work end to end without stopping to consult. No operator is watching, " +
			"so do not pause for input - make the reasonable call and keep going. They will redirect " +
			"you via the PR or a follow-up if needed."
	case PostureWatch:
		return ""
	}
	return ""
}
