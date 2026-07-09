package dispatch

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// parsedCICmd parses args against the real ci flag set so a test can drive
// runCI with the verb's flag surface.
func parsedCICmd(t *testing.T, d *Dispatcher, args ...string) *cli.Command {
	t.Helper()
	var captured *cli.Command
	cc := d.ciCommand()
	cc.Action = func(_ context.Context, c *cli.Command) error {
		captured = c
		return nil
	}
	app := &cli.Command{Name: "test", Commands: []*cli.Command{cc}}
	if err := app.Run(context.Background(), append([]string{"test", "ci"}, args...)); err != nil {
		t.Fatalf("parse ci flags: %v", err)
	}
	return captured
}

// TestCISeedPrompt_InPlaceFooter pins that the CI prompt carries the issue,
// the headless preamble, and the in-place footer (not the worktree footer).
func TestCISeedPrompt_InPlaceFooter(t *testing.T) {
	ref := &issueRef{Owner: "example-org", Repo: "example-repo", Number: 141, Platform: PlatformForgejo}
	issue := &ghIssue{
		Number: 141,
		Title:  "ci execution mode",
		Body:   "design body here",
		State:  "OPEN",
		URL:    "https://forgejo.example/example-org/example-repo/issues/141",
	}
	got := ciSeedPrompt(ref, issue)
	for _, want := range []string{
		issueRefText("example-repo", 141),
		"Forgejo issue",
		"ci execution mode",
		"design body here",
		"https://forgejo.example/example-org/example-repo/issues/141",
		"AGENTS.md",
		"CI job",
		"in place in the current checkout",
		"git push origin HEAD:main",
		"autonomous-block:",
		"exit nonzero",
		"closes the issue",
		"--no-verify",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ciSeedPrompt missing %q in:\n%s", want, got)
		}
	}
	// In-place surface must not borrow the detached worktree-merge footer.
	for _, absent := range []string{"worktree on branch", "git -C "} {
		if strings.Contains(got, absent) {
			t.Errorf("ciSeedPrompt should not contain worktree-merge text %q", absent)
		}
	}
}

// TestRunCI_ForegroundSuccess: a clean (exit 0) foreground claude run
// returns nil, runs in the resolved cwd, and skips the detached seams.
func TestRunCI_ForegroundSuccess(t *testing.T) {
	d := newTestDispatcher(t)
	checkout := t.TempDir()
	d.cfg.Getwd = func() (string, error) { return checkout, nil }
	d.cfg.ClaudeConfigPath = func() (string, error) { return filepath.Join(t.TempDir(), ".claude.json"), nil }
	d.cfg.IssueFetcher = func(_ context.Context, _, _ string, n int) (*Issue, error) {
		return &Issue{Number: n, Title: "t", State: "open", URL: "https://example/141", Labels: []string{"headless"}}, nil
	}
	var gotCwd, gotBin string
	var gotArgv []string
	d.cfg.SpawnForeground = func(_ context.Context, repoPath, bin string, argv, _ []string) (int, error) {
		gotCwd, gotBin, gotArgv = repoPath, bin, argv
		return 0, nil
	}
	d.cfg.SpawnDetached = func(_, _, _ string, _, _ []string) (int, error) {
		t.Fatal("ci must not use the detached spawn seam")
		return 0, nil
	}

	c := parsedCICmd(t, d, issueRefText("example-repo", 141))
	if err := d.runCI(context.Background(), c); err != nil {
		t.Fatalf("runCI clean exit should succeed, got: %v", err)
	}
	if gotCwd != checkout {
		t.Errorf("foreground cwd = %q, want in-place checkout %q", gotCwd, checkout)
	}
	if gotBin != "claude" {
		t.Errorf("bin = %q, want claude", gotBin)
	}
	if len(gotArgv) < 2 || gotArgv[0] != "-p" {
		t.Errorf("argv = %v, want -p <prompt> ...", gotArgv)
	}
}

