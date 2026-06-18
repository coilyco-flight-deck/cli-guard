// Package dispatch hosts the reusable dispatch subsystem: fire `claude`
// against a real open GitHub issue, in the matching local checkout,
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/ghcache"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/ghratelimit"
	"github.com/urfave/cli/v3"
)

// Config carries the host-specific seams the dispatch package refuses to
// hard-code. Required fields are noted; everything else has a documented
type Config struct {
	// Runner executes subprocesses (gh, git, open). Required.
	Runner *shell.Runner

	// Wrap applies the consumer's verb pipeline (argv validation, audit
	// logging) to a verb.Spec the package builds.
	Wrap func(verb.Spec) cli.ActionFunc

	// AllowedOwner is the primary org dispatch accepts issue refs from, and the
	// owner named in help and status text. This is the security claim, not a knob.
	AllowedOwner string

	// AllowedOwners is an optional set of additional owners dispatch accepts
	// alongside AllowedOwner. Empty means single-owner (AllowedOwner only).
	AllowedOwners []string

	// ForgejoBaseURL enables Forgejo issue refs when set (scheme://host).
	// Requires FetchForgejoIssue.
	ForgejoBaseURL string

	// FetchForgejoIssue resolves a Forgejo issue. 404-shaped errors
	// (message contains "404") fall back to GitHub for shortform refs.
	FetchForgejoIssue func(ctx context.Context, owner, repo string, number int) (*Issue, error)

	// IssueFetcher overrides the default gh resolver for GitHub and shortform
	// refs. Unset keeps gh (GitHub is the right default for a reusable library).
	IssueFetcher func(ctx context.Context, owner, repo string, number int) (*Issue, error)

	// RepoPath resolves a repo name to its expected local checkout. The
	// consumer owns the workspace layout. Required.
	RepoPath func(repo string) (string, error)

	// WorktreeRoot is the parent directory under which each interactive
	// dispatch gets its own git worktree. Required.
	WorktreeRoot func() (string, error)

	// LogRoot is the parent directory for headless dispatch log files.
	// Required.
	LogRoot func() (string, error)

	// BinaryName is the host CLI's name, used only to format help text so
	// it reads correctly per consumer ("<bin> dispatch ..." vs
	BinaryName string

	// ClaudeConfigPath resolves the Claude Code config file holding
	// per-folder trust state. Optional; defaults to ~/.claude.json.
	ClaudeConfigPath func() (string, error)

	// Notify, when set, is called once per completed dispatch with a
	// summary Event. The consumer wires ntfy, a done banner, or any other
	Notify func(Event)

	// Seams below are pluggable so tests avoid spawning real processes or
	// shelling out to git. Production leaves them nil and New installs the
	SpawnDetached    func(repoPath, logPath, bin string, argv, env []string) (int, error)
	OpenLaunch       func(ctx context.Context, runner *shell.Runner, url string) error
	WorktreeAdd      func(ctx context.Context, runner *shell.Runner, repoPath, branch, worktreePath string) error
	WorktreeReapable func(ctx context.Context, runner *shell.Runner, repoPath, branch string) bool
	WorktreeRemove   func(ctx context.Context, runner *shell.Runner, repoPath, worktreePath, branch string) error

	// SpawnHealthWindow bounds the immediate-crash watch. Zero
	// applies defaultSpawnHealthWindow; negative disables the check.
	SpawnHealthWindow time.Duration

	// ProcessAlive reports whether pid is live. Seam for the health check;
	// defaults to processRunning (the signal-0 probe).
	ProcessAlive func(pid int) bool
}

// Event is the summary handed to Config.Notify when a dispatch completes.
type Event struct {
	Mode  string // "headless" or "interactive"
	Ref   string // owner/repo#N
	Title string
	URL   string
	Cwd   string
	PID   int // headless only; 0 otherwise
}

// Dispatcher is a configured dispatch subsystem. Build one with New and
// hang Command off the host CLI's command tree.
type Dispatcher struct {
	cfg Config
}

