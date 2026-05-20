// Package passthrough is the thin pass-through used to wrap any sub-CLI
// (aws, gh, kubectl, docker, tailscale, plus every package manager) as a
// single `coily <bin> ...` verb. One Command(name, runner, audit) builder
// per binary, no subcommand modeling.
//
// What the wrapper buys:
//
//   - Audit log. Every invocation lands in ~/.coily/audit/*.jsonl with
//     timestamp + argv + cwd + exit code.
//   - Argv validation. Refusal of shell metacharacters in argv blocks the
//     dumbest injection paths. Doesn't stop a malicious tool; does stop
//     the agent from being tricked into "kubectl get pod 'foo; curl bad'".
//   - Symmetric deny coverage. The lockdown deny list blocks raw `aws` /
//     `kubectl` / `pnpm` / `cargo` / etc., so the agent can only reach
//     them via coily.
//
// What it doesn't buy: protection from a malicious binary or package
// installed on the host. The wrapper is an audit + gate, not a sandbox;
// sandboxing belongs in agentcontainers, not here.
//
// Why the per-CLI subcommand trees were ripped (issue #27): the
// generator-driven trees that previously wrapped aws/gh/kubectl earned
// their cost only via per-leaf readonly-vs-mutator gating, which is
// already redundant with the lockdown deny list (Bash(kubectl get:*) allow
// vs Bash(kubectl apply:*) deny fires before coily ever runs). The
// remaining benefits (help mirroring, tab completion below `coily ops aws`)
// were convenience without security value, and the cost was ~80k lines of
// generated Go plus a weekly refresh cadence per upstream. SkipFlagParsing
// + verb.Wrap is the same security boundary in 60 lines.
package passthrough

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/egress"
	"github.com/coilysiren/cli-guard/ghcache"
	"github.com/coilysiren/cli-guard/mcporter"
	"github.com/coilysiren/cli-guard/shell"
	"github.com/coilysiren/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

// ReadCacheClassifier inspects the post-WithArgvRewriter argv and
// returns the gh-api-style path the call would read, a per-call max-age
// cap (see ghcache.MaybeServeMaxAge for semantics), and ok=true; or
// ("", 0, false) to decline. See WithReadCache.
//
// max-age semantics:
//   - max < 0: no per-call cap; serve up to the entry's tier TTL (the
//     pre-cli-guard#77 behavior).
//   - max == 0: always miss; the classifier's path is still used as
//     the store key on a successful subprocess exit, so the next call
//     without --max-age=0 can still serve from cache.
//   - max > 0: serve only if the cached entry is at most max old.
type ReadCacheClassifier func(argv []string) (path string, maxAge time.Duration, ok bool)

// Option configures a pass-through Command. Use the With* helpers below
// rather than setting fields directly.
type Option func(*config)

type config struct {
	skipPolicy     bool
	egressOn       bool
	egressList     []string
	egressMode     egress.Mode
	verbName       string
	scopeArgvHint  func(argv []string) string
	argvRewriter   func(argv []string) []string
	readCache      ReadCacheClassifier
	secretResolver mcporter.SecretResolver
}

// WithSkipPolicy disables the shell-metacharacter check for this binary.
// Use only for tools whose argv goes through execve straight to the
// underlying CLI without ever being handed to a shell. Safe set: gh, aws,
// tailscale, package managers (npm/pnpm/yarn/cargo/uv/pip/...). Unsafe
// set: kubectl, docker, ssh - these have exec-into-shell paths where
// argv content can reach a remote bash -c. The audit log and lockdown
// deny list still cover the safe set; the metacharacter check was only
// ever defending the shell-bound path, and rejecting '>' / backticks /
// '$' in argv breaks legitimate markdown bodies in (e.g.)
// `gh issue create --body`.
func WithSkipPolicy() Option {
	return func(c *config) { c.skipPolicy = true }
}

// WithEgress wires the per-invocation HTTP CONNECT proxy. The child runs
// with HTTPS_PROXY/HTTP_PROXY pointed at 127.0.0.1:NNNNN for the lifetime
// of this invocation; the proxy collects one row per host contacted and
// attaches them to the audit record. In ModeEnforce, hosts not on the
// allowlist are denied with 403 (the underlying tool sees a connection
// failure). In ModeObserve, every host is forwarded and logged; the
// allowlist is ignored. See pkg/egress and issue #35.
func WithEgress(allowlist []string, mode egress.Mode) Option {
	return func(c *config) {
		c.egressOn = true
		c.egressList = allowlist
		c.egressMode = mode
	}
}

