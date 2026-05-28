package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coilysiren/cli-guard/shell"
)

// worktree.go owns dispatch worktree placement for the detached surfaces
// (headless, cascade); reap.go closes the lifecycle. coilysiren/coily#145.

// defaultWorktreeAdd runs `git -C <repoPath> worktree add -B <branch>
// <worktreePath>` through the shell runner. Default for Config.WorktreeAdd.
func defaultWorktreeAdd(ctx context.Context, runner *shell.Runner, repoPath, branch, worktreePath string) error {
	_, err := runner.Capture(ctx, "git", "-C", repoPath, "worktree", "add", "-B", branch, worktreePath)
	return err
}

// dispatchWorktreePath returns the worktree path for a given repo + issue
// number. Format: <WorktreeRoot>/<repo>/issue-<N>.
func (d *Dispatcher) dispatchWorktreePath(repo string, number int) (string, error) {
	root, err := d.cfg.WorktreeRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, repo, fmt.Sprintf("issue-%d", number)), nil
}

// dispatchWorktreeBranch returns the branch name for a given issue
// number. Format: dispatch/issue-<N>. Predictable so reap can find it.
func dispatchWorktreeBranch(number int) string {
	return fmt.Sprintf("dispatch/issue-%d", number)
}

// ensureDispatchWorktree returns worktreePath after guaranteeing a git
// worktree exists there. Idempotent: reuses an existing `.git` entry.
func (d *Dispatcher) ensureDispatchWorktree(ctx context.Context, repoPath string, ref *issueRef) (string, error) {
	worktreePath, err := d.dispatchWorktreePath(ref.Repo, ref.Number)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err == nil {
		return worktreePath, nil
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir worktree parent: %w", err)
	}
	branch := dispatchWorktreeBranch(ref.Number)
	if err := d.cfg.WorktreeAdd(ctx, d.cfg.Runner, repoPath, branch, worktreePath); err != nil {
		return "", fmt.Errorf("git worktree add -B %s %s (in %s): %w", branch, worktreePath, repoPath, err)
	}
	return worktreePath, nil
}

// resolveDetachedCwd returns a detached dispatch's per-issue worktree.
// Dry-run resolves the path without creating the worktree.
func (d *Dispatcher) resolveDetachedCwd(ctx context.Context, repoPath string, ref *issueRef, dryRun bool) (string, error) {
	if dryRun {
		return d.dispatchWorktreePath(ref.Repo, ref.Number)
	}
	return d.ensureDispatchWorktree(ctx, repoPath, ref)
}

// reapBeforeDetachedDispatch removes merged worktrees from prior detached
// dispatches so sprawl stays self-limiting. Soft-fail: must not block.
func (d *Dispatcher) reapBeforeDetachedDispatch(ctx context.Context, mode string) {
	if removed, reapErr := d.reapDispatchWorktrees(ctx); reapErr != nil {
		fmt.Fprintf(os.Stderr, "dispatch %s: worktree reap skipped (%v)\n", mode, reapErr)
	} else if len(removed) > 0 {
		fmt.Fprintf(os.Stderr, "dispatch %s: reaped %d merged worktree(s)\n", mode, len(removed))
	}
}
