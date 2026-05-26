package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coilysiren/cli-guard/shell"
	"github.com/coilysiren/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

// Defaults for the dispatch <-> Warp seam. The agentic-os shim reads from
// the queue dir, the URI fires warp(preview)://(tab_config|launch)/<name>.
const (
	defaultDispatchQueueDir   = "/tmp/coily-dispatch-queue"
	defaultDispatchLaunchName = "claude-dispatch-interactive"
	defaultDispatchChannel    = "preview"
	defaultDispatchSurface    = "tab"
)

// dispatchQueueSchemaVersion lets the shim reject queue entries it does
// not understand once the schema evolves. Bump when adding required
const dispatchQueueSchemaVersion = 1

// dispatchQueueEntry is the JSON payload dispatch writes for the shim to
// consume. The shim reads ref, title, cwd, prompt via jq. Field tags are
type dispatchQueueEntry struct {
	SchemaVersion int    `json:"schema_version"`
	Ref           string `json:"ref"`
	Title         string `json:"title"`
	Cwd           string `json:"cwd"`
	Prompt        string `json:"prompt"`
}

// channelScheme maps the --channel flag to the URL scheme that lands in
// that Warp build. `warp://` always opens Stable, `warppreview://` always
func channelScheme(channel string) (string, error) {
	switch channel {
	case "preview":
		return "warppreview", nil
	case "stable":
		return "warp", nil
	default:
		return "", fmt.Errorf("invalid --channel %q (valid values: preview | stable)", channel)
	}
}

// surfacePath maps the --surface flag to the URI path segment Warp uses
// to pick between a new-tab fire (tab_config) and a new-window fire
func surfacePath(surface string) (string, error) {
	switch surface {
	case "tab":
		return "tab_config", nil
	case "window":
		return "launch", nil
	default:
		return "", fmt.Errorf("invalid --surface %q (valid values: tab | window)", surface)
	}
}

// dispatchURL builds the full warp(preview)://(tab_config|launch)/<name>
// URL fired by `open`. Channel picks the scheme, surface picks the path.
func dispatchURL(channel, surface, launchName string) (string, error) {
	scheme, err := channelScheme(channel)
	if err != nil {
		return "", err
	}
	path, err := surfacePath(surface)
	if err != nil {
		return "", err
	}
	return scheme + "://" + path + "/" + launchName, nil
}

// defaultOpenLaunch fires `open <url>` through the shell runner so the
// urfave/cli plumbing and audit row remain the production path. It is the
func defaultOpenLaunch(ctx context.Context, runner *shell.Runner, url string) error {
	return runner.Exec(ctx, "open", url)
}

// defaultWorktreeAdd runs `git -C <repoPath> worktree add -B <branch>
// <worktreePath>` through the shell runner. Default for
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
// number. Format: dispatch/issue-<N>. Predictable so re-dispatching the
func dispatchWorktreeBranch(number int) string {
	return fmt.Sprintf("dispatch/issue-%d", number)
}

// ensureDispatchWorktree returns worktreePath after guaranteeing a git
// worktree exists there. Idempotent: if a `.git` entry already lives at
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