// WithVerbName overrides the dotted verb name used for audit logging.
// The user-visible cli command name (and the binary actually executed)
// stays as bin; only the audit verb path changes. Used to namespace
// pass-throughs that are mounted under a parent group, e.g. `coily ops
// aws` logs as verb "ops.aws" while still exec'ing the aws binary.
func WithVerbName(name string) Option {
	return func(c *config) {
		c.verbName = name
	}
}

// WithArgvRewriter installs a hook that may rewrite the argv passed to the
// underlying binary before exec. The original argv is still recorded in the
// audit log (verb.Wrap captures argv before Action runs); only what reaches
// the child process changes. Used by the gh wrapper to rewrite GraphQL-backed
// subcommands like `gh issue create` into REST equivalents via `gh api`, so
// the GraphQL secondary rate limit is dodged on the common write paths.
// Return the same slice unchanged to decline the rewrite.
func WithArgvRewriter(fn func(argv []string) []string) Option {
	return func(c *config) {
		c.argvRewriter = fn
	}
}

// WithReadCache wires the pass-through into ghcache so reads matching
// the supplied classifier are served from cache without exec, and
// successful exec results are stored back. The classifier sees the
// argv after WithArgvRewriter has run, so callers can apply their own
// rewrite (e.g. gh's GraphQL-to-REST translation) before classifying.
//
// On hit: cached bytes are written to base.Stdout and the action
// returns nil with no subprocess and no audit row body. On miss: the
// subprocess runs with stdout tee'd into a capture buffer; on exit 0
// the captured bytes are stored under the classified path with the
// TTL ghcache derives from the path tier. On non-zero exit, nothing
// is cached.
//
// Designed for the `coily ops gh` use case (cli-guard#76): a watch
// loop across many repos collapses to one subprocess + cache hits
// instead of one subprocess per tick per repo.
func WithReadCache(classifier ReadCacheClassifier) Option {
	return func(c *config) {
		c.readCache = classifier
	}
}

// WithSecretResolver installs a mcporter-shaped pre-exec preflight: scan
// the mcporter config file for `${VAR}` references, resolve each via r,
// and inject the resolved values as env vars on the child process only.
// The parent process env is never touched. A resolver error short-circuits
// with a clean message naming the failing variable; no partial-env exec.
// A missing mcporter config scans to an empty name list and the preflight
// is a no-op. Designed for `coily ops mcporter` (coilysiren/coily#269);
// any future consumer that wraps mcporter against any secret backend gets
// the same behavior by passing a different resolver.
func WithSecretResolver(r mcporter.SecretResolver) Option {
	return func(c *config) {
		c.secretResolver = r
	}
}

// WithScopeArgvHint installs a fallback --commit-scope resolver that runs
// only when the operator did not set --commit-scope (or COILY_COMMIT_SCOPE)
// explicitly. The hook receives the verb's argv and returns an absolute
// path or "" to decline. Used by `coily ops gh` to default the audit
// scope to ~/projects/coilysiren/<name> when --repo coilysiren/<name>
// is on the command line, so the agent does not have to also pass
// --commit-scope from outside a git repo.
func WithScopeArgvHint(fn func(argv []string) string) Option {
	return func(c *config) {
		c.scopeArgvHint = fn
	}
}

// Command returns the *cli.Command for `coily <bin>`. Every argument
// after the binary name is forwarded verbatim through the pass-through
// pipeline (argv validation -> audit -> exec). SkipFlagParsing keeps
// urfave/cli from interpreting flags meant for the underlying tool.
func Command(bin string, r *shell.Runner, w *audit.Writer, opts ...Option) *cli.Command {
	cfg := config{}
	for _, opt := range opts {
		opt(&cfg)
	}
	verbName := bin
	if cfg.verbName != "" {
		verbName = cfg.verbName
	}
	spec := verb.Spec{
		Name: verbName,
		ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
			return nil, c.Args().Slice()
		},
		SkipPolicy:          cfg.skipPolicy,
		CommitScopeArgvHint: cfg.scopeArgvHint,
	}
	if cfg.egressOn {
		spec.Action, spec.OnComplete = withEgressAction(bin, r, cfg.egressList, cfg.egressMode, cfg.argvRewriter, cfg.readCache, cfg.secretResolver)
	} else {
		spec.Action, spec.OnComplete = withStderrTail(bin, r, cfg.argvRewriter, cfg.readCache, cfg.secretResolver)
	}
	return &cli.Command{
		Name:            bin,
		Usage:           fmt.Sprintf("Pass-through to %s with argv validation + audit log.", bin),
		SkipFlagParsing: true,
		Action:          verb.Wrap(spec, w),
	}
}

