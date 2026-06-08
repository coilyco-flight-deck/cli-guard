package dispatch

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"github.com/urfave/cli/v3"
)

// cascade.go is the recursive fan-out surface: a detached worker allowed
// to dispatch its own sub-workers, bounded by a hard depth budget.

// envCascadeDepth carries the cascade depth budget into a spawned child's
// environment. Nested cascades decrement it; headless children inherit "0".
const envCascadeDepth = "COILY_DISPATCH_CASCADE_DEPTH"

// defaultCascadeDepth is the budget a top-level cascade starts with when
// --depth is unset: the three-level tree (orchestrator -> repo -> leaf).
const defaultCascadeDepth = 3

// maxCascadeDepth caps the top-level --depth so a typo cannot launch a
// pathologically deep tree.
const maxCascadeDepth = 5

// resolveCascadeBudget computes the budget to stamp on the spawned worker.
// Top-level uses flagDepth; nested decrements inherited and refuses at floor.
func resolveCascadeBudget(flagDepth int, inheritedRaw string) (int, error) {
	if flagDepth < 1 || flagDepth > maxCascadeDepth {
		return 0, fmt.Errorf("--depth must be between 1 and %d (got %d)", maxCascadeDepth, flagDepth)
	}
	inheritedRaw = strings.TrimSpace(inheritedRaw)
	if inheritedRaw == "" {
		return flagDepth, nil
	}
	inherited, err := strconv.Atoi(inheritedRaw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q in environment: %w", envCascadeDepth, inheritedRaw, err)
	}
	child := inherited - 1
	if child < 1 {
		return 0, fmt.Errorf(
			"dispatch cascade: depth budget exhausted (inherited %s=%d). Spawn `dispatch headless` leaves or do the work directly",
			envCascadeDepth, inherited)
	}
	if child > flagDepth {
		child = flagDepth
	}
	return child, nil
}

// cascadeCommand fires a detached cascade worker: a headless spawn whose
// prompt authorizes and instructs recursive decomposition into sub-workers.
func (d *Dispatcher) cascadeCommand() *cli.Command {
	return &cli.Command{
		Name:      "cascade",
		Usage:     "Spawn a detached worker allowed to recursively dispatch sub-workers (bounded fan-out).",
		ArgsUsage: "<owner/repo#N | issue-url>",
		Description: fmt.Sprintf(`cascade is the most autonomous dispatch surface: a detached headless
spawn whose prompt allows it to break a too-large task into pieces and
dispatch its own sub-workers, one level at a time, down to leaf-sized
headless workers. It exists for mass migrations and other branching
trees of dependent work that a single headless run would reject as
"scope too large".

It is NOT --dangerously-skip-permissions: the worker still runs under
the lockdown and audit. The extra autonomy is the prompt posture plus
permission to call 'dispatch' on itself.

Recursion is bounded by a hard depth budget (--depth, default %d, max %d).
dispatch stamps the budget into each spawned child's environment and
decrements it on every nested cascade; at the floor a nested cascade is
refused, so the tree cannot fork-bomb. A worker at budget 1 may still
spawn headless leaves or do the work directly - it just cannot cascade
further.

Like headless, the child is fully detached: stdio to a log file, returns
immediately, survives the terminal closing. Refuses if the local
checkout is missing.`, defaultCascadeDepth, maxCascadeDepth),
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "print the resolved issue + seeded prompt + repo path + flags + depth budget, do not exec claude",
			},
			&cli.StringFlag{
				Name:  "claude-bin",
				Usage: "override the claude binary path (default: 'claude' on $PATH)",
				Value: "claude",
			},
			&cli.StringFlag{
				Name:  "permission-mode",
				Usage: "permission mode passed to the child claude session (auto, default, acceptEdits, plan, bypassPermissions). Cascade needs a non-prompting mode; default is auto.",
				Value: defaultDispatchPermissionMode,
			},
			&cli.StringFlag{
				Name:  "allowed-tools",
				Usage: "comma-separated allowedTools list passed to the child. Default covers the workflow footer plus dispatching sub-workers.",
				Value: defaultDispatchAllowedTools,
			},
			&cli.StringFlag{
				Name:  "claims",
				Usage: "comma-separated paths this sidequest will edit. Surfaced via `dispatch registry`.",
			},
			&cli.IntFlag{
				Name:  "depth",
				Usage: "cascade depth budget for the spawned worker. Top-level only; nested cascades inherit and decrement. Capped at the max.",
				Value: defaultCascadeDepth,
			},
		},
		Action: d.cfg.Wrap(verb.Spec{
			Name:       "dispatch.cascade",
			SkipPolicy: false,
			ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
				return map[string]string{
					"--claude-bin":      c.String("claude-bin"),
					"--permission-mode": c.String("permission-mode"),
					"--allowed-tools":   c.String("allowed-tools"),
					"--claims":          c.String("claims"),
					"--depth":           strconv.Itoa(c.Int("depth")),
				}, c.Args().Slice()
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				return d.runCascade(ctx, c)
			},
		}),
	}
}

