package dispatch

// preamble.go holds the per-surface prompt preambles. coily#144 retired the
// orthogonal posture axis; the preamble is keyed off the surface, not a flag.

// headlessPreamble leads the detached headless prompt: no operator is
// watching, so finish end to end and never wait for input.
const headlessPreamble = "Complete this work end to end without stopping to consult. No operator is watching, " +
	"so do not pause for input - make the reasonable call and keep going. They will redirect " +
	"you via the PR or a follow-up if needed."

// consultPreamble leads the consult surface prompt: a raised interruption
// budget. A soft expectation to surface judgment calls, not a plan-mode stop.
const consultPreamble = "You are in auto mode. The operator has this tab open and is reachable. " +
	"When you hit a judgment call worth their input - a naming choice, an irreversible " +
	"schema or data decision, two viable designs with no clear winner - you are encouraged " +
	"to surface it and wait for their answer rather than guess. This is a soft expectation, " +
	"not a hard stop: keep moving on everything that does not need them. This is not plan " +
	"mode; you are not blocked from acting, just welcome to ask."
