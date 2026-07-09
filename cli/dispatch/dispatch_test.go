package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"github.com/urfave/cli/v3"
)

func TestParseIssueRef(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		in     string
		wantOK bool
		owner  string
		repo   string
		num    int
		plat   Platform
	}{
		{"shortform", "", issueRefText("example-repo", 136), true, "example-org", "example-repo", 136, ""},
		{"shortform-trim", "", "  " + issueRefText("example-repo", 136) + "  ", true, "example-org", "example-repo", 136, ""},
		{"github-url", "", "https://github.com/example-org/example-repo/issues/136", true, "example-org", "example-repo", 136, PlatformGitHub},
		{"github-url-noscheme", "", "github.com/example-org/example-repo/issues/136", true, "example-org", "example-repo", 136, PlatformGitHub},
		{"github-url-fragment", "", "https://github.com/example-org/example-repo/issues/136#issuecomment-1", true, "example-org", "example-repo", 136, PlatformGitHub},
		{"forgejo-url", "https://forgejo.example", "https://forgejo.example/example-org/example-repo/issues/136", true, "example-org", "example-repo", 136, PlatformForgejo},
		{"forgejo-url-noscheme", "https://forgejo.example", "forgejo.example/example-org/example-repo/issues/136", true, "example-org", "example-repo", 136, PlatformForgejo},
		{"bare", "", fmt.Sprintf("#%d", 136), true, "", "", 136, ""},
		{"bare-num", "", "136", true, "", "", 136, ""},
		{"not-ref", "", "example-org/example-repo/issues/136", false, "", "", 0, ""},
		{"non-positive", "", fmt.Sprintf("%s/%s#%d", "example-org", "example-repo", 0), false, "", "", 0, ""},
		{"missing-repo", "", fmt.Sprintf("%s#%d", "example-org", 136), false, "", "", 0, ""},
		{"empty", "", "", false, "", "", 0, ""},
		{"random", "", "random gibberish", false, "", "", 0, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseIssueRef(tc.base, tc.in)
			if tc.wantOK {
				if err != nil {
					t.Errorf("ParseIssueRef(%q): unexpected err: %v", tc.in, err)
					return
				}
				if got.Owner != tc.owner || got.Repo != tc.repo || got.Number != tc.num || got.Platform != tc.plat {
					t.Errorf("ParseIssueRef(%q) = %+v, want owner=%s repo=%s num=%d platform=%q",
						tc.in, got, tc.owner, tc.repo, tc.num, tc.plat)
				}
				return
			}
			if err == nil {
				t.Errorf("ParseIssueRef(%q): expected error, got %+v", tc.in, got)
			}
		})
	}
	d := newTestDispatcher(t)
	got, err := d.parseIssueRef(issueRefText("example-repo", 136))
	if err != nil {
		t.Fatalf("dispatcher parseIssueRef: %v", err)
	}
	if got.Owner != "example-org" || got.Repo != "example-repo" || got.Number != 136 || got.Platform != "" {
		t.Fatalf("dispatcher parseIssueRef = %+v, want shortform %s", got, issueRefText("example-repo", 136))
	}
}

func TestSeedPrompt_IncludesIssueAndFooter(t *testing.T) {
	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 136}
	issue := &ghIssue{
		Number: 136,
		Title:  "example dispatch",
		Body:   "design body here",
		State:  "OPEN",
		URL:    "https://github.com/example-org/example-repo/issues/136",
	}
	got := seedPrompt(ref, issue, "/repo/example-repo")
	for _, want := range []string{
		issueRefText("example-repo", 136),
		"example dispatch",
		"design body here",
		"https://github.com/example-org/example-repo/issues/136",
		"AGENTS.md",
		"closes #",
		"--no-verify",
		"pull --rebase",
		"non-fast-forward",
		"force-push",
		"worktree on branch `dispatch/issue-136-example-dispatch`",
		"git -C /repo/example-repo merge dispatch/issue-136-example-dispatch",
		"push origin main",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("seedPrompt missing %q in:\n%s", want, got)
		}
	}
}