// New validates cfg, fills defaults, and returns a Dispatcher. It errors
// rather than panicking so the host CLI can fail loud at startup.
func New(cfg Config) (*Dispatcher, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	applyConfigDefaults(&cfg)
	return &Dispatcher{cfg: cfg}, nil
}

// validateConfig refuses a Config missing any required seam.
func validateConfig(cfg Config) error {
	switch {
	case cfg.Runner == nil:
		return fmt.Errorf("dispatch: Config.Runner is required")
	case cfg.Wrap == nil:
		return fmt.Errorf("dispatch: Config.Wrap is required (dispatch must not run unaudited)")
	case strings.TrimSpace(cfg.AllowedOwner) == "":
		return fmt.Errorf("dispatch: Config.AllowedOwner is required")
	case cfg.RepoPath == nil:
		return fmt.Errorf("dispatch: Config.RepoPath is required")
	case cfg.WorktreeRoot == nil:
		return fmt.Errorf("dispatch: Config.WorktreeRoot is required")
	case cfg.LogRoot == nil:
		return fmt.Errorf("dispatch: Config.LogRoot is required")
	case cfg.ForgejoBaseURL != "" && cfg.FetchForgejoIssue == nil:
		return fmt.Errorf("dispatch: Config.FetchForgejoIssue is required when ForgejoBaseURL is set")
	}
	return nil
}

// applyConfigDefaults fills every optional seam left nil with the real
// implementation, so the rest of the package never branches on nil.
func applyConfigDefaults(cfg *Config) {
	if cfg.BinaryName == "" {
		cfg.BinaryName = "dispatch"
	}
	if cfg.ClaudeConfigPath == nil {
		cfg.ClaudeConfigPath = defaultClaudeConfigPath
	}
	if cfg.SpawnDetached == nil {
		cfg.SpawnDetached = spawnDetachedClaude
	}
	if cfg.OpenLaunch == nil {
		cfg.OpenLaunch = defaultOpenLaunch
	}
	if cfg.WorktreeAdd == nil {
		cfg.WorktreeAdd = defaultWorktreeAdd
	}
	if cfg.WorktreeReapable == nil {
		cfg.WorktreeReapable = defaultWorktreeReapable
	}
	if cfg.WorktreeRemove == nil {
		cfg.WorktreeRemove = defaultWorktreeRemove
	}
	if cfg.SpawnHealthWindow == 0 {
		cfg.SpawnHealthWindow = defaultSpawnHealthWindow
	}
	if cfg.ProcessAlive == nil {
		cfg.ProcessAlive = processRunning
	}
}

// defaultDispatchPermissionMode is the strictest mode that still lets the
// headless child make progress without an operator to approve prompts.
const defaultDispatchPermissionMode = "auto"

// defaultDispatchAllowedTools is the baseline tool set the seeded prompt
// footer assumes. It covers the workflow that closes an issue: git
const defaultDispatchAllowedTools = "Bash,Read,Edit,Write,Glob,Grep,TodoWrite"