// withStderrTail wraps the standard exec action so the wrapped tool's
// stderr is tee'd into a bounded ring buffer. On non-zero exit the captured
// tail is attached to the audit record via OnComplete. Issue #63: without
// this, kubectl/aws/gh failures in the audit log collapse to bare
// "exit status 1" and the operator/forensic reader can't tell what
// actually went wrong.
func withStderrTail(bin string, base *shell.Runner, rewriter func([]string) []string, readCache ReadCacheClassifier, resolver mcporter.SecretResolver) (cli.ActionFunc, func(*audit.Record)) {
	tail := newTailBuffer(audit.MaxStderrTailBytes)
	action := func(ctx context.Context, c *cli.Command) error {
		argv := c.Args().Slice()
		if rewriter != nil {
			argv = rewriter(argv)
		}
		plan, hit := readCachePlan(base.Stdout, readCache, argv)
		if hit {
			return nil
		}
		shadow := *base
		if err := applySecretResolver(&shadow, resolver); err != nil {
			return err
		}
		// Tee stderr through the tail buffer while still streaming to the
		// caller's terminal. Falls back to just the tail buffer when the
		// runner has no stderr writer (test contexts).
		if base.Stderr != nil {
			shadow.Stderr = io.MultiWriter(base.Stderr, tail)
		} else {
			shadow.Stderr = tail
		}
		plan.installStdoutTee(&shadow)
		err := shadow.Exec(ctx, bin, argv...)
		plan.storeIfSuccess(err)
		return err
	}
	hook := func(rec *audit.Record) {
		if rec.ExitCode == 0 {
			return
		}
		if s := strings.TrimSpace(tail.String()); s != "" {
			rec.StderrTail = s
		}
	}
	return action, hook
}

// withEgressAction wraps the standard exec action to start a per-invocation
// proxy, inject HTTPS_PROXY/HTTP_PROXY into a per-call shadow Runner so
// concurrent commands stay independent, and surface the collected rows
// through OnComplete.
func withEgressAction(bin string, base *shell.Runner, allowlist []string, mode egress.Mode, rewriter func([]string) []string, readCache ReadCacheClassifier, resolver mcporter.SecretResolver) (cli.ActionFunc, func(*audit.Record)) {
	var rows []audit.EgressRow
	tail := newTailBuffer(audit.MaxStderrTailBytes)
	action := func(ctx context.Context, c *cli.Command) error {
		argv := c.Args().Slice()
		if rewriter != nil {
			argv = rewriter(argv)
		}
		// Pre-exec read-cache check. On hit, skip the proxy and the
		// subprocess - the cached body is already correct and the
		// egress allowlist's job is preventing unintended network
		// reach, which a cache hit by definition doesn't make.
		plan, hit := readCachePlan(base.Stdout, readCache, argv)
		if hit {
			return nil
		}
		p := egress.New(allowlist, mode)
		proxyURL, err := p.Start(ctx)
		if err != nil {
			return fmt.Errorf("egress: start proxy: %w", err)
		}
		shadow := *base
		shadow.Env = append([]string(nil), base.Env...)
		shadow.Env = append(shadow.Env,
			"HTTPS_PROXY="+proxyURL,
			"HTTP_PROXY="+proxyURL,
			"https_proxy="+proxyURL,
			"http_proxy="+proxyURL,
		)
		if err := applySecretResolver(&shadow, resolver); err != nil {
			return err
		}
		if base.Stderr != nil {
			shadow.Stderr = io.MultiWriter(base.Stderr, tail)
		} else {
			shadow.Stderr = tail
		}
		plan.installStdoutTee(&shadow)
		execErr := shadow.Exec(ctx, bin, argv...)
		rows = p.Stop()
		plan.storeIfSuccess(execErr)
		return execErr
	}
	hook := func(rec *audit.Record) {
		if len(rows) > 0 {
			rec.Egress = rows
		}
		if rec.ExitCode != 0 {
			if s := strings.TrimSpace(tail.String()); s != "" {
				rec.StderrTail = s
			}
		}
	}
	return action, hook
}