// interactiveCommand spins up a new Warp tab cwd'd into the target repo
// with `claude "Work on issue <ref>"` already submitted as the first
func (d *Dispatcher) interactiveCommand() *cli.Command {
	return &cli.Command{
		Name:      "interactive",
		Usage:     "Open a new Warp tab with `claude \"Work on issue <ref>\"` pre-submitted.",
		ArgsUsage: "<owner/repo#N | issue-url>",
		Description: `interactive validates the issue ref exists and is open, writes a JSON
queue entry under the queue dir carrying ref, title, cwd, and prompt
(mode 0600), then fires open warppreview://tab_config/<launch-name> to
trigger the agentic-os shim that pops the queue, echoes the title
header, and execs claude inside the local checkout.

Concurrent dispatches land in separate Warp tabs without racing on a
single scratch path - the FIFO queue gives each shim its own entry.

Defaults to Warp Preview (--channel preview). Pass --channel stable to
target Warp Stable instead.

Defaults to a new tab inside the active window (--surface tab). Pass
--surface window to fall back to the launch path which opens a fresh
Warp window instead.

Hands control back immediately. The dispatched session runs in the
foreground in its own Warp tab or window. If you need an AFK
fire-and-forget run, use 'dispatch headless'.

Soft-fails to a copy-paste fallback if Warp / open are unavailable.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "print the resolved issue + queue entry + URL, do not write the queue entry or fire open",
			},
			&cli.StringFlag{
				Name:  "queue-dir",
				Usage: "override the dispatch queue directory. Must match the path the Warp shim reads.",
				Value: defaultDispatchQueueDir,
			},
			&cli.StringFlag{
				Name:  "launch-name",
				Usage: "override the Warp config name fired via warp(preview)://(tab_config|launch)/<name>.",
				Value: defaultDispatchLaunchName,
			},
			&cli.StringFlag{
				Name:  "channel",
				Usage: "Warp channel to fire the URL into. `preview` (default, daily driver) or `stable` (fallback).",
				Value: defaultDispatchChannel,
			},
			&cli.StringFlag{
				Name:  "surface",
				Usage: "URI path that picks tab vs window fire. `tab` (default, opens a new tab) or `window` (fallback, opens a fresh window).",
				Value: defaultDispatchSurface,
			},
			&cli.BoolFlag{
				Name:  "no-worktree",
				Usage: "skip git worktree creation and cd the dispatched session straight into the bare repo checkout. Escape hatch for sessions that must work against the canonical tree; concurrent dispatches into the same repo will race on the working tree.",
			},
		},
		Action: d.cfg.Wrap(verb.Spec{
			Name:       "dispatch.interactive",
			SkipPolicy: false,
			ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
				return map[string]string{
					"--queue-dir":   c.String("queue-dir"),
					"--launch-name": c.String("launch-name"),
					"--channel":     c.String("channel"),
					"--surface":     c.String("surface"),
				}, c.Args().Slice()
			},
			CommitScopeArgvHint: d.commitScopeArgvHint,
			Action: func(ctx context.Context, c *cli.Command) error {
				return d.runInteractive(ctx, c)
			},
		}),
	}
}

// interactivePrompt is the prompt the shim execs claude with. Embeds the
// first-action instruction so the dispatched agent fetches issue body +
func interactivePrompt(ref *issueRef, issue *ghIssue, noWorktree bool, repoPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"Work on issue %s. First action: run `coily ops gh issue view %s --comments` and read the full body and comment thread before doing anything else.",
		ref, issue.URL)
	if !noWorktree {
		branch := dispatchWorktreeBranch(ref.Number)
		fmt.Fprintf(&b,
			"\n\nThis session runs in a git worktree on branch `%s`. When the work is complete and verified (tests, linters, and builds green), land it autonomously without checking in first: merge that branch into `main` and push. Run `git -C %s merge %s` then `git -C %s push origin main`. Resolve any merge conflicts yourself. Never force-push. Leave the worktree directory in place - the next `coily dispatch` reaps it once the merge lands.",
			branch, repoPath, branch, repoPath)
	}
	return b.String()
}

// interactiveTitleLine is the self-identifying header the shim echoes in
// the dispatched tab before exec'ing claude. Gives the operator an
func interactiveTitleLine(ref *issueRef, issue *ghIssue) string {
	return fmt.Sprintf("%s: %s", ref, strings.TrimSpace(issue.Title))
}

// resolveDispatchCwd returns the directory the dispatched session should
// run in. With --no-worktree, that's the bare repo checkout. Otherwise
func (d *Dispatcher) resolveDispatchCwd(ctx context.Context, repoPath string, ref *issueRef, noWorktree, dryRun bool) (string, error) {
	if noWorktree {
		return repoPath, nil
	}
	if dryRun {
		return d.dispatchWorktreePath(ref.Repo, ref.Number)
	}
	return d.ensureDispatchWorktree(ctx, repoPath, ref)
}

func (d *Dispatcher) runInteractive(ctx context.Context, c *cli.Command) error {
	args := c.Args().Slice()
	if len(args) != 1 {
		return fmt.Errorf("dispatch interactive: pass exactly one issue reference (got %d args)", len(args))
	}
	ref, issue, err := d.resolveDispatchIssue(ctx, args[0])
	if err != nil {
		return err
	}

	noWorktree := c.Bool("no-worktree")
	queueDir := c.String("queue-dir")
	launchName := c.String("launch-name")
	channel := c.String("channel")
	surface := c.String("surface")
	repoPath, _ := d.cfg.RepoPath(ref.Repo)
	prompt := interactivePrompt(ref, issue, noWorktree, repoPath)
	titleLine := interactiveTitleLine(ref, issue)

	// Reap merged worktrees from prior dispatches before creating this
	// one, so worktree sprawl stays self-limiting. Soft-fail: a reaper
	if !c.Bool("dry-run") {
		if removed, reapErr := d.reapDispatchWorktrees(ctx); reapErr != nil {
			fmt.Fprintf(os.Stderr, "dispatch interactive: worktree reap skipped (%v)\n", reapErr)
		} else if len(removed) > 0 {
			fmt.Fprintf(os.Stderr, "dispatch interactive: reaped %d merged worktree(s)\n", len(removed))
		}
	}

	url, err := dispatchURL(channel, surface, launchName)
	if err != nil {
		return fmt.Errorf("dispatch interactive: %w", err)
	}

	// Each dispatch lands in its own git worktree branched off main, so
	// concurrent dispatches into the same repo never collide on a shared
	cwd, err := d.resolveDispatchCwd(ctx, repoPath, ref, noWorktree, c.Bool("dry-run"))
	if err != nil {
		return fmt.Errorf("dispatch interactive: %w", err)
	}

	entry := dispatchQueueEntry{
		SchemaVersion: dispatchQueueSchemaVersion,
		Ref:           ref.String(),
		Title:         strings.TrimSpace(issue.Title),
		Cwd:           cwd,
		Prompt:        prompt,
	}

	if c.Bool("dry-run") {
		entryJSON, _ := json.MarshalIndent(entry, "", "  ")
		fmt.Printf("# dispatch interactive (dry-run)\n")
		fmt.Printf("issue:        %s\n", ref)
		fmt.Printf("url:          %s\n", issue.URL)
		fmt.Printf("cwd:          %s\n", cwd)
		fmt.Printf("queue-dir:    %s\n", queueDir)
		fmt.Printf("channel:      %s\n", channel)
		fmt.Printf("surface:      %s\n", surface)
		fmt.Printf("dispatch-url: %s\n", url)
		fmt.Printf("----- title -----\n%s\n----- end -----\n", titleLine)
		fmt.Printf("----- queue entry -----\n%s\n----- end -----\n", string(entryJSON))
		return nil
	}

	// Pre-trust the cwd so the dispatched session never stalls on the
	// folder-trust prompt. Soft-fail: a missed trust prime is a papercut.
	if err := d.ensureClaudeFolderTrust(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "dispatch interactive: could not pre-trust %s (%v); the dispatched session may show the folder-trust prompt.\n", cwd, err)
	}

	queuePath, err := writeDispatchQueueEntry(queueDir, entry)
	if err != nil {
		return fmt.Errorf("dispatch interactive: write queue entry under %s: %w", queueDir, err)
	}

	if err := d.cfg.OpenLaunch(ctx, d.cfg.Runner, url); err != nil {
		printInteractiveFallback(ref, cwd, prompt, url, queuePath, err)
		return nil
	}
	d.notify(Event{Mode: "interactive", Ref: ref.String(), Title: strings.TrimSpace(issue.Title), URL: issue.URL, Cwd: cwd})
	return nil
}

// writeDispatchQueueEntry writes a single JSON file to dir named
// <unix-nanos>-<8hex>.json with mode 0600. Atomic via tmp-write + rename
func writeDispatchQueueEntry(dir string, entry dispatchQueueEntry) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir queue dir: %w", err)
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("marshal queue entry: %w", err)
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("rand suffix: %w", err)
	}
	name := fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), hex.EncodeToString(suffix))
	finalPath := filepath.Join(dir, name)
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return "", fmt.Errorf("write tmp queue entry: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename tmp to final: %w", err)
	}
	return finalPath, nil
}

// printInteractiveFallback emits the exact command the operator can paste
// in a manually-opened tab when the Warp URL fire fails. Soft-fail by
func printInteractiveFallback(ref *issueRef, repoPath, prompt, url, queuePath string, openErr error) {
	fmt.Fprintf(os.Stderr, "dispatch interactive: %s did not fire (%v).\n", url, openErr)
	fmt.Fprintf(os.Stderr, "Queue entry left at %s for the shim to consume on retry.\n", queuePath)
	fmt.Fprintf(os.Stderr, "Or paste this in a new tab cwd'd to %s:\n\n", repoPath)
	fmt.Fprintf(os.Stderr, "  cd %s && claude %q\n\n", repoPath, prompt)
	fmt.Fprintf(os.Stderr, "Issue: %s\n", ref)
}
