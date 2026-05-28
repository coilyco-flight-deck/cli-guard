package dispatch

import (
	"strings"
	"testing"
)

// TestParseInteractivePosture pins the watch/consult gate: empty defaults
// to watch, the two valid values round-trip, headless is rejected.
func TestParseInteractivePosture(t *testing.T) {
	cases := []struct {
		in      string
		want    Posture
		wantErr bool
	}{
		{"", PostureWatch, false},
		{"watch", PostureWatch, false},
		{"consult", PostureConsult, false},
		{"  Consult  ", PostureConsult, false},
		{"WATCH", PostureWatch, false},
		{"headless", "", true},
		{"garbage", "", true},
	}
	for _, tc := range cases {
		got, err := parseInteractivePosture(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseInteractivePosture(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseInteractivePosture(%q): unexpected err %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseInteractivePosture(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseInteractivePosture_ErrorNamesValues makes sure a bad value
// names the accepted postures rather than failing opaque.
func TestParseInteractivePosture_ErrorNamesValues(t *testing.T) {
	_, err := parseInteractivePosture("plan")
	if err == nil {
		t.Fatal("parseInteractivePosture(plan) should error, got nil")
	}
	for _, want := range []string{"watch", "consult", "invalid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing substring %q", err.Error(), want)
		}
	}
}

// TestPosturePreamble_Watch verifies watch injects nothing, preserving the
// historical prompt byte-for-byte.
func TestPosturePreamble_Watch(t *testing.T) {
	if got := posturePreamble(PostureWatch); got != "" {
		t.Errorf("posturePreamble(watch) = %q, want empty", got)
	}
}

// TestPosturePreamble_Consult verifies the consult preamble raises the
// interruption budget and disclaims plan mode.
func TestPosturePreamble_Consult(t *testing.T) {
	got := posturePreamble(PostureConsult)
	for _, want := range []string{
		"auto mode",
		"reachable",
		"soft expectation",
		"not a hard stop",
		"not plan",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("consult preamble missing %q, got %q", want, got)
		}
	}
}

// TestPosturePreamble_Headless verifies the detached child is told to
// finish end to end and never wait for input.
func TestPosturePreamble_Headless(t *testing.T) {
	got := posturePreamble(PostureHeadless)
	for _, want := range []string{"end to end", "do not pause", "No operator"} {
		if !strings.Contains(got, want) {
			t.Errorf("headless preamble missing %q, got %q", want, got)
		}
	}
}

// TestInteractivePrompt_ConsultPosture verifies the consult preamble leads
// the prompt with the work instruction following intact.
func TestInteractivePrompt_ConsultPosture(t *testing.T) {
	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 130}
	issue := &ghIssue{
		Number: 130,
		Title:  "add a consult-posture axis",
		URL:    "https://github.com/coilysiren/coily/issues/130",
		State:  "open",
	}
	got := interactivePrompt(ref, issue, false, "/repo/coily", PostureConsult)
	if !strings.HasPrefix(got, "You are in auto mode.") {
		t.Errorf("consult prompt should lead with the posture preamble, got %q", got)
	}
	if !strings.Contains(got, "Work on issue coilysiren/coily#130.") {
		t.Errorf("consult prompt missing the work instruction, got %q", got)
	}
}

// TestSeedPrompt_HeadlessPosture verifies the detached prompt carries the
// never-consult posture ahead of the work instruction and keeps its footer.
func TestSeedPrompt_HeadlessPosture(t *testing.T) {
	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 130, Platform: PlatformForgejo}
	issue := &ghIssue{
		Number: 130,
		Title:  "add a consult-posture axis",
		URL:    "https://forgejo.coilysiren.me/coilysiren/coily/issues/130",
		State:  "open",
	}
	got := seedPrompt(ref, issue)
	if !strings.HasPrefix(got, "Complete this work end to end") {
		t.Errorf("headless seed prompt should lead with the headless posture, got %q", got)
	}
	for _, want := range []string{"Work on Forgejo issue coilysiren/coily#130.", "Workflow rules"} {
		if !strings.Contains(got, want) {
			t.Errorf("headless seed prompt missing %q, got %q", want, got)
		}
	}
}
