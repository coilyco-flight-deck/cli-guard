package dispatch

import (
	"strings"
	"testing"
)

func TestBranchSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "fix the thing", "fix-the-thing"},
		{"conventional commit", "feat(dispatch): support X", "feat-dispatch-support-x"},
		{"parens and colon", "dispatch: two modes (main / worktree)", "dispatch-two-modes-main-worktree"},
		{"uppercase", "Fix The Bug", "fix-the-bug"},
		{"collapse runs", "a---b   c", "a-b-c"},
		{"leading/trailing junk", "  ::title::  ", "title"},
		{"digits kept", "bump v2.44.1 formula", "bump-v2-44-1-formula"},
		{"unicode dropped", "café neïve", "caf-ne-ve"},
		{"empty", "", ""},
		{"all punctuation", ":;(){}|&", ""},
		{"shell metachars", "a;b|c&d`e$f", "a-b-c-d-e-f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := branchSlug(tc.in); got != tc.want {
				t.Errorf("branchSlug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBranchSlugLengthCap(t *testing.T) {
	long := "this is a very long issue title that keeps going and going well past the limit"
	got := branchSlug(long)
	if len(got) > maxBranchSlugLen {
		t.Errorf("branchSlug len = %d (%q), want <= %d", len(got), got, maxBranchSlugLen)
	}
	// Cap lands on a dash boundary: no trailing dash, no partial trailing word
	// beyond the cap.
	if got == "" || got[len(got)-1] == '-' {
		t.Errorf("branchSlug(long) = %q, want non-empty with no trailing dash", got)
	}
}

// TestBranchSlugProducesValidRefComponents guards the sink contract: every
// slug is a legal git branch-name component. Long input exercises the cap.
func TestBranchSlugProducesValidRefComponents(t *testing.T) {
	inputs := []string{
		"feat(x): y",
		"a.b.c..d",
		"~^:?*[\\@{",
		"supercalifragilisticexpialidocioussupercalifragilistic",
	}
	for _, in := range inputs {
		s := branchSlug(in)
		for _, r := range s {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !ok {
				t.Errorf("branchSlug(%q) = %q contains illegal ref byte %q", in, s, r)
			}
		}
		if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
			t.Errorf("branchSlug(%q) = %q has a leading/trailing dash", in, s)
		}
	}
}