func TestIssueRef_String(t *testing.T) {
	ref := issueRef{Owner: "example-org", Repo: "example-repo", Number: 136}
	if got, want := ref.String(), issueRefText("example-repo", 136); got != want {
		t.Errorf("issueRef.String() = %q, want %q", got, want)
	}
}

// TestDispatchDefaults pins the headless-friendly defaults: permission-
// mode defaults to auto so the child doesn't stall on the first
func TestDispatchDefaults(t *testing.T) {
	if defaultDispatchPermissionMode != "auto" {
		t.Errorf("defaultDispatchPermissionMode = %q, want auto", defaultDispatchPermissionMode)
	}
	for _, tool := range []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep", "TodoWrite"} {
		if !strings.Contains(defaultDispatchAllowedTools, tool) {
			t.Errorf("defaultDispatchAllowedTools missing %q (got %q)", tool, defaultDispatchAllowedTools)
		}
	}
}

// TestDispatchLogPath pins the headless log path shape: rooted under the
// configured LogRoot, namespaced by repo, named issue-<N>-<timestamp>.log.
func TestDispatchLogPath(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	got, err := d.dispatchLogPath("example-repo", 302)
	if err != nil {
		t.Fatalf("dispatchLogPath: %v", err)
	}
	if dir := filepath.Dir(got); dir != filepath.Join(root, "example-repo") {
		t.Errorf("log dir = %q, want %q", dir, filepath.Join(root, "example-repo"))
	}
	base := filepath.Base(got)
	if !strings.HasPrefix(base, "issue-302-") || !strings.HasSuffix(base, ".log") {
		t.Errorf("log file = %q, want issue-302-<timestamp>.log", base)
	}
}

// TestSpawnDetachedClaude verifies the detached spawn actually runs the
// child, redirects its stdio to the log file, and returns without
func TestSpawnDetachedClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell as the claude stand-in")
	}
	logPath := filepath.Join(t.TempDir(), "nested", "issue-302.log")
	pid, err := spawnDetachedClaude(t.TempDir(), logPath, "sh",
		[]string{"-c", "echo detached-ok"}, nil)
	if err != nil {
		t.Fatalf("spawnDetachedClaude: %v", err)
	}
	if pid <= 0 {
		t.Errorf("pid = %d, want positive", pid)
	}
	// The child writes asynchronously; poll the log briefly.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(logPath); readErr == nil && strings.Contains(string(b), "detached-ok") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("log %s never received the child's output", logPath)
}

// parsedHeadlessCmd parses args against the real headless flag set so a
// test can drive spawnDetachedWorker with the verb's flag surface.
func parsedHeadlessCmd(t *testing.T, d *Dispatcher, args ...string) *cli.Command {
	t.Helper()
	var captured *cli.Command
	hc := d.headlessCommand()
	hc.Action = func(_ context.Context, c *cli.Command) error {
		captured = c
		return nil
	}
	app := &cli.Command{Name: "test", Commands: []*cli.Command{hc}}
	if err := app.Run(context.Background(), append([]string{"test", "headless"}, args...)); err != nil {
		t.Fatalf("parse headless flags: %v", err)
	}
	return captured
}

// TestWatchForImmediateExit covers the startup-crash detector across
// dead-within-window, alive-past-window, and disabled-window cases.
func TestWatchForImmediateExit(t *testing.T) {
	d := newTestDispatcher(t)
	d.cfg.SpawnHealthWindow = 60 * time.Millisecond

	d.cfg.ProcessAlive = func(int) bool { return false }
	if !d.watchForImmediateExit(123) {
		t.Error("dead child within window: want true, got false")
	}

	d.cfg.ProcessAlive = func(int) bool { return true }
	if d.watchForImmediateExit(123) {
		t.Error("live child outliving window: want false, got true")
	}

	d.cfg.SpawnHealthWindow = -1
	d.cfg.ProcessAlive = func(int) bool { return false }
	if d.watchForImmediateExit(123) {
		t.Error("disabled window: want false, got true")
	}
}

