package dispatch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/shell"
)

// TestInteractivePrompt_RefAndFirstAction pins the prompt contract. The
// shim greps the owner/repo#N token out of the JSON payload via jq, but
func TestInteractivePrompt_RefAndFirstAction(t *testing.T) {
	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 270}
	issue := &ghIssue{
		Number: 270,
		Title:  "split dispatch into headless/interactive",
		URL:    "https://github.com/coilysiren/coily/issues/270",
		State:  "open",
	}
	got := interactivePrompt(ref, issue, "")

	if !strings.HasPrefix(got, "Work on issue coilysiren/coily#270.") {
		t.Errorf("interactivePrompt prefix = %q, want \"Work on issue coilysiren/coily#270.\" lead", got)
	}
	for _, want := range []string{
		"First action",
		"coily ops gh issue view",
		"https://github.com/coilysiren/coily/issues/270",
		"--comments",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("interactivePrompt missing %q, got %q", want, got)
		}
	}
}

// TestInteractivePrompt_MainMode pins the supervised-surface contract
// (coily#145): main-mode prompt, no worktree-merge instructions.
func TestInteractivePrompt_MainMode(t *testing.T) {
	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 300}
	issue := &ghIssue{
		Number: 300,
		Title:  "invert the surface-to-isolation mapping",
		URL:    "https://github.com/coilysiren/coily/issues/300",
		State:  "open",
	}

	got := interactivePrompt(ref, issue, "")
	for _, want := range []string{
		"canonical checkout on the default branch",
		"there is no worktree",
		"closes #300",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("main-mode prompt missing %q, got %q", want, got)
		}
	}
	for _, absent := range []string{
		"dispatch/issue-300",
		"merge that branch",
		"Never force-push",
	} {
		if strings.Contains(got, absent) {
			t.Errorf("main-mode prompt must not mention worktree-merge %q, got %q", absent, got)
		}
	}

	// The consult preamble leads the prompt when supplied.
	withPreamble := interactivePrompt(ref, issue, consultPreamble)
	if !strings.HasPrefix(withPreamble, consultPreamble) {
		t.Errorf("consult prompt should lead with the consult preamble, got %q", withPreamble)
	}
}

// TestInteractiveTitleLine pins the self-identifying header shape
// dispatch embeds in the queue entry's title field and the shim echoes
func TestInteractiveTitleLine(t *testing.T) {
	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 279}
	issue := &ghIssue{
		Number: 279,
		Title:  "  dispatch interactive: echo issue title and prime first agent action  ",
		URL:    "https://github.com/coilysiren/coily/issues/279",
		State:  "open",
	}
	got := interactiveTitleLine(ref, issue)
	want := "coilysiren/coily#279: dispatch interactive: echo issue title and prime first agent action"
	if got != want {
		t.Errorf("interactiveTitleLine = %q, want %q", got, want)
	}
}

// TestWriteDispatchQueueEntry_ModeAndJSON verifies the queue entry is
// written under the queue dir with mode 0600, named
func TestWriteDispatchQueueEntry_ModeAndJSON(t *testing.T) {
	dir := t.TempDir()
	entry := dispatchQueueEntry{
		SchemaVersion: dispatchQueueSchemaVersion,
		Ref:           "coilysiren/coily#280",
		Title:         "concurrency race on scratch path",
		Cwd:           "/Users/kai/projects/coilysiren/coily",
		Prompt:        "Work on issue coilysiren/coily#280. First action: ...",
	}
	path, err := writeDispatchQueueEntry(dir, entry)
	if err != nil {
		t.Fatalf("writeDispatchQueueEntry: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat queue entry: %v", err)
	}
	if got, want := st.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("queue entry mode = %o, want %o", got, want)
	}
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("queue entry path = %q, want .json suffix", path)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("queue entry dir = %q, want %q", filepath.Dir(path), dir)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read queue entry: %v", err)
	}
	var got dispatchQueueEntry
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal queue entry: %v\nbody: %s", err, b)
	}
	if got != entry {
		t.Errorf("queue entry roundtrip = %+v, want %+v", got, entry)
	}
}

// TestWriteDispatchQueueEntry_UniqueFilenames verifies two back-to-back
// writes produce distinct filenames so concurrent dispatches never
func TestWriteDispatchQueueEntry_UniqueFilenames(t *testing.T) {
	dir := t.TempDir()
	entry := dispatchQueueEntry{
		SchemaVersion: dispatchQueueSchemaVersion,
		Ref:           "coilysiren/coily#280",
		Title:         "concurrency race on scratch path",
		Cwd:           "/Users/kai/projects/coilysiren/coily",
		Prompt:        "Work on issue coilysiren/coily#280.",
	}
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		path, err := writeDispatchQueueEntry(dir, entry)
		if err != nil {
			t.Fatalf("writeDispatchQueueEntry iteration %d: %v", i, err)
		}
		if seen[path] {
			t.Errorf("duplicate queue path %q at iteration %d", path, i)
		}
		seen[path] = true
	}
}

