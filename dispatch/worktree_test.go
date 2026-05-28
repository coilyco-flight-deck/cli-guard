package dispatch

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/coilysiren/cli-guard/shell"
)

// TestResolveDetachedCwd_CreatesWorktree pins the inverted mapping
// (coily#145): detached surfaces run in a per-issue worktree, not the bare repo.
func TestResolveDetachedCwd_CreatesWorktree(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	repoPath := t.TempDir()
	d.cfg.WorktreeRoot = func() (string, error) { return root, nil }

	var added bool
	d.cfg.WorktreeAdd = func(_ context.Context, _ *shell.Runner, gitDir, gitBranch, _ string) error {
		added = true
		if gitDir != repoPath {
			t.Errorf("WorktreeAdd repo = %q, want %q", gitDir, repoPath)
		}
		if gitBranch != "dispatch/issue-285" {
			t.Errorf("WorktreeAdd branch = %q, want dispatch/issue-285", gitBranch)
		}
		return nil
	}

	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 285}
	// Empty title -> bare issue-285 dir; slug naming is covered in interactive_test.go.
	cwd, err := d.resolveDetachedCwd(context.Background(), repoPath, ref, "", false)
	if err != nil {
		t.Fatalf("resolveDetachedCwd: %v", err)
	}
	if !added {
		t.Error("resolveDetachedCwd must create the worktree, WorktreeAdd was never called")
	}
	want := filepath.Join(root, "coily", "issue-285")
	if cwd != want {
		t.Errorf("resolveDetachedCwd = %q, want the worktree path %q (not the bare checkout)", cwd, want)
	}
	if cwd == repoPath {
		t.Error("resolveDetachedCwd returned the canonical checkout; detached surfaces must be isolated in a worktree")
	}
}

// TestResolveDetachedCwd_DryRunNoCreate verifies dry-run resolves the
// worktree path without shelling out to git.
func TestResolveDetachedCwd_DryRunNoCreate(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.WorktreeRoot = func() (string, error) { return root, nil }
	d.cfg.WorktreeAdd = func(_ context.Context, _ *shell.Runner, _, _, _ string) error {
		t.Fatal("dry-run must not create a worktree")
		return nil
	}

	ref := &issueRef{Owner: "coilysiren", Repo: "coily", Number: 285}
	cwd, err := d.resolveDetachedCwd(context.Background(), t.TempDir(), ref, "", true)
	if err != nil {
		t.Fatalf("resolveDetachedCwd dry-run: %v", err)
	}
	if want := filepath.Join(root, "coily", "issue-285"); cwd != want {
		t.Errorf("dry-run cwd = %q, want %q", cwd, want)
	}
}
