package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
)

// worktree.go owns dispatch worktree placement for the detached surfaces
// (headless, cascade); reap.go closes the lifecycle.

// defaultWorktreeAdd runs `git -C <repoPath> worktree add -B <branch>
// <worktreePath>` through the shell runner. Default for Config.WorktreeAdd.
func defaultWorktreeAdd(ctx context.Context, runner *shell.Runner, repoPath, branch, worktreePath string) error {
	_, err := runner.Capture(ctx, "git", "-C", repoPath, "worktree", "add", "-B", branch, worktreePath)
	return err
}

// dispatchWorktreeName is the worktree dir basename: "issue-<N>-<slug>", or
// "issue-<N>" when the title has no sluggable content. reap derives the branch.
func dispatchWorktreeName(number int, title string) string {
	if slug := branchSlug(title); slug != "" {
		return fmt.Sprintf("issue-%d-%s", number, slug)
	}
	return fmt.Sprintf("issue-%d", number)
}

// dispatchWorktreePath returns the worktree path for a given repo + issue.
// Format: <WorktreeRoot>/<repo>/issue-<N>-<slug> (or issue-<N> with no slug).
func (d *Dispatcher) dispatchWorktreePath(repo string, number int, title string) (string, error) {
	root, err := d.cfg.WorktreeRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, repo, dispatchWorktreeName(number, title)), nil
}

// dispatchWorktreeBranch returns "dispatch/<name>" for a worktree dir basename,
// so reap can derive the branch from the directory alone.
func dispatchWorktreeBranch(name string) string {
	return "dispatch/" + name
}

// ensureDispatchWorktree returns worktreePath after guaranteeing a git
// worktree exists there. Idempotent: reuses an existing `.git` entry.
func (d *Dispatcher) ensureDispatchWorktree(ctx context.Context, repoPath string, ref *issueRef, title string) (string, error) {
	name := dispatchWorktreeName(ref.Number, title)
	worktreePath, err := d.dispatchWorktreePath(ref.Repo, ref.Number, title)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err == nil {
		return worktreePath, nil
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir worktree parent: %w", err)
	}
	branch := dispatchWorktreeBranch(name)
	if err := d.cfg.WorktreeAdd(ctx, d.cfg.Runner, repoPath, branch, worktreePath); err != nil {
		return "", fmt.Errorf("git worktree add -B %s %s (in %s): %w", branch, worktreePath, repoPath, err)
	}
	return worktreePath, nil
}

// resolveDetachedCwd returns a detached dispatch's per-issue worktree.
// Dry-run resolves the path without creating the worktree.
func (d *Dispatcher) resolveDetachedCwd(ctx context.Context, repoPath string, ref *issueRef, title string, dryRun bool) (string, error) {
	if dryRun {
		return d.dispatchWorktreePath(ref.Repo, ref.Number, title)
	}
	return d.ensureDispatchWorktree(ctx, repoPath, ref, title)
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
