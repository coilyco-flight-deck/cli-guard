package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/coilysiren/cli-guard/shell"
	"github.com/urfave/cli/v3"
)

// reap.go closes the worktree lifecycle opened by interactive.go.
// ensureDispatchWorktree creates one git worktree per dispatched issue
// and nothing else removes it, so worktrees and dispatch/issue-N branches
// would pile up under the worktree root forever. The reaper removes every
// worktree whose branch is fully merged into main.

// defaultWorktreeReapable reports whether a dispatch worktree is safe to
// remove: its branch is either gone (already cleaned up) or fully merged
// into main. An existing, unmerged branch is never reapable - that is
// in-flight work. Default for Config.WorktreeReapable; tests swap it.
func defaultWorktreeReapable(ctx context.Context, runner *shell.Runner, repoPath, branch string) bool {
	// Branch missing -> nothing unmerged can be lost; reapable.
	if _, err := runner.Capture(ctx, "git", "-C", repoPath,
		"rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return true
	}
	// Branch present -> reapable only if it is an ancestor of main.
	_, err := runner.Capture(ctx, "git", "-C", repoPath,
		"merge-base", "--is-ancestor", branch, "main")
	return err == nil
}

// defaultWorktreeRemove removes one merged worktree, deletes its branch,
// and prunes git's worktree metadata. `git worktree remove` (no --force)
// refuses a dirty worktree, so uncommitted work in a merged worktree is
// preserved rather than silently dropped - the caller skips and warns.
// Default for Config.WorktreeRemove; tests swap it.
func defaultWorktreeRemove(ctx context.Context, runner *shell.Runner, repoPath, worktreePath, branch string) error {
	if _, err := runner.Capture(ctx, "git", "-C", repoPath,
		"worktree", "remove", worktreePath); err != nil {
		return err
	}
	// Branch delete + prune are best-effort: the worktree is already
	// gone, so a leftover branch ref or stale metadata is cosmetic.
	_, _ = runner.Capture(ctx, "git", "-C", repoPath, "branch", "-D", branch)
	_, _ = runner.Capture(ctx, "git", "-C", repoPath, "worktree", "prune")
	return nil
}

// parseIssueDirName extracts N from an "issue-N" worktree directory name.
func parseIssueDirName(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "issue-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// reapLocalRepoPath resolves a repo's local checkout and reports whether
// it exists on disk. A worktree whose repo is not checked out cannot be
// reaped through git, so the reaper skips it.
func (d *Dispatcher) reapLocalRepoPath(repo string) (string, bool) {
	p, err := d.cfg.RepoPath(repo)
	if err != nil {
		return "", false
	}
	st, statErr := os.Stat(p)
	return p, statErr == nil && st.IsDir()
}

// reapRepoWorktrees removes every merged worktree under one repo's
// dispatch-worktree directory and returns the removed paths. Pulled out
// of reapDispatchWorktrees so the outer walk stays under the cognitive
// complexity threshold.
func (d *Dispatcher) reapRepoWorktrees(ctx context.Context, root, repo string) []string {
	repoPath, ok := d.reapLocalRepoPath(repo)
	if !ok {
		return nil
	}
	issueEntries, err := os.ReadDir(filepath.Join(root, repo))
	if err != nil {
		return nil
	}
	var removed []string
	for _, issueEntry := range issueEntries {
		n, ok := parseIssueDirName(issueEntry.Name())
		if !issueEntry.IsDir() || !ok {
			continue
		}
		worktreePath := filepath.Join(root, repo, issueEntry.Name())
		branch := dispatchWorktreeBranch(n)
		if !d.cfg.WorktreeReapable(ctx, d.cfg.Runner, repoPath, branch) {
			continue
		}
		if err := d.cfg.WorktreeRemove(ctx, d.cfg.Runner, repoPath, worktreePath, branch); err != nil {
			fmt.Fprintf(os.Stderr, "dispatch reap: skip %s: %v\n", worktreePath, err)
			continue
		}
		removed = append(removed, worktreePath)
	}
	return removed
}

// reapDispatchWorktrees walks the dispatch-worktree root and removes every
// worktree whose branch is merged into its repo's main. Best-effort: a
// failure on one worktree (dirty tree, missing repo) is logged and
// skipped, never aborts the sweep. Returns the removed worktree paths.
func (d *Dispatcher) reapDispatchWorktrees(ctx context.Context) ([]string, error) {
	root, err := d.cfg.WorktreeRoot()
	if err != nil {
		return nil, err
	}
	repoEntries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dispatch-worktree root %s: %w", root, err)
	}
	var removed []string
	for _, repoEntry := range repoEntries {
		if !repoEntry.IsDir() {
			continue
		}
		removed = append(removed, d.reapRepoWorktrees(ctx, root, repoEntry.Name())...)
	}
	return removed, nil
}

// reapCommand is the explicit all-repos sweep. The same reaper runs
// automatically at the start of every `dispatch interactive`, so this
// verb is the on-demand version for cleaning up between dispatches.
func (d *Dispatcher) reapCommand() *cli.Command {
	return &cli.Command{
		Name:  "reap",
		Usage: "Remove dispatch worktrees whose branch is already merged into main.",
		Description: `reap walks the dispatch-worktree root and removes every worktree whose
dispatch/issue-N branch is fully merged into that repo's main (or whose
branch is already gone). It deletes the merged branch and runs git
worktree prune.

A worktree with an unmerged branch is left alone - that is in-flight
work. A worktree with uncommitted changes is skipped with a warning
rather than force-removed.

The same sweep runs automatically at the start of every
'dispatch interactive', so worktree sprawl is self-limiting; this verb
is the explicit on-demand version.`,
		Action: func(ctx context.Context, _ *cli.Command) error {
			removed, err := d.reapDispatchWorktrees(ctx)
			if err != nil {
				return fmt.Errorf("dispatch reap: %w", err)
			}
			if len(removed) == 0 {
				fmt.Println("dispatch reap: no merged worktrees to remove")
				return nil
			}
			fmt.Printf("dispatch reap: removed %d merged worktree(s):\n", len(removed))
			for _, p := range removed {
				fmt.Printf("  %s\n", p)
			}
			return nil
		},
	}
}
