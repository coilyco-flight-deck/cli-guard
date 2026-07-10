// Package passthrough is the thin pass-through used to wrap any sub-CLI
// (aws, gh, kubectl, docker, tailscale, plus every package manager) as a
package passthrough

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/egress"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/ghcache"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/audit"
	"github.com/urfave/cli/v3"
)

// ReadCacheClassifier inspects the post-WithArgvRewriter argv and
// returns the gh-api-style path the call would read, a per-call max-age
type ReadCacheClassifier func(argv []string) (path string, maxAge time.Duration, ok bool)

// Option configures a pass-through Command. Use the With* helpers below
// rather than setting fields directly.
type Option func(*config)

type config struct {
	skipPolicy   bool
	egressOn     bool
	egressList   []string
	egressMode   egress.Mode
	verbName     string
	argvRewriter func(argv []string) []string
	readCache    ReadCacheClassifier
	envFunc      func() (map[string]string, error)
}

// WithSkipPolicy disables the shell-metacharacter check for this binary.
// Use only for tools whose argv goes through execve straight to the
func WithSkipPolicy() Option {
	return func(c *config) { c.skipPolicy = true }
}

// WithEgress wires the per-invocation HTTP CONNECT proxy. The child runs
// with HTTPS_PROXY/HTTP_PROXY pointed at 127.0.0.1:NNNNN for the lifetime
func WithEgress(allowlist []string, mode egress.Mode) Option {
	return func(c *config) {
		c.egressOn = true
		c.egressList = allowlist
		c.egressMode = mode
	}
}

// WithVerbName overrides the dotted verb name used for audit logging.
// The user-visible cli command name (and the binary actually executed)
func WithVerbName(name string) Option {
	return func(c *config) {
		c.verbName = name
	}
}

// WithArgvRewriter installs a hook that may rewrite the argv passed to the
// underlying binary before exec. The original argv is still recorded in the
func WithArgvRewriter(fn func(argv []string) []string) Option {
	return func(c *config) {
		c.argvRewriter = fn
	}
}

// WithReadCache wires the pass-through into ghcache so reads matching
// the supplied classifier are served from cache without exec, and
func WithReadCache(classifier ReadCacheClassifier) Option {
	return func(c *config) {
		c.readCache = classifier
	}
}

// WithEnvFunc injects extra env vars into the wrapped binary at exec time;
// returned keys win over the parent env, resolved once per run just before exec.
func WithEnvFunc(fn func() (map[string]string, error)) Option {
	return func(c *config) {
		c.envFunc = fn
	}
}

// Command returns the *cli.Command for the wrapped `<bin>`. Every argument
// after the binary name is forwarded verbatim through the pass-through
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
		SkipPolicy: cfg.skipPolicy,
	}
	if cfg.egressOn {
		spec.Action, spec.OnComplete = withEgressAction(bin, r, cfg.egressList, cfg.egressMode, cfg.argvRewriter, cfg.readCache, cfg.envFunc)
	} else {
		spec.Action, spec.OnComplete = withStderrTail(bin, r, cfg.argvRewriter, cfg.readCache, cfg.envFunc)
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
func withStderrTail(bin string, base *shell.Runner, rewriter func([]string) []string, readCache ReadCacheClassifier, envFunc func() (map[string]string, error)) (cli.ActionFunc, func(*audit.Record)) {
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
		if err := applyEnvFunc(&shadow, envFunc); err != nil {
			return err
		}
		// Tee stderr through the tail buffer while still streaming to the
		// caller's terminal. Falls back to just the tail buffer when the
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
func withEgressAction(bin string, base *shell.Runner, allowlist []string, mode egress.Mode, rewriter func([]string) []string, readCache ReadCacheClassifier, envFunc func() (map[string]string, error)) (cli.ActionFunc, func(*audit.Record)) {
	var rows []audit.EgressRow
	tail := newTailBuffer(audit.MaxStderrTailBytes)
	action := func(ctx context.Context, c *cli.Command) error {
		argv := c.Args().Slice()
		if rewriter != nil {
			argv = rewriter(argv)
		}
		// Pre-exec read-cache check. On hit, skip the proxy and the
		// subprocess - the cached body is already correct and the
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
		shadow.Env = appendLoopbackNoProxy(shadow.Env)
		if err := applyEnvFunc(&shadow, envFunc); err != nil {
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

// applyEnvFunc merges the exec-time hook's result into a clone of the shadow
// env; the clone stops injected secrets leaking into later shared-base invocations.
func applyEnvFunc(shadow *shell.Runner, f func() (map[string]string, error)) error {
	if f == nil {
		return nil
	}
	extra, err := f()
	if err != nil {
		return err
	}
	if len(extra) == 0 {
		return nil
	}
	cloned := append([]string(nil), shadow.Env...)
	for k, v := range extra {
		cloned = append(cloned, k+"="+v)
	}
	shadow.Env = cloned
	return nil
}

func appendLoopbackNoProxy(env []string) []string {
	merged := mergeNoProxyValues(effectiveEnvValue("NO_PROXY", env), effectiveEnvValue("no_proxy", env))
	merged = mergeNoProxyValues(merged, "127.0.0.1,::1,localhost")
	env = upsertEnv(env, "NO_PROXY", merged)
	env = upsertEnv(env, "no_proxy", merged)
	return env
}

func effectiveEnvValue(key string, env []string) string {
	if v, ok := envLookup(env, key); ok {
		return v
	}
	return os.Getenv(key)
}

func envLookup(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && k == key {
			return v, true
		}
	}
	return "", false
}

func upsertEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		k, _, ok := strings.Cut(entry, "=")
		if ok && k == key {
			continue
		}
		out = append(out, entry)
	}
	return append(out, key+"="+value)
}

func mergeNoProxyValues(values ...string) string {
	var merged []string
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			token := strings.TrimSpace(part)
			if token == "" {
				continue
			}
			key := strings.ToLower(token)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, token)
		}
	}
	return strings.Join(merged, ",")
}

// tailBuffer is a fixed-size last-N-bytes ring. Writes never block and never
// error. After N total bytes, older bytes are dropped to keep the buffer
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
type readCachePlanState struct {
	path    string
	capture *bytes.Buffer
}

// readCachePlan runs the classifier (if any) and acts on the result.
// Hit: writes cached bytes to baseStdout, returns (_, true).
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
func (p readCachePlanState) storeIfSuccess(execErr error) {
	if p.capture == nil || execErr != nil {
		return
	}
	_ = ghcache.Store(p.path, p.capture.Bytes())
}