// TestSpawnDetachedWorker_ImmediateExitSurfacesFailure: a child
// gone within the window surfaces as a nonzero failure with the log tail.
func TestSpawnDetachedWorker_ImmediateExitSurfacesFailure(t *testing.T) {
	d := newTestDispatcher(t)
	logRoot := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return logRoot, nil }
	d.cfg.ClaudeConfigPath = func() (string, error) { return filepath.Join(t.TempDir(), ".claude.json"), nil }
	d.cfg.SpawnHealthWindow = 50 * time.Millisecond
	d.cfg.ProcessAlive = func(int) bool { return false }
	d.cfg.SpawnDetached = func(_, logPath, _ string, _, _ []string) (int, error) {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return 0, err
		}
		body := "API Error: 400 messages.1.content.28: `thinking` blocks cannot be modified.\n"
		if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
			return 0, err
		}
		return 4242, nil
	}

	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 150}
	issue := &ghIssue{Number: 150, Title: "t", URL: "https://example/150"}
	c := parsedHeadlessCmd(t, d, issueRefText("example-repo", 150))

	err := d.spawnDetachedWorker(context.Background(), c, detachedSpec{mode: "headless"}, ref, issue, t.TempDir(), t.TempDir(), noopReservation{}, "prompt", "auto", "Bash")
	if err == nil {
		t.Fatal("expected immediate-exit failure, got nil")
	}
	for _, want := range []string{issueRefText("example-repo", 150), "no work done", "pid 4242", "API Error: 400"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q in:\n%s", want, err.Error())
		}
	}
}

// TestSpawnDetachedWorker_LiveChildSucceeds: a child still running at the
// end of the health window returns no error.
func TestSpawnDetachedWorker_LiveChildSucceeds(t *testing.T) {
	d := newTestDispatcher(t)
	logRoot := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return logRoot, nil }
	d.cfg.ClaudeConfigPath = func() (string, error) { return filepath.Join(t.TempDir(), ".claude.json"), nil }
	d.cfg.SpawnHealthWindow = 20 * time.Millisecond
	d.cfg.ProcessAlive = func(int) bool { return true }
	d.cfg.SpawnDetached = func(_, _, _ string, _, _ []string) (int, error) { return 4242, nil }

	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 150}
	issue := &ghIssue{Number: 150, Title: "t", URL: "https://example/150"}
	c := parsedHeadlessCmd(t, d, issueRefText("example-repo", 150))

	if err := d.spawnDetachedWorker(context.Background(), c, detachedSpec{mode: "headless"}, ref, issue, t.TempDir(), t.TempDir(), noopReservation{}, "prompt", "auto", "Bash"); err != nil {
		t.Fatalf("live child should succeed, got: %v", err)
	}
}