// applySecretResolver runs the mcporter preflight: scan the mcporter
// config for `${VAR}` references, resolve each via r, and merge the
// resolved values into shadow.Env. shadow.Env is always cloned before
// append so base.Env's backing array is never mutated (the egress path
// already clones too; double-cloning is cheap and keeps callers from
// having to track which path they're in).
//
// No-op when r is nil or the config has no references.
func applySecretResolver(shadow *shell.Runner, r mcporter.SecretResolver) error {
	if r == nil {
		return nil
	}
	path, err := mcporter.ConfigPath()
	if err != nil {
		return err
	}
	names, err := mcporter.ScanConfig(path)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	resolved, err := mcporter.ResolveAll(r, names)
	if err != nil {
		return err
	}
	cloned := append([]string(nil), shadow.Env...)
	for k, v := range resolved {
		cloned = append(cloned, k+"="+v)
	}
	shadow.Env = cloned
	return nil
}

// tailBuffer is a fixed-size last-N-bytes ring. Writes never block and never
// error. After N total bytes, older bytes are dropped to keep the buffer
// bounded. String() returns the current contents in write order.
type tailBuffer struct {
	cap int
	buf []byte
}

func newTailBuffer(capBytes int) *tailBuffer {
	return &tailBuffer{cap: capBytes}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if t.cap <= 0 {
		return n, nil
	}
	if len(p) >= t.cap {
		t.buf = append(t.buf[:0], p[len(p)-t.cap:]...)
		return n, nil
	}
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.cap; over > 0 {
		t.buf = t.buf[over:]
	}
	return n, nil
}

func (t *tailBuffer) String() string {
	return string(t.buf)
}

// readCachePlan is the per-invocation state produced by inspecting argv
// against a ReadCacheClassifier. On hit, cached bytes are already
// written to baseStdout and the caller should return nil. On miss with
// classifier ok, plan.path is set and plan.capture is the tee target;
// callers should installStdoutTee(shadow) before Exec and call
// storeIfSuccess(err) after. With no classifier or a declined argv,
// installStdoutTee and storeIfSuccess are no-ops.
type readCachePlanState struct {
	path    string
	capture *bytes.Buffer
}

// readCachePlan runs the classifier (if any) and acts on the result.
// Hit: writes cached bytes to baseStdout, returns (_, true).
// Miss with classifier ok: returns a plan with capture allocated.
// Classifier declines or nil: returns a zero plan.
func readCachePlan(baseStdout io.Writer, classifier ReadCacheClassifier, argv []string) (readCachePlanState, bool) {
	if classifier == nil {
		return readCachePlanState{}, false
	}
	path, maxAge, ok := classifier(argv)
	if !ok {
		return readCachePlanState{}, false
	}
	if data, hit := ghcache.MaybeServeMaxAge(path, maxAge); hit {
		if baseStdout != nil {
			_, _ = baseStdout.Write(data)
		}
		return readCachePlanState{}, true
	}
	return readCachePlanState{path: path, capture: &bytes.Buffer{}}, false
}

// installStdoutTee wires the plan's capture buffer into shadow.Stdout
// so subprocess stdout is mirrored to both the operator's terminal
// and the capture buffer. No-op if the plan has no capture (no
// classifier or classifier declined).
func (p readCachePlanState) installStdoutTee(shadow *shell.Runner) {
	if p.capture == nil {
		return
	}
	if shadow.Stdout != nil {
		shadow.Stdout = io.MultiWriter(shadow.Stdout, p.capture)
	} else {
		shadow.Stdout = p.capture
	}
}

// storeIfSuccess writes the captured bytes back to ghcache iff the
// subprocess exited successfully. A non-zero exit leaves the cache
// untouched - we never cache an error body. No-op if the plan has no
// capture.
func (p readCachePlanState) storeIfSuccess(execErr error) {
	if p.capture == nil || execErr != nil {
		return
	}
	_ = ghcache.Store(p.path, p.capture.Bytes())
}