// Command returns the dispatch umbrella verb. It refuses bare invocation and
// requires a surface subverb (headless | interactive | consult | cascade).
func (d *Dispatcher) Command() *cli.Command {
	bin := d.cfg.BinaryName
	return &cli.Command{
		Name:      "dispatch",
		Usage:     "Fire claude against a real open issue. Surface required: headless | interactive | consult | cascade.",
		ArgsUsage: "<headless|interactive|consult|cascade> <owner/repo#N | issue-url>",
		Description: fmt.Sprintf(`dispatch resolves a GitHub issue reference, refuses anything outside the
%s org or any issue that is not open, then hands off to one of four
surface subverbs:

  %s dispatch headless    <ref>   Spawn a detached claude -p in a
                                  per-issue worktree+branch, log to a
                                  file, return immediately. AFK queue
                                  work, never consults. Lands by merging
                                  its branch into main when green.
  %s dispatch interactive <ref>   Open a new tab cwd'd into the canonical
                                  checkout (default branch, no worktree)
                                  with claude pre-submitted "Work on issue
                                  <ref>". Auto mode; the operator may watch
                                  but is not consulted.
  %s dispatch consult     <ref>   Like interactive, but with a raised
                                  interruption budget: the agent is
                                  encouraged to surface real judgment
                                  calls and wait. A soft expectation,
                                  not plan mode.
  %s dispatch cascade     <ref>   Like headless, but the worker may
                                  recursively dispatch its own sub-workers
                                  to split a too-large task. Bounded by a
                                  hard depth budget. For mass migrations.

It also carries three maintenance verbs:

  %s dispatch reap                Remove dispatch worktrees whose branch
                                  is already merged into main.
  %s dispatch status              Show pid + log tail for a headless
                                  dispatch (most recent, by ref, or by pid).
  %s dispatch registry            List active sidequests (headless dispatches
                                  whose pid is still alive), so a parent agent
                                  or sibling sidequest can see who is editing
                                  what before writing shared paths.

Bare 'dispatch <ref>' errors. Pick a surface.`, d.cfg.allowedOwnersLabel(), bin, bin, bin, bin, bin, bin, bin),
		Commands: []*cli.Command{
			d.headlessCommand(),
			d.interactiveCommand(),
			d.consultCommand(),
			d.cascadeCommand(),
			d.reapCommand(),
			d.statusCommand(),
			d.registryCommand(),
		},
		Action: func(_ context.Context, _ *cli.Command) error {
			return fmt.Errorf("dispatch: specify surface: headless | interactive | consult | cascade (see `%s dispatch --help`)", bin)
		},
	}
}

// headlessCommand fires `claude -p` against the issue in the local
// checkout as a fully detached, fire-and-forget child: new session, own
func (d *Dispatcher) headlessCommand() *cli.Command {
	return &cli.Command{
		Name:      "headless",
		Usage:     "Spawn a detached `claude -p` against a real open issue.",
		ArgsUsage: "<owner/repo#N | issue-url>",
		Description: fmt.Sprintf(`headless resolves a GitHub issue reference, refuses anything outside the
%s org or any issue that is not open, seeds a prompt from the issue
title and body plus the standard git-workflow footer, then spawns
claude -p inside a per-issue git worktree branched off the canonical
checkout. Concurrent headless dispatches into one repo each get their
own working tree and index, so they never corrupt each other - the
worker lands by merging its branch into main when the work is green.

The child is fully detached: it runs in a new session with its own
process group, its stdio goes to a per-dispatch log file rather than the
parent terminal, and the parent does not wait on it. dispatch prints the
child PID and log path and returns immediately - the run survives the
terminal closing. This is what makes headless truly fire-and-forget.

Refuses to run if the local checkout is missing - clone the repo first,
then re-run dispatch.

The child claude session is launched with --permission-mode (default
'auto') and --allowedTools (default Bash,Read,Edit,Write,Glob,Grep,
TodoWrite). Both are overridable per invocation so target repos that
need a wider tool set can opt in without editing dispatch.`, d.cfg.allowedOwnersLabel()),
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "print the resolved issue + seeded prompt + repo path + flags, do not exec claude",
			},
			&cli.StringFlag{
				Name:  "claude-bin",
				Usage: "override the claude binary path (default: 'claude' on $PATH)",
				Value: "claude",
			},
			&cli.StringFlag{
				Name:  "permission-mode",
				Usage: "permission mode passed to the child claude session (auto, default, acceptEdits, plan, bypassPermissions). Headless dispatch needs a non-prompting mode; default is auto.",
				Value: defaultDispatchPermissionMode,
			},
			&cli.StringFlag{
				Name:  "allowed-tools",
				Usage: "comma-separated allowedTools list passed to the child. Default covers the workflow footer: git, wrapped privileged ops, file edits, reads, todos.",
				Value: defaultDispatchAllowedTools,
			},
			&cli.StringFlag{
				Name:  "claims",
				Usage: "comma-separated paths this sidequest will edit. Relative paths resolve against the dispatch caller's cwd. Surfaced via `dispatch registry list|check` so a parent or sibling can detect overlap.",
			},
		},
		Action: d.cfg.Wrap(verb.Spec{
			Name:       "dispatch.headless",
			SkipPolicy: false,
			ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
				return map[string]string{
					"--claude-bin":      c.String("claude-bin"),
					"--permission-mode": c.String("permission-mode"),
					"--allowed-tools":   c.String("allowed-tools"),
					"--claims":          c.String("claims"),
				}, c.Args().Slice()
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				return d.runHeadless(ctx, c)
			},
		}),
	}
}