// TestRunCI_NonzeroExitFailsJob: a nonzero foreground exit surfaces as an
// error so the Actions job fails, naming the ref and the block fallback.
func TestRunCI_NonzeroExitFailsJob(t *testing.T) {
	d := newTestDispatcher(t)
	d.cfg.Getwd = func() (string, error) { return t.TempDir(), nil }
	d.cfg.ClaudeConfigPath = func() (string, error) { return filepath.Join(t.TempDir(), ".claude.json"), nil }
	d.cfg.IssueFetcher = func(_ context.Context, _, _ string, n int) (*Issue, error) {
		return &Issue{Number: n, Title: "t", State: "open", URL: "https://example/141", Labels: []string{"headless"}}, nil
	}
	d.cfg.SpawnForeground = func(_ context.Context, _, _ string, _, _ []string) (int, error) {
		return 7, nil
	}

	c := parsedCICmd(t, d, issueRefText("example-repo", 141))
	err := d.runCI(context.Background(), c)
	if err == nil {
		t.Fatal("nonzero foreground exit should fail the job, got nil")
	}
	for _, want := range []string{issueRefText("example-repo", 141), "exited 7", "autonomous-block:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q in:\n%s", want, err.Error())
		}
	}
}

// TestRunCI_DryRunDoesNotSpawn: --dry-run prints the plan and never calls
// the foreground spawn seam.
func TestRunCI_DryRunDoesNotSpawn(t *testing.T) {
	d := newTestDispatcher(t)
	d.cfg.Getwd = func() (string, error) { return t.TempDir(), nil }
	d.cfg.IssueFetcher = func(_ context.Context, _, _ string, n int) (*Issue, error) {
		return &Issue{Number: n, Title: "t", State: "open", URL: "https://example/141", Labels: []string{"headless"}}, nil
	}
	d.cfg.SpawnForeground = func(_ context.Context, _, _ string, _, _ []string) (int, error) {
		t.Fatal("dry-run must not spawn claude")
		return 0, nil
	}

	c := parsedCICmd(t, d, "--dry-run", issueRefText("example-repo", 141))
	if err := d.runCI(context.Background(), c); err != nil {
		t.Fatalf("dry-run runCI: %v", err)
	}
}

// TestRunCI_RejectsForeignOwner: the in-place surface honors the same
// owner-allowlist security claim as the rest of dispatch.
func TestRunCI_RejectsForeignOwner(t *testing.T) {
	d := newTestDispatcher(t)
	d.cfg.Getwd = func() (string, error) { return t.TempDir(), nil }
	d.cfg.SpawnForeground = func(_ context.Context, _, _ string, _, _ []string) (int, error) {
		t.Fatal("must refuse before spawning")
		return 0, nil
	}
	c := parsedCICmd(t, d, ownerRefText("someoneelse/repo", 1))
	err := d.runCI(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "refusing to dispatch outside example-org/*") {
		t.Fatalf("runCI should refuse a foreign owner, got: %v", err)
	}
}

// TestSpawnForegroundClaude verifies the real foreground spawn runs the
// child in the given dir and reports a nonzero exit as a code, not an error.
func TestSpawnForegroundClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell as the claude stand-in")
	}
	// Exit 0: clean run.
	code, err := spawnForegroundClaude(context.Background(), t.TempDir(), "sh",
		[]string{"-c", "exit 0"}, nil)
	if err != nil {
		t.Fatalf("spawnForegroundClaude exit 0: %v", err)
	}
	if code != 0 {
		t.Errorf("clean run code = %d, want 0", code)
	}

	// Nonzero exit: reported as a code, nil error.
	code, err = spawnForegroundClaude(context.Background(), t.TempDir(), "sh",
		[]string{"-c", "exit 5"}, nil)
	if err != nil {
		t.Fatalf("spawnForegroundClaude nonzero exit returned err: %v", err)
	}
	if code != 5 {
		t.Errorf("nonzero run code = %d, want 5", code)
	}

	// Unresolvable binary: a start failure, reported as an error.
	if _, err := spawnForegroundClaude(context.Background(), t.TempDir(),
		"definitely-not-a-real-binary-xyz", nil, nil); err == nil {
		t.Error("expected an error for an unresolvable binary")
	}
}