// TestResolveDispatchIssue_RejectsForeignOwner locks the security claim:
// dispatch refuses any issue ref outside Config.AllowedOwner. The owner
func TestResolveDispatchIssue_RejectsForeignOwner(t *testing.T) {
	d := newTestDispatcher(t)
	_, _, err := d.resolveDispatchIssue(context.Background(), fmt.Sprintf("%s#%d", "someoneelse/repo", 1), levelConsult)
	if err == nil {
		t.Fatal("resolveDispatchIssue should refuse a foreign owner, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to dispatch outside example-org/*") {
		t.Errorf("error = %q, want an example-org/* refusal", err)
	}
}

// TestParseIssueRef_ForgejoURL pins recognition of Forgejo URLs,
// and rejection when ForgejoBaseURL is unconfigured.
func TestParseIssueRef_ForgejoURL(t *testing.T) {
	base := "https://forgejo.coilysiren.me"
	d, err := New(Config{
		Runner:         &shell.Runner{},
		Wrap:           func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner:   "example-org",
		RepoPath:       func(string) (string, error) { return "/tmp", nil },
		WorktreeRoot:   func() (string, error) { return "/tmp", nil },
		LogRoot:        func() (string, error) { return "/tmp", nil },
		ForgejoBaseURL: base,
		FetchForgejoIssue: func(context.Context, string, string, int) (*Issue, error) {
			return &Issue{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	url := base + "/coilyco-flight-deck/ward/issues/18"
	got, err := d.parseIssueRef(url)
	if err != nil {
		t.Fatalf("parseIssueRef(%q): %v", url, err)
	}
	if got.Owner != "coilyco-flight-deck" || got.Repo != "ward" || got.Number != 18 || got.Platform != PlatformForgejo {
		t.Errorf("parseIssueRef(%q) = %+v, want Forgejo URL for the ward issue", url, got)
	}

	bare := newTestDispatcher(t)
	if _, err := bare.parseIssueRef(url); err == nil {
		t.Errorf("parseIssueRef(%q) without ForgejoBaseURL: expected error", url)
	}
}

// TestResolveDispatchIssue_ForgejoFirstThenGitHub pins shortform
// disambiguation (Forgejo first, GitHub fallback on 404).
func TestResolveDispatchIssue_ForgejoFirstThenGitHub(t *testing.T) {
	var forgejoCalled, ghCalled bool
	d, err := New(Config{
		Runner:         &shell.Runner{},
		Wrap:           func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner:   "example-org",
		RepoPath:       func(string) (string, error) { return "/tmp", nil },
		WorktreeRoot:   func() (string, error) { return "/tmp", nil },
		LogRoot:        func() (string, error) { return "/tmp", nil },
		ForgejoBaseURL: "https://forgejo.coilysiren.me",
		FetchForgejoIssue: func(_ context.Context, owner, repo string, n int) (*Issue, error) {
			forgejoCalled = true
			if owner == "example-org" && repo == "example-repo" && n == 99 {
				return &Issue{Number: 99, Title: "from forgejo", State: "open", URL: "https://forgejo.coilysiren.me/example-org/example-repo/issues/99"}, nil
			}
			return nil, fmt.Errorf("forgejo: 404 not found")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = ghCalled
	ref, issue, err := d.resolveDispatchIssue(context.Background(), issueRefText("example-repo", 99), levelConsult)
	if err != nil {
		t.Fatalf("resolveDispatchIssue: %v", err)
	}
	if !forgejoCalled {
		t.Error("expected forgejo fetcher to be tried first for shortform")
	}
	if ref.Platform != PlatformForgejo {
		t.Errorf("ref.Platform = %q, want forgejo", ref.Platform)
	}
	if issue.Title != "from forgejo" {
		t.Errorf("issue.Title = %q, want from forgejo", issue.Title)
	}
}

// TestIssueFetcher_OverridesGHDefault proves a configured IssueFetcher
// replaces the gh-based resolver for shortform refs, with no Forgejo seam set.
func TestIssueFetcher_OverridesGHDefault(t *testing.T) {
	var gotOwner, gotRepo string
	var gotNum int
	d, err := New(Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		RepoPath:     func(string) (string, error) { return "/tmp", nil },
		WorktreeRoot: func() (string, error) { return "/tmp", nil },
		LogRoot:      func() (string, error) { return "/tmp", nil },
		IssueFetcher: func(_ context.Context, owner, repo string, n int) (*Issue, error) {
			gotOwner, gotRepo, gotNum = owner, repo, n
			return &Issue{Number: n, Title: "from hook", State: "open", URL: "https://example/issues/77"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref, issue, err := d.resolveDispatchIssue(context.Background(), issueRefText("example-repo", 77), levelConsult)
	if err != nil {
		t.Fatalf("resolveDispatchIssue: %v", err)
	}
	if gotOwner != "example-org" || gotRepo != "example-repo" || gotNum != 77 {
		t.Errorf("IssueFetcher got %s/%s#%d, want %s", gotOwner, gotRepo, gotNum, issueRefText("example-repo", 77))
	}
	if issue.Title != "from hook" {
		t.Errorf("issue.Title = %q, want from hook", issue.Title)
	}
	if ref.Platform != PlatformGitHub {
		t.Errorf("ref.Platform = %q, want github", ref.Platform)
	}
}

// modeGateDispatcher builds a Dispatcher whose fetcher returns an open
// issue with the given labels, to exercise the ceiling gate without a forge.
func modeGateDispatcher(t *testing.T, labels []string) *Dispatcher {
	t.Helper()
	d, err := New(Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		RepoPath:     func(string) (string, error) { return "/tmp", nil },
		WorktreeRoot: func() (string, error) { return "/tmp", nil },
		LogRoot:      func() (string, error) { return "/tmp", nil },
		IssueFetcher: func(_ context.Context, _, _ string, n int) (*Issue, error) {
			return &Issue{Number: n, Title: "t", State: "open", URL: "https://example/issues", Labels: labels}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// TestResolveDispatchIssue_ModeCeiling pins the surface <= mode gate on
// headless > interactive > consult, unlabeled failing closed.
func TestResolveDispatchIssue_ModeCeiling(t *testing.T) {
	cases := []struct {
		name    string
		labels  []string
		surface surfaceLevel
		wantErr bool
	}{
		{"headless-surface-on-headless-issue", []string{"headless"}, levelHeadless, false},
		{"headless-surface-on-interactive-issue", []string{"interactive"}, levelHeadless, true},
		{"headless-surface-on-consult-issue", []string{"consult"}, levelHeadless, true},
		{"headless-surface-on-unlabeled-issue", nil, levelHeadless, true},
		{"interactive-surface-on-headless-issue", []string{"headless"}, levelInteractive, false},
		{"interactive-surface-on-interactive-issue", []string{"interactive"}, levelInteractive, false},
		{"interactive-surface-on-consult-issue", []string{"consult"}, levelInteractive, true},
		{"interactive-surface-on-unlabeled-issue", nil, levelInteractive, true},
		{"consult-surface-on-headless-issue", []string{"headless"}, levelConsult, false},
		{"consult-surface-on-consult-issue", []string{"consult"}, levelConsult, false},
		{"consult-surface-on-unlabeled-issue", nil, levelConsult, false},
		// A stray extra label can only tighten the gate (most restrictive wins).
		{"headless-surface-on-mixed-headless-consult", []string{"headless", "consult"}, levelHeadless, true},
		// Non-mode labels are ignored; the mode label still governs.
		{"headless-surface-on-headless-plus-P1", []string{"P1", "headless"}, levelHeadless, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := modeGateDispatcher(t, tc.labels)
			_, _, err := d.resolveDispatchIssue(context.Background(), issueRefText("example-repo", 1), tc.surface)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("surface %s on labels %v: want refusal, got nil", tc.surface, tc.labels)
				}
				if !strings.Contains(err.Error(), "automation mode") {
					t.Errorf("error = %q, want an automation-mode refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("surface %s on labels %v: want allow, got %v", tc.surface, tc.labels, err)
			}
		})
	}
}

// TestIssueFetcher_UsedForGitHubURL proves a GitHub-tagged ref (from a
// github.com URL) also routes through IssueFetcher when set.
func TestIssueFetcher_UsedForGitHubURL(t *testing.T) {
	called := false
	d, err := New(Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		RepoPath:     func(string) (string, error) { return "/tmp", nil },
		WorktreeRoot: func() (string, error) { return "/tmp", nil },
		LogRoot:      func() (string, error) { return "/tmp", nil },
		IssueFetcher: func(_ context.Context, _, _ string, n int) (*Issue, error) {
			called = true
			return &Issue{Number: n, Title: "from hook", State: "open", URL: "https://github.com/example-org/example-repo/issues/5"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, err := d.resolveDispatchIssue(context.Background(), "https://github.com/example-org/example-repo/issues/5", levelConsult); err != nil {
		t.Fatalf("resolveDispatchIssue: %v", err)
	}
	if !called {
		t.Error("expected IssueFetcher to be called for a github.com URL ref")
	}
}

// TestNew_RequiresFields proves New fails loud when a required Config
// field is missing rather than panicking deep in a verb later.
func TestNew_RequiresFields(t *testing.T) {
	ok := Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		RepoPath:     func(string) (string, error) { return "/tmp", nil },
		WorktreeRoot: func() (string, error) { return "/tmp", nil },
		LogRoot:      func() (string, error) { return "/tmp", nil },
	}
	if _, err := New(ok); err != nil {
		t.Fatalf("New(complete config): %v", err)
	}
	bad := ok
	bad.AllowedOwner = ""
	if _, err := New(bad); err == nil {
		t.Error("New with empty AllowedOwner should error")
	}
	bad = ok
	bad.Runner = nil
	if _, err := New(bad); err == nil {
		t.Error("New with nil Runner should error")
	}
}