// Platform tags which forge an issue ref resolves against.
type Platform string

const (
	PlatformGitHub  Platform = "github"
	PlatformForgejo Platform = "forgejo"
)

// issueRef is the parsed shape of an issue reference. Empty Platform
// means shortform - resolveDispatchIssue picks a forge.
type issueRef struct {
	Owner    string
	Repo     string
	Number   int
	Platform Platform
}

func (i issueRef) String() string {
	return fmt.Sprintf("%s/%s#%d", i.Owner, i.Repo, i.Number)
}

// issueRefShortRE matches owner/repo#N.
var issueRefShortRE = regexp.MustCompile(`^([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)#(\d+)$`)

// issueRefURLRE matches https://github.com/owner/repo/issues/N.
var issueRefURLRE = regexp.MustCompile(`^https?://github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/issues/(\d+)/?$`)

// forgejoURLRE matches <ForgejoBaseURL>/owner/repo/issues/N. Built
// dynamically because the base URL is host-configurable.
func forgejoURLRE(baseURL string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(strings.TrimRight(baseURL, "/")) +
		`/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/issues/(\d+)/?$`)
}

// parseIssueRef accepts the supported reference forms. Forgejo URLs
// require d.cfg.ForgejoBaseURL to be set.
func (d *Dispatcher) parseIssueRef(s string) (*issueRef, error) {
	s = strings.TrimSpace(s)
	if m := issueRefShortRE.FindStringSubmatch(s); m != nil {
		return buildRef(m, "", s)
	}
	if m := issueRefURLRE.FindStringSubmatch(s); m != nil {
		return buildRef(m, PlatformGitHub, s)
	}
	if d.cfg.ForgejoBaseURL != "" {
		if m := forgejoURLRE(d.cfg.ForgejoBaseURL).FindStringSubmatch(s); m != nil {
			return buildRef(m, PlatformForgejo, s)
		}
	}
	want := "owner/repo#N or https://github.com/owner/repo/issues/N"
	if d.cfg.ForgejoBaseURL != "" {
		want += " or " + strings.TrimRight(d.cfg.ForgejoBaseURL, "/") + "/owner/repo/issues/N"
	}
	return nil, fmt.Errorf("dispatch: not an issue reference (want %s): %q", want, s)
}

func buildRef(m []string, platform Platform, s string) (*issueRef, error) {
	n := 0
	if _, err := fmt.Sscanf(m[3], "%d", &n); err != nil {
		return nil, fmt.Errorf("dispatch: parse issue number in %q: %w", s, err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("dispatch: issue number must be positive: %q", s)
	}
	return &issueRef{Owner: m[1], Repo: m[2], Number: n, Platform: platform}, nil
}

// Issue is the platform-neutral fetch result. GitHub and Forgejo share
// the same JSON field names so one struct covers both.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"html_url"`
}

// ghIssue keeps the internal name for older call sites.
type ghIssue = Issue

// ownerAllowed reports whether owner is the primary AllowedOwner or a member
// of the optional AllowedOwners set.
func (c Config) ownerAllowed(owner string) bool {
	if owner == c.AllowedOwner {
		return true
	}
	for _, o := range c.AllowedOwners {
		if owner == o {
			return true
		}
	}
	return false
}

// allowedOwnersLabel renders the accepted-owner set for the refusal message:
// "<owner>/*" for a single owner, or "{a, b, c}/*" when AllowedOwners adds more.
func (c Config) allowedOwnersLabel() string {
	if len(c.AllowedOwners) == 0 {
		return c.AllowedOwner + "/*"
	}
	owners := append([]string{c.AllowedOwner}, c.AllowedOwners...)
	return "{" + strings.Join(owners, ", ") + "}/*"
}

