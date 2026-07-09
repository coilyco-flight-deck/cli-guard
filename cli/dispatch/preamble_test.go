package dispatch

import (
	"strings"
	"testing"
)

// TestConsultPreamble verifies the consult preamble raises the
// interruption budget and disclaims plan mode.
func TestConsultPreamble(t *testing.T) {
	for _, want := range []string{
		"auto mode",
		"reachable",
		"soft expectation",
		"not a hard stop",
		"not plan",
	} {
		if !strings.Contains(consultPreamble, want) {
			t.Errorf("consult preamble missing %q, got %q", want, consultPreamble)
		}
	}
}

// TestHeadlessPreamble verifies the detached child is told to finish end
// to end and never wait for input.
func TestHeadlessPreamble(t *testing.T) {
	for _, want := range []string{"end to end", "do not pause", "No operator"} {
		if !strings.Contains(headlessPreamble, want) {
			t.Errorf("headless preamble missing %q, got %q", want, headlessPreamble)
		}
	}
}

// TestInteractivePrompt_ConsultSurface verifies the consult preamble leads
// the prompt with the work instruction following intact.
func TestInteractivePrompt_ConsultSurface(t *testing.T) {
	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 144}
	issue := &ghIssue{
		Number: 144,
		Title:  "make consult a 4th positional surface",
		URL:    "https://github.com/example-org/example-repo/issues/144",
		State:  "open",
	}
	got := interactivePrompt(ref, issue, consultPreamble)
	if !strings.HasPrefix(got, "You are in auto mode.") {
		t.Errorf("consult prompt should lead with the consult preamble, got %q", got)
	}
	if !strings.Contains(got, "Work on issue "+issueRefText("example-repo", 144)+".") {
		t.Errorf("consult prompt missing the work instruction, got %q", got)
	}
}

// TestInteractivePrompt_InteractiveSurface verifies the interactive (watch)
// surface injects no preamble, leading straight with the work instruction.
func TestInteractivePrompt_InteractiveSurface(t *testing.T) {
	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 144}
	issue := &ghIssue{
		Number: 144,
		Title:  "make consult a 4th positional surface",
		URL:    "https://github.com/example-org/example-repo/issues/144",
		State:  "open",
	}
	got := interactivePrompt(ref, issue, "")
	if !strings.HasPrefix(got, "Work on issue "+issueRefText("example-repo", 144)+".") {
		t.Errorf("interactive prompt should lead with the work instruction, got %q", got)
	}
}

// TestSeedPrompt_HeadlessPreamble verifies the detached prompt carries the
// never-consult preamble ahead of the work instruction and keeps its footer.
func TestSeedPrompt_HeadlessPreamble(t *testing.T) {
	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 144, Platform: PlatformForgejo}
	issue := &ghIssue{
		Number: 144,
		Title:  "make consult a 4th positional surface",
		URL:    "https://forgejo.coilysiren.me/example-org/example-repo/issues/144",
		State:  "open",
	}
	got := seedPrompt(ref, issue, "/repo/example-repo")
	if !strings.HasPrefix(got, "Complete this work end to end") {
		t.Errorf("headless seed prompt should lead with the headless preamble, got %q", got)
	}
	for _, want := range []string{"Work on Forgejo issue " + issueRefText("example-repo", 144) + ".", "Workflow rules"} {
		if !strings.Contains(got, want) {
			t.Errorf("headless seed prompt missing %q, got %q", want, got)
		}
	}
}
