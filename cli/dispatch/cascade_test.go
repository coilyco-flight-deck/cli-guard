package dispatch

import (
	"context"
	"strings"
	"testing"
)

// TestResolveCascadeBudget pins the hard depth bound: top-level uses the
// flag, nested decrements the inherited budget, the floor refuses.
func TestResolveCascadeBudget(t *testing.T) {
	cases := []struct {
		name      string
		flagDepth int
		inherited string
		want      int
		wantErr   bool
	}{
		{"top-level default", 3, "", 3, false},
		{"top-level explicit", 2, "", 2, false},
		{"nested decrements", 3, "3", 2, false},
		{"nested decrements again", 3, "2", 1, false},
		{"floor refuses", 3, "1", 0, true},
		{"exhausted refuses", 3, "0", 0, true},
		{"flag narrows inherited", 1, "3", 1, false},
		{"flag below zero rejected", 0, "", 0, true},
		{"flag above max rejected", 6, "", 0, true},
		{"garbage env rejected", 3, "notanint", 0, true},
	}
	for _, tc := range cases {
		got, err := resolveCascadeBudget(tc.flagDepth, tc.inherited)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: resolveCascadeBudget(%d,%q) = %d, want error", tc.name, tc.flagDepth, tc.inherited, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: resolveCascadeBudget(%d,%q): unexpected err %v", tc.name, tc.flagDepth, tc.inherited, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: resolveCascadeBudget(%d,%q) = %d, want %d", tc.name, tc.flagDepth, tc.inherited, got, tc.want)
		}
	}
}

// TestResolveCascadeBudget_FloorErrorIsActionable verifies the exhausted
// error tells the worker to fall back to headless leaves.
func TestResolveCascadeBudget_FloorErrorIsActionable(t *testing.T) {
	_, err := resolveCascadeBudget(3, "1")
	if err == nil {
		t.Fatal("expected floor refusal, got nil")
	}
	for _, want := range []string{"exhausted", "headless"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("floor error %q missing %q", err.Error(), want)
		}
	}
}

// TestCascadePreamble_AboveFloor verifies a worker with budget > 1 is told
// it may spawn both cascade sub-trees and headless leaves.
func TestCascadePreamble_AboveFloor(t *testing.T) {
	got := cascadePreamble(3)
	for _, want := range []string{"budget of 3", "dispatch cascade", "dispatch headless", "do NOT pass --depth"} {
		if !strings.Contains(got, want) {
			t.Errorf("budget-3 preamble missing %q, got %q", want, got)
		}
	}
}

// TestCascadePreamble_AtFloor verifies a budget-1 worker is forbidden from
// spawning further cascades and pointed at headless leaves or direct work.
func TestCascadePreamble_AtFloor(t *testing.T) {
	got := cascadePreamble(1)
	if !strings.Contains(got, "budget is 1") {
		t.Errorf("budget-1 preamble should name the floor, got %q", got)
	}
	if !strings.Contains(got, "may NOT spawn") || !strings.Contains(got, "dispatch headless") {
		t.Errorf("budget-1 preamble must forbid cascade and allow headless, got %q", got)
	}
}

// TestCascadeSeedPrompt verifies the composed prompt leads with the
// recursion posture and carries the issue body and cascade footer.
func TestCascadeSeedPrompt(t *testing.T) {
	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 130, Platform: PlatformForgejo}
	issue := &ghIssue{
		Number: 130,
		Title:  "swarm migration",
		Body:   "migrate repos A B C",
		State:  "open",
		URL:    "https://forgejo.coilysiren.me/example-org/example-repo/issues/130",
	}
	got := cascadeSeedPrompt(ref, issue, "/repo/example-repo", 3)
	for _, want := range []string{
		"cascade worker with a recursion depth budget of 3",
		"Work on Forgejo issue " + issueRefText("example-repo", 130) + ".",
		"migrate repos A B C",
		"do NOT close this issue",
		"closes #",
		"pull --rebase",
		"non-fast-forward",
		"force-push",
		"worktree on branch `dispatch/issue-130-swarm-migration`",
		"git -C /repo/example-repo merge dispatch/issue-130-swarm-migration",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cascadeSeedPrompt missing %q, got %q", want, got)
		}
	}
}

// TestDetachedEnv verifies env composition: nil base + no extra stays nil
// so the child inherits the parent env; extra is appended after base.
func TestDetachedEnv(t *testing.T) {
	if got := detachedEnv(nil, nil); got != nil {
		t.Errorf("detachedEnv(nil,nil) = %v, want nil", got)
	}
	got := detachedEnv([]string{"A=1"}, []string{"B=2"})
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Errorf("detachedEnv append = %v, want [A=1 B=2]", got)
	}
	// Extra-only still produces the extra (base nil).
	if got := detachedEnv(nil, []string{"B=2"}); len(got) != 1 || got[0] != "B=2" {
		t.Errorf("detachedEnv(nil,[B=2]) = %v, want [B=2]", got)
	}
}

// TestDispatchHasCascadeSubverb proves the cascade subverb hangs off the
// dispatch parent alongside headless and interactive.
func TestDispatchHasCascadeSubverb(t *testing.T) {
	d := newTestDispatcher(t)
	cmd := d.Command()
	got := map[string]bool{}
	for _, sub := range cmd.Commands {
		got[sub.Name] = true
	}
	if !got["cascade"] {
		t.Errorf("dispatch parent missing cascade subverb (got %v)", got)
	}
}

// TestDispatchBare_NamesCascade verifies the mode-gate error lists cascade
// so an operator who omits the mode learns it exists.
func TestDispatchBare_NamesCascade(t *testing.T) {
	d := newTestDispatcher(t)
	cmd := d.Command()
	err := cmd.Run(context.Background(), []string{"dispatch", issueRefText("example-repo", 130)})
	if err == nil {
		t.Fatal("bare dispatch <ref> should error")
	}
	if !strings.Contains(err.Error(), "cascade") {
		t.Errorf("mode-gate error = %q, want it to name cascade", err.Error())
	}
}