// resolveDispatchIssue parses the ref, refuses non-allowed-owner and
// non-open issues, and returns both the ref and the fetched issue. Shared
func (d *Dispatcher) resolveDispatchIssue(ctx context.Context, raw string) (*issueRef, *ghIssue, error) {
	ref, err := d.parseIssueRef(raw)
	if err != nil {
		return nil, nil, err
	}
	if !d.cfg.ownerAllowed(ref.Owner) {
		return nil, nil, fmt.Errorf("dispatch: refusing to dispatch outside %s (got %s)", d.cfg.allowedOwnersLabel(), ref.Owner)
	}
	issue, err := d.fetchIssueForRef(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if !strings.EqualFold(issue.State, "OPEN") {
		return nil, nil, fmt.Errorf("dispatch: refusing to dispatch against non-open issue %s (state=%s)", ref, issue.State)
	}
	return ref, issue, nil
}

// fetchIssueForRef routes to the matching forge. Shortform refs prefer
// Forgejo when configured and fall back to GitHub on 404.
func (d *Dispatcher) fetchIssueForRef(ctx context.Context, ref *issueRef) (*ghIssue, error) {
	switch ref.Platform {
	case PlatformGitHub:
		return d.resolveIssue(ctx, ref)
	case PlatformForgejo:
		return d.cfg.FetchForgejoIssue(ctx, ref.Owner, ref.Repo, ref.Number)
	}
	if d.cfg.FetchForgejoIssue != nil {
		issue, err := d.cfg.FetchForgejoIssue(ctx, ref.Owner, ref.Repo, ref.Number)
		if err == nil {
			ref.Platform = PlatformForgejo
			return issue, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
	}
	issue, err := d.resolveIssue(ctx, ref)
	if err != nil {
		return nil, err
	}
	ref.Platform = PlatformGitHub
	return issue, nil
}

// resolveIssue calls the consumer's IssueFetcher when set, else the gh
// default. The Forgejo path keeps its own FetchForgejoIssue seam.
func (d *Dispatcher) resolveIssue(ctx context.Context, ref *issueRef) (*ghIssue, error) {
	if d.cfg.IssueFetcher != nil {
		return d.cfg.IssueFetcher(ctx, ref.Owner, ref.Repo, ref.Number)
	}
	return d.fetchIssue(ctx, ref)
}

// isNotFound matches host-wrapped 404 errors so we can fall back forges.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "404")
}

func (d *Dispatcher) runHeadless(ctx context.Context, c *cli.Command) error {
	return d.runDetached(ctx, c, detachedSpec{
		mode:     "headless",
		prompt:   seedPrompt,
		extraEnv: []string{fmt.Sprintf("%s=0", envCascadeDepth)},
	})
}

// detachedSpec carries the per-mode bits that differ between the detached
// surfaces (headless, cascade). Everything else - resolve, stat, trust,
type detachedSpec struct {
	// mode labels output and the audit Event ("headless" | "cascade").
	mode string
	// prompt builds the seeded prompt fed to the detached claude -p.
	// repoPath is the canonical checkout the worker merges its branch into.
	prompt func(ref *issueRef, issue *ghIssue, repoPath string) string
	// extraEnv is appended to the child env. Carries the cascade depth
	// budget so nested dispatches can enforce the recursion floor.
	extraEnv []string
}

// runDetached is the shared body for the fire-and-forget surfaces. It
// resolves the issue, refuses a missing checkout, then either prints the
func (d *Dispatcher) runDetached(ctx context.Context, c *cli.Command, spec detachedSpec) error {
	args := c.Args().Slice()
	if len(args) != 1 {
		return fmt.Errorf("dispatch %s: pass exactly one issue reference (got %d args)", spec.mode, len(args))
	}
	ref, issue, err := d.resolveDispatchIssue(ctx, args[0])
	if err != nil {
		return err
	}
	repoPath, err := d.cfg.RepoPath(ref.Repo)
	if err != nil {
		return fmt.Errorf("dispatch %s: resolve local repo path: %w", spec.mode, err)
	}
	if st, statErr := os.Stat(repoPath); statErr != nil || !st.IsDir() {
		return fmt.Errorf("dispatch %s: local checkout missing at %s. Clone it first: gh repo clone %s/%s %s",
			spec.mode, repoPath, ref.Owner, ref.Repo, repoPath)
	}

	prompt := spec.prompt(ref, issue, repoPath)
	permMode := c.String("permission-mode")
	allowedTools := c.String("allowed-tools")

	// Detached surfaces each get their own worktree+branch so concurrent
	// workers never share a working tree.
	if !c.Bool("dry-run") {
		d.reapBeforeDetachedDispatch(ctx, spec.mode)
	}
	cwd, err := d.resolveDetachedCwd(ctx, repoPath, ref, issue.Title, c.Bool("dry-run"))
	if err != nil {
		return fmt.Errorf("dispatch %s: %w", spec.mode, err)
	}

	if c.Bool("dry-run") {
		return printDetachedDryRun(spec.mode, c, ref, issue, cwd, permMode, allowedTools, prompt)
	}

	return d.spawnDetachedWorker(c, spec, ref, issue, cwd, prompt, permMode, allowedTools)
}

// spawnDetachedWorker pre-trusts the worktree, spawns the detached child,
// and records the status sidecar. Split out to keep runDetached's complexity down.
func (d *Dispatcher) spawnDetachedWorker(c *cli.Command, spec detachedSpec, ref *issueRef, issue *ghIssue, cwd, prompt, permMode, allowedTools string) error {
	// Pre-trust the worktree so the detached child never stalls on the
	// folder-trust prompt. Soft-fail.
	if err := d.ensureClaudeFolderTrust(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "dispatch %s: could not pre-trust %s (%v).\n", spec.mode, cwd, err)
	}

	claims, err := parseClaims(c.String("claims"))
	if err != nil {
		return fmt.Errorf("dispatch %s: --claims: %w", spec.mode, err)
	}

	bin, argv := buildDispatchClaudeArgv(c.String("claude-bin"), prompt, permMode, allowedTools)

	logPath, err := d.dispatchLogPath(ref.Repo, ref.Number)
	if err != nil {
		return fmt.Errorf("dispatch %s: resolve log path: %w", spec.mode, err)
	}
	pid, err := d.cfg.SpawnDetached(cwd, logPath, bin, argv, detachedEnv(d.cfg.Runner.Env, spec.extraEnv))
	if err != nil {
		return fmt.Errorf("dispatch %s: %w", spec.mode, err)
	}
	// Persist pid + spawn time alongside the log so `dispatch status`
	// can render RUNNING/EXITED without scraping ps. Soft-fail: a missed
	if err := writeDispatchMeta(logPath, dispatchMeta{
		PID:           pid,
		StartedAt:     time.Now().UTC(),
		Ref:           ref.String(),
		URL:           issue.URL,
		ParentSession: os.Getenv("CLAUDE_CODE_SESSION_ID"),
		PathsClaimed:  claims,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "dispatch %s: could not write status sidecar (%v); `dispatch status` will report pid unknown.\n", spec.mode, err)
	}
	// A startup crash exits within seconds having done no work; surface it
	// as a nonzero failure rather than the cheerful "spawned".
	if d.watchForImmediateExit(pid) {
		return d.reportImmediateExit(spec.mode, ref, logPath, pid)
	}
	fmt.Printf("dispatch %s: spawned claude for %s (pid %d)\n", spec.mode, ref, pid)
	fmt.Printf("  cwd: %s\n", cwd)
	fmt.Printf("  log: %s\n", logPath)
	fmt.Printf("  detached - survives this terminal closing. Follow with: tail -f %s\n", logPath)
	d.notify(Event{Mode: spec.mode, Ref: ref.String(), Title: strings.TrimSpace(issue.Title), URL: issue.URL, Cwd: cwd, PID: pid})
	return nil
}

