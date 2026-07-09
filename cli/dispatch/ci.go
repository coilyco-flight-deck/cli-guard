package dispatch

import (
	"context"
	"fmt"
	"os"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"github.com/urfave/cli/v3"
)

// ci.go is the foreground / in-place surface: run claude -p inside a
// Forgejo Actions runner.

// ciCommand runs claude -p in the foreground, in place, against the issue,
// blocking until the agent exits so the Actions job tracks its lifecycle.
func (d *Dispatcher) ciCommand() *cli.Command {
	return &cli.Command{
		Name:      "ci",
		Usage:     "Run `claude -p` in the foreground, in place in the current checkout (for a CI runner).",
		ArgsUsage: "<owner/repo#N | issue-url>",
		Description: fmt.Sprintf(`ci is the foreground / in-place dispatch surface, for running inside a
Forgejo Actions runner rather than on a workstation. It resolves a GitHub
or Forgejo issue reference, refuses anything outside the %s org or any
issue that is not open, seeds the same prompt the headless surface uses
(issue title + body plus a git-workflow footer), then execs claude -p in
the FOREGROUND - stdio streams to the job log and the call blocks until
claude exits.

Unlike headless / cascade, ci makes no workstation assumptions:

  - it runs IN PLACE in the current checkout (the runner workspace, already
    a clone of the target repo) - no per-issue worktree under WorktreeRoot,
  - it does NOT detach - the job process is the agent lifecycle, so the
    Actions job stays alive for exactly as long as the agent runs,
  - it writes NO log file under LogRoot - the runner already captures stdio.

Because the job process is the agent, ci fails the Actions job on a nonzero
claude exit. The seeded footer tells the agent to land on green (commit and
push to main) and, when it cannot - validation keeps failing, or the issue
is blocked on a decision it cannot make - to file an autonomous-block:
issue and exit nonzero so the failure surfaces instead of pushing a broken
tree.

The child claude session is launched with --permission-mode (default
'auto') and --allowedTools (default Bash,Read,Edit,Write,Glob,Grep,
TodoWrite). Both are overridable per invocation.`, d.cfg.allowedOwnersLabel()),
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "print the resolved issue + seeded prompt + checkout path + flags, do not exec claude",
			},
			&cli.StringFlag{
				Name:  "claude-bin",
				Usage: "override the claude binary path (default: 'claude' on $PATH)",
				Value: "claude",
			},
			&cli.StringFlag{
				Name:  "permission-mode",
				Usage: "permission mode passed to the child claude session (auto, default, acceptEdits, plan, bypassPermissions). CI dispatch needs a non-prompting mode; default is auto.",
				Value: defaultDispatchPermissionMode,
			},
			&cli.StringFlag{
				Name:  "allowed-tools",
				Usage: "comma-separated allowedTools list passed to the child. Default covers the workflow footer: git, wrapped privileged ops, file edits, reads, todos.",
				Value: defaultDispatchAllowedTools,
			},
		},
		Action: d.cfg.Wrap(verb.Spec{
			Name:       "dispatch.ci",
			SkipPolicy: false,
			ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
				return map[string]string{
					"--claude-bin":      c.String("claude-bin"),
					"--permission-mode": c.String("permission-mode"),
					"--allowed-tools":   c.String("allowed-tools"),
				}, c.Args().Slice()
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				return d.runCI(ctx, c)
			},
		}),
	}
}

