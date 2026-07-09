package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"github.com/urfave/cli/v3"
)

// newPreflightDispatcher builds a Dispatcher whose fetcher always returns an
// open issue, so resolveDispatchIssue reaches the Preflight seam under test.
func newPreflightDispatcher(t *testing.T, pf func(context.Context, *IssueRef, *Issue) (PreflightResult, error)) *Dispatcher {
	t.Helper()
	d, err := New(Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		RepoPath:     func(string) (string, error) { return "/tmp", nil },
		WorktreeRoot: func() (string, error) { return "/tmp", nil },
		LogRoot:      func() (string, error) { return "/tmp", nil },
		Preflight:    pf,
		IssueFetcher: func(_ context.Context, _, repo string, n int) (*Issue, error) {
			return &Issue{Number: n, Title: "t", State: "open", URL: "https://example/" + repo}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// TestPreflight_GoProceeds proves an explicit GO lets the dispatch resolve.
func TestPreflight_GoProceeds(t *testing.T) {
	d := newPreflightDispatcher(t, func(context.Context, *IssueRef, *Issue) (PreflightResult, error) {
		return PreflightResult{Verdict: VerdictGo}, nil
	})
	if _, _, err := d.resolveDispatchIssue(context.Background(), issueRefText("example-repo", 1), levelConsult); err != nil {
		t.Fatalf("GO verdict should resolve, got: %v", err)
	}
}

// TestPreflight_Refusals pins each non-GO verdict and that the reason is
// folded into the refusal.
func TestPreflight_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		verdict Verdict
		reason  string
		want    string
	}{
		{"no-go", VerdictNoGo, "milestone closed", "NO-GO"},
		{"wrong-repo", VerdictWrongRepo, "serves ward only", "WRONG-REPO"},
		{"empty fails closed", Verdict(""), "", "no GO verdict"},
		{"unknown fails closed", Verdict("maybe"), "", "no GO verdict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newPreflightDispatcher(t, func(context.Context, *IssueRef, *Issue) (PreflightResult, error) {
				return PreflightResult{Verdict: tc.verdict, Reason: tc.reason}, nil
			})
			_, _, err := d.resolveDispatchIssue(context.Background(), issueRefText("example-repo", 1), levelConsult)
			if err == nil {
				t.Fatalf("%s verdict should refuse, got nil", tc.verdict)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if tc.reason != "" && !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error = %q, want it to fold in reason %q", err.Error(), tc.reason)
			}
		})
	}
}

// TestPreflight_ErrorAborts proves a returned error stops the dispatch.
func TestPreflight_ErrorAborts(t *testing.T) {
	d := newPreflightDispatcher(t, func(context.Context, *IssueRef, *Issue) (PreflightResult, error) {
		return PreflightResult{}, errors.New("verdict backend down")
	})
	_, _, err := d.resolveDispatchIssue(context.Background(), issueRefText("example-repo", 1), levelConsult)
	if err == nil || !strings.Contains(err.Error(), "verdict backend down") {
		t.Fatalf("error from Preflight should abort with its message, got: %v", err)
	}
}

// TestPreflight_SeesResolvedRefAndIssue proves the stage receives the
// owner-checked ref and the fetched issue.
func TestPreflight_SeesResolvedRefAndIssue(t *testing.T) {
	var gotRef *IssueRef
	var gotIssue *Issue
	d := newPreflightDispatcher(t, func(_ context.Context, ref *IssueRef, issue *Issue) (PreflightResult, error) {
		gotRef, gotIssue = ref, issue
		return PreflightResult{Verdict: VerdictGo}, nil
	})
	if _, _, err := d.resolveDispatchIssue(context.Background(), issueRefText("example-repo", 7), levelConsult); err != nil {
		t.Fatalf("resolveDispatchIssue: %v", err)
	}
	if gotRef == nil || gotRef.Owner != "example-org" || gotRef.Number != 7 {
		t.Errorf("Preflight ref = %+v, want example-org/... issue 7", gotRef)
	}
	if gotIssue == nil || gotIssue.Number != 7 {
		t.Errorf("Preflight issue = %+v, want issue 7", gotIssue)
	}
}

// TestPreflight_NotReachedForForeignOwner proves the consumer stage runs
// only after dispatch's own owner gate, never for a refused owner.
func TestPreflight_NotReachedForForeignOwner(t *testing.T) {
	called := false
	d := newPreflightDispatcher(t, func(context.Context, *IssueRef, *Issue) (PreflightResult, error) {
		called = true
		return PreflightResult{Verdict: VerdictGo}, nil
	})
	if _, _, err := d.resolveDispatchIssue(context.Background(), ownerRefText("someoneelse/repo", 1), levelConsult); err == nil {
		t.Fatal("foreign owner should be refused before pre-flight")
	}
	if called {
		t.Error("Preflight ran for a foreign owner; the owner gate must come first")
	}
}

// TestParseIssueRef_Exported proves the exported parser gives consumers the
// same normalization dispatch uses internally, including bare refs and URLs.
func TestParseIssueRef_Exported(t *testing.T) {
	cases := []struct {
		name      string
		baseURL   string
		in        string
		wantOwner string
		wantRepo  string
		wantNum   int
		wantPlat  Platform
	}{
		{"shortform", "", issueRefText("example-repo", 99), "example-org", "example-repo", 99, ""},
		{"bare", "", bareRefText(99), "", "", 99, ""},
		{"github-url", "", "https://github.com/example-org/example-repo/issues/99", "example-org", "example-repo", 99, PlatformGitHub},
		{"forgejo-url", "https://forgejo.example", "forgejo.example/example-org/example-repo/issues/99", "example-org", "example-repo", 99, PlatformForgejo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ParseIssueRef(tc.baseURL, tc.in)
			if err != nil {
				t.Fatalf("ParseIssueRef(%q): %v", tc.in, err)
			}
			if ref.Owner != tc.wantOwner || ref.Repo != tc.wantRepo || ref.Number != tc.wantNum || ref.Platform != tc.wantPlat {
				t.Fatalf("ParseIssueRef(%q) = %+v, want %+v/%+v#%d platform %q", tc.in, ref, tc.wantOwner, tc.wantRepo, tc.wantNum, tc.wantPlat)
			}
		})
	}

	d := newTestDispatcher(t)
	ref, err := d.ParseIssueRef(issueRefText("example-repo", 99))
	if err != nil {
		t.Fatalf("Dispatcher.ParseIssueRef: %v", err)
	}
	if ref.Owner != "example-org" || ref.Repo != "example-repo" || ref.Number != 99 {
		t.Errorf("Dispatcher.ParseIssueRef = %+v, want %s", ref, issueRefText("example-repo", 99))
	}
}