func (d *Dispatcher) runCascade(ctx context.Context, c *cli.Command) error {
	budget, err := resolveCascadeBudget(c.Int("depth"), os.Getenv(envCascadeDepth))
	if err != nil {
		return err
	}
	return d.runDetached(ctx, c, detachedSpec{
		mode: "cascade",
		prompt: func(ref *issueRef, issue *ghIssue, repoPath string) string {
			return cascadeSeedPrompt(ref, issue, repoPath, budget)
		},
		extraEnv: []string{fmt.Sprintf("%s=%d", envCascadeDepth, budget)},
	})
}

// cascadeSeedPrompt composes the prompt for a cascade worker holding the
// given depth budget. repoPath is the checkout it merges its branch into.
func cascadeSeedPrompt(ref *issueRef, issue *ghIssue, repoPath string, budget int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", cascadePreamble(budget))
	fmt.Fprintf(&b, "Work on %s issue %s.\n\n", forgeName(ref.Platform), ref)
	fmt.Fprintf(&b, "Title: %s\n", issue.Title)
	fmt.Fprintf(&b, "URL:   %s\n\n", issue.URL)
	fmt.Fprintf(&b, "Issue body:\n\n%s\n\n", strings.TrimSpace(issue.Body))
	fmt.Fprintf(&b, "%s", cascadeWorktreeFooter(repoPath, ref.Number, issue.Title))
	return b.String()
}

// cascadePreamble is the recursion posture, parameterized by the worker's
// depth budget. At budget 1 it forbids further cascade spawns (the floor).
func cascadePreamble(budget int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a cascade worker with a recursion depth budget of %d. ", budget)
	b.WriteString("You are running detached and autonomously - not dangerously-skip-permissions, but more autonomous than a normal headless run: you are allowed to dispatch your own sub-workers to split large bodies of work. ")
	b.WriteString("First assess scope. If this task fits one focused session, just do it end to end and finish - do not fan out for its own sake. ")
	b.WriteString("If it is too large (it spans multiple repos, or naturally splits into independent slices like source / docs / tests), decompose it: for each slice, file a sub-issue in the relevant repo capturing that slice with a link back to this issue, then dispatch a worker against it. ")
	if budget > 1 {
		b.WriteString("For a slice that itself needs further splitting, spawn a sub-tree with `dispatch cascade <owner/repo#N>` - it inherits and decrements this budget automatically, so do NOT pass --depth. For a leaf-sized slice, spawn `dispatch headless <owner/repo#N>`. ")
	} else {
		b.WriteString("Your budget is 1 - the floor. You may NOT spawn `dispatch cascade` (it will refuse). Either do the work yourself end to end, or for independent leaf-sized slices file sub-issues and spawn `dispatch headless <owner/repo#N>`. ")
	}
	b.WriteString("Sub-workers are detached and run concurrently; do not block waiting on them. After dispatching, leave a comment on this issue listing the sub-issues and workers you spawned, then finish.")
	return b.String()
}

// cascadeWorktreeFooter is the cascade-specific workflow footer: like
// headless, but does not close the issue on fan-out.
func cascadeWorktreeFooter(repoPath string, number int, title string) string {
	branch := dispatchWorktreeBranch(dispatchWorktreeName(number, title))
	return fmt.Sprintf("Workflow rules (from AGENTS.md):\n"+
		"- You are in a git worktree on branch `%s`, isolated from sibling workers so concurrent workers in this repo never share a working tree or index. Commit work you do directly to this branch.\n"+
		"- Run tests, linters, and builds without asking. Fix failures. Never use --no-verify.\n"+
		"- To land work you do directly (no fan-out): run `git -C %s merge %s` then `git -C %s push origin main`. Resolve any merge conflicts yourself. Never force-push.\n"+
		"- If `git push origin main` is rejected as non-fast-forward (a sibling worker pushed first), run `git -C %s pull --rebase origin main`, re-run tests/build, then merge and push again. Repeat until it lands.\n"+
		"- If you do the work directly (no fan-out), close this issue with a commit trailer: closes #<N>.\n"+
		"- If you fan out, do NOT close this issue: it stays open until the spawned leaves land their own work and close their own sub-issues. Leave a summary comment instead. Your worktree branch may stay empty - that is fine, it reaps once merged (an empty branch is already an ancestor of main).\n"+
		"- Use `dispatch registry list` to see sibling sidequests before writing shared paths.\n"+
		"- Leave the worktree directory in place - the next `dispatch` run reaps it once the branch merges into main.\n",
		branch, repoPath, branch, repoPath, repoPath)
}