// defaultSpawnHealthWindow watches a fresh detached child for a startup
// crash; a healthy claude -p run far outlasts it.
const defaultSpawnHealthWindow = 4 * time.Second

// spawnHealthPollInterval is the pid re-probe cadence within the window.
const spawnHealthPollInterval = 200 * time.Millisecond

// watchForImmediateExit reports true if pid dies within the health window -
// the signature of a startup crash. Non-positive window disables the check.
func (d *Dispatcher) watchForImmediateExit(pid int) bool {
	window := d.cfg.SpawnHealthWindow
	if window <= 0 || pid <= 0 {
		return false
	}
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if !d.cfg.ProcessAlive(pid) {
			return true
		}
		time.Sleep(spawnHealthPollInterval)
	}
	return !d.cfg.ProcessAlive(pid)
}

// reportImmediateExit builds the failure for a child that died inside the
// health window, folding the log tail in so the caller sees why.
func (d *Dispatcher) reportImmediateExit(mode string, ref *issueRef, logPath string, pid int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dispatch %s: child for %s (pid %d) exited within %s of spawn - no work done, no branch/PR.\n",
		mode, ref, pid, d.cfg.SpawnHealthWindow)
	fmt.Fprintf(&b, "  log: %s", logPath)
	if tail, err := tailLines(logPath, dispatchStatusTailLines); err == nil && len(tail) > 0 {
		b.WriteString("\n  log tail:")
		for _, l := range tail {
			fmt.Fprintf(&b, "\n    %s", l)
		}
	}
	return fmt.Errorf("%s", b.String())
}