// runCI resolves the issue, seeds the in-place prompt, and runs claude -p
// in the foreground; a nonzero exit becomes an error so the job fails.
func (d *Dispatcher) runCI(ctx context.Context, c *cli.Command) error {
	args := c.Args().Slice()
	if len(args) != 1 {
		return fmt.Errorf("dispatch ci: pass exactly one issue reference (got %d args)", len(args))
	}
	// ci runs claude -p unattended, so it is autonomous and gates at the
	// headless ceiling.
	ref, issue, err := d.resolveDispatchIssue(ctx, args[0], levelHeadless)
	if err != nil {
		return err
	}

	// In place: the runner already checked the repo out into its workspace,
	// which is our cwd. No RepoPath lookup, no worktree.
	cwd, err := d.cfg.Getwd()
	if err != nil {
		return fmt.Errorf("dispatch ci: resolve current checkout: %w", err)
	}

	prompt := ciSeedPrompt(ref, issue)
	permMode := c.String("permission-mode")
	allowedTools := c.String("allowed-tools")

	if c.Bool("dry-run") {
		return printCIDryRun(c, ref, issue, cwd, permMode, allowedTools, prompt)
	}

	// Pre-trust the checkout so the foreground session never stalls on the
	// folder-trust prompt. Soft-fail: a missed trust prime is a papercut.
	if err := d.ensureClaudeFolderTrust(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "dispatch ci: could not pre-trust %s (%v); the session may show the folder-trust prompt.\n", cwd, err)
	}

	bin, argv := buildDispatchClaudeArgv(c.String("claude-bin"), prompt, permMode, allowedTools)

	fmt.Printf("dispatch ci: running claude for %s in place at %s\n", ref, cwd)
	code, err := d.cfg.SpawnForeground(ctx, cwd, bin, argv, d.cfg.Runner.Env)
	if err != nil {
		return fmt.Errorf("dispatch ci: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("dispatch ci: claude for %s exited %d - the agent files an autonomous-block: issue when it cannot land the work; see the job log above", ref, code)
	}
	d.notify(Event{Mode: "ci", Ref: ref.String(), Title: strings.TrimSpace(issue.Title), URL: issue.URL, Cwd: cwd})
	return nil
}

// ciSeedPrompt composes the prompt fed to the foreground claude -p, sharing
// the headless preamble and issue framing with the in-place CI footer.
func ciSeedPrompt(ref *issueRef, issue *ghIssue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", headlessPreamble)
	fmt.Fprintf(&b, "Work on %s issue %s.\n\n", forgeName(ref.Platform), ref)
	fmt.Fprintf(&b, "Title: %s\n", issue.Title)
	fmt.Fprintf(&b, "URL:   %s\n\n", issue.URL)
	fmt.Fprintf(&b, "Issue body:\n\n%s\n\n", strings.TrimSpace(issue.Body))
	fmt.Fprintf(&b, "%s", ciInPlaceFooter(ref.Number))
	return b.String()
}

// ciInPlaceFooter renders the git-workflow footer for a foreground CI worker:
// work in the runner checkout, land on green, autonomous-block on failure.
func ciInPlaceFooter(number int) string {
	return fmt.Sprintf("Workflow rules (from AGENTS.md):\n"+
		"- You are running inside a CI job (a Forgejo Actions runner), in place in the current checkout. There is no worktree and no detached log - this job process is your lifecycle. Work directly in this checkout and commit here.\n"+
		"- Run tests, linters, and builds without asking. Fix failures. Never use --no-verify.\n"+
		"- When the work is complete and verified (tests, linters, builds green), land it: commit, then `git push origin HEAD:main`. Resolve any merge conflicts yourself. Never force-push.\n"+
		"- If the push is rejected as non-fast-forward (someone pushed first), run `git pull --rebase origin main`, re-run tests/build, then push again. Repeat until it lands.\n"+
		"- If you cannot land the work - validation keeps failing and you cannot fix it, or the issue is blocked on a decision you cannot make - file an `autonomous-block:` issue in this repo describing the blocker, with a link back to this issue, then exit nonzero so the CI job reports the failure. Do not push a broken tree.\n"+
		"- Close the issue with a commit trailer that closes the issue: closes #%d (or fixes / resolves).\n",
		number)
}

// printCIDryRun renders the resolved CI dispatch plan without exec'ing claude.
func printCIDryRun(c *cli.Command, ref *issueRef, issue *Issue, cwd, permMode, allowedTools, prompt string) error {
	fmt.Printf("# dispatch ci (dry-run)\n")
	fmt.Printf("issue:           %s\n", ref)
	fmt.Printf("url:             %s\n", issue.URL)
	fmt.Printf("cwd:             %s\n", cwd)
	fmt.Printf("claude-bin:      %s\n", c.String("claude-bin"))
	fmt.Printf("permission-mode: %s\n", permMode)
	fmt.Printf("allowed-tools:   %s\n", allowedTools)
	fmt.Printf("----- seeded prompt -----\n%s\n----- end -----\n", prompt)
	return nil
}