// TestDispatchInteractiveDefaults pins the seam strings between dispatch
// and the agentic-os shim. Changing either side without the other breaks
func TestDispatchInteractiveDefaults(t *testing.T) {
	if defaultDispatchQueueDir != "/tmp/coily-dispatch-queue" {
		t.Errorf("defaultDispatchQueueDir = %q, want /tmp/coily-dispatch-queue", defaultDispatchQueueDir)
	}
	if defaultDispatchLaunchName != "claude-dispatch-interactive" {
		t.Errorf("defaultDispatchLaunchName = %q, want claude-dispatch-interactive", defaultDispatchLaunchName)
	}
	if defaultDispatchChannel != "preview" {
		t.Errorf("defaultDispatchChannel = %q, want preview", defaultDispatchChannel)
	}
	if defaultDispatchSurface != "tab" {
		t.Errorf("defaultDispatchSurface = %q, want tab", defaultDispatchSurface)
	}
	if dispatchQueueSchemaVersion != 1 {
		t.Errorf("dispatchQueueSchemaVersion = %d, want 1", dispatchQueueSchemaVersion)
	}
}

// TestDispatchURL_ChannelSurfaceMatrix pins the four URL shapes dispatch
// can produce: (preview, stable) x (tab, window). Stable always lands at
func TestDispatchURL_ChannelSurfaceMatrix(t *testing.T) {
	cases := []struct {
		channel string
		surface string
		want    string
	}{
		{"preview", "tab", "warppreview://tab_config/claude-dispatch-interactive"},
		{"preview", "window", "warppreview://launch/claude-dispatch-interactive"},
		{"stable", "tab", "warp://tab_config/claude-dispatch-interactive"},
		{"stable", "window", "warp://launch/claude-dispatch-interactive"},
	}
	for _, tc := range cases {
		got, err := dispatchURL(tc.channel, tc.surface, "claude-dispatch-interactive")
		if err != nil {
			t.Errorf("dispatchURL(%q,%q): unexpected err: %v", tc.channel, tc.surface, err)
			continue
		}
		if got != tc.want {
			t.Errorf("dispatchURL(%q,%q) = %q, want %q", tc.channel, tc.surface, got, tc.want)
		}
	}
}