// detachedEnv appends extraEnv to base. Returns nil only when both are
// empty so spawnDetachedClaude keeps inheriting the parent environment.
func detachedEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

// printDetachedDryRun renders the resolved dispatch plan without spawning.
func printDetachedDryRun(mode string, c *cli.Command, ref *issueRef, issue *Issue, cwd, permMode, allowedTools, prompt string) error {
	claims, err := parseClaims(c.String("claims"))
	if err != nil {
		return fmt.Errorf("dispatch %s: --claims: %w", mode, err)
	}
	fmt.Printf("# dispatch %s (dry-run)\n", mode)
	fmt.Printf("issue:           %s\n", ref)
	fmt.Printf("url:             %s\n", issue.URL)
	fmt.Printf("cwd:             %s\n", cwd)
	fmt.Printf("permission-mode: %s\n", permMode)
	fmt.Printf("allowed-tools:   %s\n", allowedTools)
	fmt.Printf("claims:          %s\n", strings.Join(claims, ","))
	fmt.Printf("----- seeded prompt -----\n%s\n----- end -----\n", prompt)
	return nil
}

// notify calls Config.Notify if the consumer wired one.
func (d *Dispatcher) notify(e Event) {
	if d.cfg.Notify != nil {
		d.cfg.Notify(e)
	}
}

// dispatchLogPath returns the per-dispatch log file path. Format:
// <LogRoot>/<repo>/issue-<N>-<YYYYMMDD-HHMMSS>.log. The timestamp keeps
func (d *Dispatcher) dispatchLogPath(repo string, number int) (string, error) {
	root, err := d.cfg.LogRoot()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("issue-%d-%s.log", number, time.Now().Format("20060102-150405"))
	return filepath.Join(root, repo, name), nil
}

// spawnDetachedClaude starts `claude -p` fully detached: a new session
// (see detachSysProcAttr), its own process group, stdio redirected to
func spawnDetachedClaude(repoPath, logPath, bin string, argv, env []string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, fmt.Errorf("mkdir dispatch log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open dispatch log %s: %w", logPath, err)
	}
	defer logFile.Close()

	binPath, err := exec.LookPath(bin)
	if err != nil {
		return 0, fmt.Errorf("resolve %s on PATH: %w", bin, err)
	}

	// Plain exec.Command, not CommandContext: the child must outlive the
	// parent process, so it must not be bound to the parent's context.
	cmd := exec.Command(binPath, argv...)
	cmd.Dir = repoPath
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachSysProcAttr()
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start detached claude: %w", err)
	}
	pid := cmd.Process.Pid
	// Release the child handle so the parent does not have to Wait() and
	// the child is reparented to init rather than zombied on parent exit.
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("release detached claude (pid %d): %w", pid, err)
	}
	return pid, nil
}