// TestDispatchURL_RejectsUnknownChannel pins the "preview | stable" gate.
// An unknown channel must error rather than silently fall through to a
func TestDispatchURL_RejectsUnknownChannel(t *testing.T) {
	_, err := dispatchURL("garbage", "tab", "claude-dispatch-interactive")
	if err == nil {
		t.Fatal("dispatchURL(garbage,tab) should error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"preview", "stable", "invalid"} {
		if !strings.Contains(msg, want) {
			t.Errorf("dispatchURL(garbage,tab) error = %q, want substring %q", msg, want)
		}
	}
}

// TestDispatchURL_RejectsUnknownSurface pins the "tab | window" gate.
func TestDispatchURL_RejectsUnknownSurface(t *testing.T) {
	_, err := dispatchURL("preview", "garbage", "claude-dispatch-interactive")
	if err == nil {
		t.Fatal("dispatchURL(preview,garbage) should error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"tab", "window", "invalid"} {
		if !strings.Contains(msg, want) {
			t.Errorf("dispatchURL(preview,garbage) error = %q, want substring %q", msg, want)
		}
	}
}

// TestDispatchBare_ErrorsWithSurfaceGate pins the "no default surface"
// rule. Bare `dispatch <ref>` must error and name the four valid surfaces;
func TestDispatchBare_ErrorsWithSurfaceGate(t *testing.T) {
	d := newTestDispatcher(t)
	cmd := d.Command()
	err := cmd.Run(context.Background(), []string{"dispatch", "coilysiren/coily#270"})
	if err == nil {
		t.Fatal("bare dispatch <ref> should error with surface-gate, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"specify surface", "headless", "interactive", "consult", "cascade"} {
		if !strings.Contains(msg, want) {
			t.Errorf("dispatch bare error = %q, want substring %q", msg, want)
		}
	}
}

// TestDispatchHasSurfaceSubverbs proves all four surface subverbs hang off
// the dispatch parent.
func TestDispatchHasSurfaceSubverbs(t *testing.T) {
	d := newTestDispatcher(t)
	cmd := d.Command()
	got := map[string]bool{}
	for _, sub := range cmd.Commands {
		got[sub.Name] = true
	}
	for _, want := range []string{"headless", "interactive", "consult", "cascade"} {
		if !got[want] {
			t.Errorf("dispatch parent missing subverb %q (got %v)", want, got)
		}
	}
}

// TestDispatchInteractiveRejectsPostureFlag pins coily#144: --posture is gone,
// so passing it to interactive must error as an unknown flag.
func TestDispatchInteractiveRejectsPostureFlag(t *testing.T) {
	d := newTestDispatcher(t)
	cmd := d.Command()
	err := cmd.Run(context.Background(), []string{
		"dispatch", "interactive", "--posture", "consult", "coilysiren/coily#144",
	})
	if err == nil {
		t.Fatal("dispatch interactive --posture should error (flag removed), got nil")
	}
	if !strings.Contains(err.Error(), "posture") {
		t.Errorf("error = %q, want it to name the unknown --posture flag", err.Error())
	}
}

// TestDispatchWorktreePath pins <WorktreeRoot>/<repo>/issue-<N>-<slug>, with
// the bare issue-<N> fallback when the title has no sluggable content.
func TestDispatchWorktreePath(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.WorktreeRoot = func() (string, error) { return root, nil }
	got, err := d.dispatchWorktreePath("coily", 285, "feat(dispatch): support X")
	if err != nil {
		t.Fatalf("dispatchWorktreePath: %v", err)
	}
	want := filepath.Join(root, "coily", "issue-285-feat-dispatch-support-x")
	if got != want {
		t.Errorf("dispatchWorktreePath = %q, want %q", got, want)
	}
	gotBare, err := d.dispatchWorktreePath("coily", 285, "  ::  ")
	if err != nil {
		t.Fatalf("dispatchWorktreePath (bare): %v", err)
	}
	if wantBare := filepath.Join(root, "coily", "issue-285"); gotBare != wantBare {
		t.Errorf("dispatchWorktreePath (no slug) = %q, want %q", gotBare, wantBare)
	}
}

// TestDispatchWorktreeBranch pins the invariant branch == "dispatch/" + the
// worktree directory basename, so reap can derive the branch from the dir.
func TestDispatchWorktreeBranch(t *testing.T) {
	name := dispatchWorktreeName(285, "feat(dispatch): support X")
	if want := "issue-285-feat-dispatch-support-x"; name != want {
		t.Errorf("dispatchWorktreeName = %q, want %q", name, want)
	}
	if got, want := dispatchWorktreeBranch(name), "dispatch/issue-285-feat-dispatch-support-x"; got != want {
		t.Errorf("dispatchWorktreeBranch(%q) = %q, want %q", name, got, want)
	}
	if got, want := dispatchWorktreeBranch("issue-285"), "dispatch/issue-285"; got != want {
		t.Errorf("dispatchWorktreeBranch(bare) = %q, want %q", got, want)
	}
}

// TestEnsureDispatchWorktree_CallsGit verifies the production path: when
// no worktree exists at the target path, ensure runs the WorktreeAdd
func TestEnsureDispatchWorktree_CallsGit(t *testing.T) {
	d := newTestDispatcher(t)
	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 285}
	repoPath := t.TempDir()
	root := t.TempDir()
	d.cfg.WorktreeRoot = func() (string, error) { return root, nil }

	var gotRepo, gotBranch, gotPath string
	calls := 0
	d.cfg.WorktreeAdd = func(_ context.Context, _ *shell.Runner, gitDir, gitBranch, gitWT string) error {
		calls++
		gotRepo, gotBranch, gotPath = gitDir, gitBranch, gitWT
		return nil
	}

	wt, err := d.ensureDispatchWorktree(context.Background(), repoPath, ref, "feat(dispatch): support X")
	if err != nil {
		t.Fatalf("ensureDispatchWorktree: %v", err)
	}
	if calls != 1 {
		t.Errorf("WorktreeAdd called %d times, want 1", calls)
	}
	if gotRepo != repoPath {
		t.Errorf("git -C dir = %q, want %q", gotRepo, repoPath)
	}
	if gotBranch != "dispatch/issue-285-feat-dispatch-support-x" {
		t.Errorf("branch = %q, want dispatch/issue-285-feat-dispatch-support-x", gotBranch)
	}
	if !strings.HasSuffix(gotPath, filepath.Join("coily", "issue-285-feat-dispatch-support-x")) {
		t.Errorf("worktree path = %q, want suffix coily/issue-285-feat-dispatch-support-x", gotPath)
	}
	if wt != gotPath {
		t.Errorf("ensure returned %q, WorktreeAdd saw %q", wt, gotPath)
	}
}

// TestEnsureDispatchWorktree_Idempotent verifies the reuse path: when a
// .git entry already exists under the target worktree path, ensure
func TestEnsureDispatchWorktree_Idempotent(t *testing.T) {
	d := newTestDispatcher(t)
	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 285}
	repoPath := t.TempDir()
	root := t.TempDir()
	d.cfg.WorktreeRoot = func() (string, error) { return root, nil }

	existing := filepath.Join(root, "coily", "issue-285")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("mkdir existing worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	calls := 0
	d.cfg.WorktreeAdd = func(_ context.Context, _ *shell.Runner, _, _, _ string) error {
		calls++
		return nil
	}

	// Empty-slug title so the path falls back to the bare issue-285 dir that
	// this test pre-creates.
	wt, err := d.ensureDispatchWorktree(context.Background(), repoPath, ref, "")
	if err != nil {
		t.Fatalf("ensureDispatchWorktree: %v", err)
	}
	if calls != 0 {
		t.Errorf("WorktreeAdd called %d times, want 0 (existing worktree must be reused)", calls)
	}
	if wt != existing {
		t.Errorf("ensure returned %q, want %q", wt, existing)
	}
}