// buildDispatchClaudeArgv resolves the child claude binary and argv.
// Pulled out of runHeadless so the per-flag conditionals don't push the
func buildDispatchClaudeArgv(claudeBin, prompt, permMode, allowedTools string) (string, []string) {
	if claudeBin == "" {
		claudeBin = "claude"
	}
	argv := []string{"-p", prompt}
	if permMode != "" {
		argv = append(argv, "--permission-mode", permMode)
	}
	if allowedTools != "" {
		argv = append(argv, "--allowedTools", allowedTools)
	}
	return claudeBin, argv
}

// fetchIssue shells out to gh to resolve the issue. Goes through the REST
// API (`gh api /repos/.../issues/N`) rather than `gh issue view --json`
func (d *Dispatcher) fetchIssue(ctx context.Context, ref *issueRef) (*ghIssue, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", ref.Owner, ref.Repo, ref.Number)
	raw, err := ghcache.GetJSON(path, func() ([]byte, error) {
		return ghratelimit.Retry(func() ([]byte, error) {
			return d.cfg.Runner.Capture(ctx, "gh", "api", path)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("dispatch: gh api repos/%s/%s/issues/%d: %w", ref.Owner, ref.Repo, ref.Number, err)
	}
	var issue ghIssue
	if err := json.Unmarshal(raw, &issue); err != nil {
		return nil, fmt.Errorf("dispatch: parse gh api issue output: %w", err)
	}
	return &issue, nil
}

// seedPrompt composes the prompt fed to claude -p. repoPath is the
// canonical checkout the worker merges its worktree branch into.
func seedPrompt(ref *issueRef, issue *ghIssue, repoPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", headlessPreamble)
	fmt.Fprintf(&b, "Work on %s issue %s.\n\n", forgeName(ref.Platform), ref)
	fmt.Fprintf(&b, "Title: %s\n", issue.Title)
	fmt.Fprintf(&b, "URL:   %s\n\n", issue.URL)
	fmt.Fprintf(&b, "Issue body:\n\n%s\n\n", strings.TrimSpace(issue.Body))
	fmt.Fprintf(&b, "%s", detachedWorktreeFooter(repoPath, ref.Number, issue.Title))
	return b.String()
}

// forgeName renders the platform tag for prompts. Default is GitHub so
// older un-tagged refs keep their existing prompt.
func forgeName(p Platform) string {
	if p == PlatformForgejo {
		return "Forgejo"
	}
	return "GitHub"
}

// detachedWorktreeFooter renders the git-workflow footer for a detached
// worker: commit to its branch, then merge into main from repoPath.
func detachedWorktreeFooter(repoPath string, number int, title string) string {
	branch := dispatchWorktreeBranch(dispatchWorktreeName(number, title))
	return fmt.Sprintf("Workflow rules (from AGENTS.md):\n"+
		"- You are in a git worktree on branch `%s`, isolated from the canonical checkout so concurrent workers in this repo never share a working tree or index. Commit your work to this branch.\n"+
		"- Run tests, linters, and builds without asking. Fix failures. Never use --no-verify.\n"+
		"- When the work is complete and verified (tests, linters, builds green), land it: run `git -C %s merge %s` then `git -C %s push origin main`. Resolve any merge conflicts yourself. Never force-push.\n"+
		"- If `git push origin main` is rejected as non-fast-forward (a sibling worker pushed first), run `git -C %s pull --rebase origin main`, re-run tests/build, then merge and push again. Repeat until it lands.\n"+
		"- Close the issue with a commit trailer: closes #<N> (or fixes / resolves).\n"+
		"- Leave the worktree directory in place - the next `dispatch` run reaps it once the merge lands.\n",
		branch, repoPath, branch, repoPath, repoPath)
}
