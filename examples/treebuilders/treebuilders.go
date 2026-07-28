// Package treebuilders exports each examples/<name>/main.go's *cli.Command
// tree so scripts/gen-webdocs can render it, and so each example main
package treebuilders

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/gittree"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/passthrough"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/egress"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/policy"
	"github.com/urfave/cli/v3"
)

// Audit is the tree for examples/audit. writer may be nil for doc
// rendering since Actions are not executed.
func Audit(writer *audit.Writer) *cli.Command {
	return &cli.Command{
		Name:    "demo",
		Usage:   "tiny cli-guard demo app",
		Version: "v0.0.0",
		Description: `demo is the smallest end-to-end exercise of the cli-guard pipeline:

    1. The wrapped Action runs through policy.ValidateArg over every
       user-supplied string before execve.
    2. An append-only JSONL row lands in $TMPDIR/cli-guard-demo.jsonl
       with timestamp, argv, cwd, exit code, and the forensic RepoRoot
       (git toplevel of cwd, or empty outside any repo).

Why an append-only audit log: it is the forensic trail if an agent
(or a confused human) invokes something destructive. The log lives
outside the working tree, is written 0600 in a 0700 dir, and rotates
via lumberjack when the active file hits the size cap. Old backups
past the retention horizon are pruned. There is no "skip audit" knob.

Operating model for an agent calling these commands:

    - Failure to preflight the audit dir is a hard fail at startup,
      not per call. If you see "audit preflight: ..." on stderr, do
      not retry; the host is broken (disk full, perms wrong, dir not
      writable). Surface to the operator.
    - Inspect the audit row after the call to reconstruct what
      happened: ` + "`tail -1 \"$TMPDIR/cli-guard-demo.jsonl\" | jq`" + `.`,
		Commands: []*cli.Command{
			{
				Name:      "hello",
				Usage:     "print a greeting",
				ArgsUsage: "<name>",
				Description: `Audited greeting verb. Demonstrates verb.Wrap composing every
cli-guard primitive on a single Action.

Examples:

    # greet
    demo hello world
    # hello, world

    # empty name accepted - defaults to "world"
    demo hello
    # hello, world

    # rejected by policy.ValidateArg (shell metacharacter)
    demo hello 'world; whoami'
    # policy: shell metacharacter rejected: arg positional[0] contains ';' at index 5

The audit row produced on a passing call includes:

    {"ts":"2026-...","verb":"hello","argv":["hello","world"],"cwd":"...","repo_root":"...","exit":0}

On a rejected call the row records exit=2 and the policy reason in
the envelope. An agent reading the log can reconstruct intent without
parsing stderr.`,
				Action: verb.Wrap(verb.Spec{
					Name: "hello",
					ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
						return nil, c.Args().Slice()
					},
					Action: func(_ context.Context, c *cli.Command) error {
						name := c.Args().First()
						if name == "" {
							name = "world"
						}
						fmt.Printf("hello, %s\n", name)
						return nil
					},
				}, writer),
			},
		},
	}
}

func egressDialThrough(proxyAddr, target string) (int, string, error) {
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		return 0, "", err
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	req, err := http.NewRequest("HEAD", target, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Status, nil
}

func egressRun(_ context.Context, target string, mode egress.Mode) error {
	p := egress.New([]string{"example.com"}, mode)
	addr, err := p.Start(context.Background())
	if err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	fmt.Println("proxy listening on", addr)
	code, status, err := egressDialThrough(addr, target)
	if err != nil {
		fmt.Println("dial error:", err)
	} else {
		fmt.Println("response:", code, status)
	}
	rows := p.Stop()
	fmt.Println("egress rows:")
	for _, r := range rows {
		fmt.Printf("  host=%-32s decision=%-7s up=%d down=%d ms=%d\n", r.Host, r.Decision, r.BytesUp, r.BytesDown, r.DurationMS)
	}
	return nil
}

// Egress is the tree for examples/egress.
func Egress() *cli.Command {
	return &cli.Command{
		Name:    "egress-demo",
		Usage:   "show the CONNECT-proxy allowlist gate",
		Version: "v0.0.0",
		Description: `egress-demo exercises the per-invocation HTTP CONNECT proxy that
cli-guard stands up for the duration of a wrapped subprocess. The
child inherits HTTPS_PROXY / HTTP_PROXY pointing at a local proxy on
127.0.0.1:<random-port>. The proxy logs every CONNECT, joins the
rows back to the parent invocation's audit row, and either enforces a
pinned allowlist or observes silently.

Two modes:

    - ModeEnforce: per-binary allowlist pinned in code. Denied
      CONNECTs return HTTP 403 from the proxy. The row is marked
      decision=deny. Used for package-manager wrappers (brew, npm,
      pip, cargo, ...) where the upstream registry set is small,
      stable, and high-value.
    - ModeObserve: no allowlist, every CONNECT is forwarded and
      logged. Used for aws, gh, kubectl, docker, tailscale - tools
      whose upstream surface is too broad to enumerate but whose
      every network reach is still worth auditing.

What this gate is and is not:

    - CONNECT-only. No TLS interception, no CA install, no payload
      inspection. The proxy reads the host:port from the CONNECT
      request line and decides accept/deny on hostname alone.
    - Plaintext HTTP through the proxy is rejected with 405. Any
      tool that depends on plaintext fetching is already a bug, and
      we do not want to be the layer that silently downgrades it.
    - Not a sandbox. A determined hostile process can resolve a host
      directly, bypass the env proxy hint, or use a non-HTTP
      transport. The gate stops the dumb cases (npm post-install
      curls a remote shell), records the rest, and pairs with the
      lockdown deny rules and audit log to make the rest expensive
      to hide.

Operating model for an agent calling these commands:

    - On a 403, the child saw a normal HTTP failure. Do not retry.
      The upstream is unreachable by design; the right escalation is
      "the allowlist does not cover <host>, here is the egress row,
      operator decide". An allowlist edit is a code change, not a
      runtime flag.
    - Egress rows are emitted to stderr at proxy.Stop() time in
      these examples for inspection. In production they fold into
      the parent audit row.`,
		Commands: []*cli.Command{
			{
				Name:  "allowed",
				Usage: "dial a host that is on the allowlist",
				Description: `Dials https://example.com via the local CONNECT proxy with
ModeEnforce and an allowlist of just {"example.com"}. Returns the
HTTP response status from the dial plus the captured egress rows.

Examples:

    egress-demo allowed
    # proxy listening on 127.0.0.1:<port>
    # response: 200 200 OK
    # egress rows:
    #   host=example.com:443                decision=allow  up=... down=... ms=...

The decision=allow row is what an auditor wants to see: the host was
reached, the proxy let it through because it matched the allowlist,
the size+duration of the CONNECT tunnel is recorded. Replays from
this row alone can reconstruct what the child did at the network
layer without needing the child's stdout.`,
				Action: func(ctx context.Context, _ *cli.Command) error {
					return egressRun(ctx, "https://example.com/", egress.ModeEnforce)
				},
			},
			{
				Name:  "denied",
				Usage: "dial a host that is not on the allowlist",
				Description: `Dials https://www.iana.org via the same proxy + allowlist as
` + "`allowed`" + `. The hostname is not on the allowlist, so the proxy refuses
the CONNECT with 403 and records decision=deny.

Examples:

    egress-demo denied
    # proxy listening on 127.0.0.1:<port>
    # response: 403 403 Forbidden
    # egress rows:
    #   host=www.iana.org:443               decision=deny   up=0 down=0 ms=...

What the child sees: a normal-looking HTTP 403 from its proxy. From
the child's perspective the upstream simply did not respond. There
is no special signal that cli-guard caused the failure, and that is
intentional: we do not want to teach hostile code to fingerprint the
gate and route around it. The forensic trail lives in the audit row,
not in the child's error message.

Agent behavior on a deny: surface the egress row to the operator,
propose an allowlist update if the host is legitimate, do NOT try to
work around the proxy by setting HTTPS_PROXY="" or dialing the
address directly. Bypassing the gate is what the gate is there to
catch.`,
				Action: func(ctx context.Context, _ *cli.Command) error {
					return egressRun(ctx, "https://www.iana.org/", egress.ModeEnforce)
				},
			},
			{
				Name:  "observe",
				Usage: "log every host without enforcing",
				Description: `Dials https://www.iana.org via the proxy with ModeObserve. No
allowlist is consulted, every CONNECT is forwarded, every CONNECT is
recorded.

Examples:

    egress-demo observe
    # proxy listening on 127.0.0.1:<port>
    # response: 200 200 OK
    # egress rows:
    #   host=www.iana.org:443               decision=observe up=... down=... ms=...

When to reach for ModeObserve vs ModeEnforce: enforce when the set
of legitimate hosts is small, stable, and explicitly enumerable
(package registries). Observe when the set is too broad to pin
(aws.amazon.com fans out into thousands of regional endpoints) but
where after-the-fact "what did the agent talk to" is the actually-
load-bearing telemetry.

Operating model: a row with decision=observe is not an alert; it is
expected. Anomaly detection on the observed-row stream is a
downstream concern, not a consumer-internal one. Tools that aggregate
these rows belong in the telemetry repo, not in cli-guard.`,
				Action: func(ctx context.Context, _ *cli.Command) error {
					return egressRun(ctx, "https://www.iana.org/", egress.ModeObserve)
				},
			},
		},
	}
}

// Exitcode is the tree for examples/exitcode.
func Exitcode() *cli.Command {
	return &cli.Command{
		Name:    "exitcode-demo",
		Usage:   "show the public exit-code taxonomy",
		Version: "v0.0.0",
		Description: `exitcode-demo exercises the public exit-code taxonomy. External
consumers (orchestrators, CI steps, watchdogs, retry loops) pattern-
match on the process exit code to decide retry vs. abort vs. handoff
without parsing stderr. The codes are a stable contract.

The taxonomy:

    0 - Success      verb ran, underlying tool returned without error
    1 - Generic      catch-all, prefer a typed code over this
    2 - PolicyDenied the security pre-flight rejected the call (metachar,
                     missing required arg, deny rule hit). The
                     underlying tool was never invoked.
    3 - UpstreamFailed the wrapped tool ran and returned non-zero.
                     Stdout/stderr from the tool flow through; the
                     envelope's message is the wrapping error.
    4 - Internal     consumer-internal failure (config load, manifest
                     miss, audit-write fail). Distinct from
                     PolicyDenied because the user cannot fix it;
                     this is a consumer bug or a host problem.
    5 - UserError    obviously-wrong input that wasn't a metachar
                     rejection (missing flag, wrong arg count).
                     Distinct from PolicyDenied so a consumer can
                     differentiate "you typed it wrong" from "policy
                     says no".

Operating model for an agent consuming an exit code:

    - 2 (PolicyDenied) is non-retryable. The argv is hostile or
      malformed in a way that the gate refuses to forward. Surface
      to operator; do not escape and retry.
    - 3 (UpstreamFailed) is potentially retryable but only with the
      same argv and only if the underlying tool's exit suggests
      transience (network blip, lock contention). The wrapper does
      not retry for you. Read stderr to decide.
    - 4 (Internal) is non-retryable on this host. Report a bug, try
      a different host, or wait for a fix.
    - 5 (UserError) is non-retryable from automation; the operator
      needs to correct the input.

Add a new code only when an external consumer can act differently on
it. Do not subdivide for taxonomy; a single rejection class with a
yaml error envelope is more useful than a fan-out of codes.`,
		Commands: []*cli.Command{
			{
				Name:  "success",
				Usage: "exit 0",
				Description: `Returns nil. Process exits 0.

Examples:

    exitcode-demo success ; echo "exit: $?"
    # exit: 0`,
				Action: func(_ context.Context, _ *cli.Command) error { return nil },
			},
			{
				Name:  "policy",
				Usage: "exit 2 (PolicyDenied)",
				Description: `Returns exitcode.New(PolicyDenied, ...). Process exits 2. The yaml
envelope on stderr is the structured form an orchestrator parses.

Examples:

    exitcode-demo policy ; echo "exit: $?"
    # error:
    #     kind: policy
    #     message: argv rejected
    #     hint: fix the input
    # exit: 2

Treat exit 2 as authoritative-non-retryable.`,
				Action: func(_ context.Context, _ *cli.Command) error {
					return exitcode.New(exitcode.PolicyDenied, "policy", errors.New("argv rejected"), "fix the input")
				},
			},
			{
				Name:  "upstream",
				Usage: "exit 3 (UpstreamFailed)",
				Description: `Returns exitcode.New(UpstreamFailed, ...). Process exits 3. Use this
when the wrapped tool ran and returned non-zero; the wrapper passes
the failure up but tags it as "from upstream" so the operator can
tell "the tool said no" apart from "the consumer said no".

Examples:

    exitcode-demo upstream ; echo "exit: $?"
    # error:
    #     kind: upstream_failed
    #     message: wrapped tool exited 7
    #     hint: check the tool
    # exit: 3

Retry semantics: read the underlying tool's stderr. The wrapper does
not retry for you.`,
				Action: func(_ context.Context, _ *cli.Command) error {
					return exitcode.New(exitcode.UpstreamFailed, "upstream_failed", errors.New("wrapped tool exited 7"), "check the tool")
				},
			},
			{
				Name:  "internal",
				Usage: "exit 4 (Internal)",
				Description: `Returns exitcode.New(Internal, ...). Process exits 4. Use this for
consumer-internal failures the user cannot fix from the call site:
config load failure, manifest miss, audit-write fail.

Examples:

    exitcode-demo internal ; echo "exit: $?"
    # error:
    #     kind: internal
    #     message: config load failed
    #     hint: report a bug
    # exit: 4

Treat exit 4 as host-broken-or-bug; escalate to operator, do not
retry on this host.`,
				Action: func(_ context.Context, _ *cli.Command) error {
					return exitcode.New(exitcode.Internal, "internal", errors.New("config load failed"), "report a bug")
				},
			},
		},
	}
}

// Gittree is the tree for examples/gittree.
func Gittree() *cli.Command {
	return &cli.Command{
		Name:    "gittree-demo",
		Usage:   "show the clean+synced gate",
		Version: "v0.0.0",
		Description: `gittree-demo exercises the clean+synced gate that fronts every
.ward/ward.yaml repo verb. The gate refuses a repo-verb invocation
whenever the working tree could not be reconstructed from git history
alone:

    - uncommitted changes (modified files)
    - untracked files (new files not in .gitignore)
    - detached HEAD (no branch context for the commit)
    - a branch without a configured upstream
    - upstream that has not been fetched recently

Why: every repo-verb audit row points at a commit. If the tree was
dirty at the time of invocation, the audit row alone cannot tell you
what code ran. Refusing the verb keeps the audit log forensically
reconstructable from git history later.

Scope of the gate:

    - Repo verbs only. Built-in consumer verbs (lockdown, setup, audit
      inspection) are reproducible from the binary version trailer
      in the audit row and are not gated.
    - The gate is run per call. A repo can be clean at call N and
      dirty at call N+1.
    - There is no global "skip" knob. --audit-override-dirty exists
      for genuine emergencies; it tags the audit row with
      audit_override=true and captures the working tree status so
      the run can still be reconstructed after the fact.

Operating model for an agent calling repo verbs:

    - On refusal, the State.Recovery field names the dictatable shell
      command the operator should run (git status, git commit, git
      push, edit .gitignore). The agent should surface that recovery
      hint verbatim, not invent its own.
    - Do NOT auto-stash or auto-commit to clear the gate. That
      destroys the very signal the gate was protecting (what was the
      working tree at audit time).
    - The gate runs git plumbing under the hood. If git itself is
      broken (not installed, .git corrupted), the gate fails with a
      structured error rather than silently passing.`,
		Commands: []*cli.Command{
			{
				Name:  "build",
				Usage: "pretend-build verb, gated on a clean tree",
				Description: `Pretend-build verb. Calls gittree.CheckClean on cwd; if the tree is
clean, prints "ok: tree is clean, pretend-build runs". If not, returns
the structured refusal so the operator can read which gate property
failed.

Examples:

    # inside a clean checkout
    cd /path/to/clean/repo
    gittree-demo build
    # ok: tree is clean, pretend-build runs

    # dirty checkout - uncommitted change
    echo dirt > some-tracked-file
    gittree-demo build
    # refused: working tree is dirty
    #   M some-tracked-file

    # untracked file
    touch new-file
    gittree-demo build
    # refused: untracked files present
    #   ?? new-file

    # detached HEAD
    git checkout HEAD~5
    gittree-demo build
    # refused: HEAD is detached

What the audit row records on refusal: the verb name (build), the
exit code (2 = PolicyDenied), and the gate property that failed. An
auditor can read this and reproduce the host state at refusal time
without needing the operator's terminal scrollback.`,
				Action: func(_ context.Context, _ *cli.Command) error {
					cwd, _ := os.Getwd()
					state, err := gittree.CheckClean(cwd)
					if err != nil {
						return fmt.Errorf("refused: %w", err)
					}
					_ = state
					fmt.Println("ok: tree is clean, pretend-build runs")
					return nil
				},
			},
		},
	}
}

// Passthrough is the tree for examples/passthrough. writer/runner may be
// nil for doc rendering. When nil, a placeholder echo command is
func Passthrough(runner *shell.Runner, writer *audit.Writer) *cli.Command {
	var echoCmd *cli.Command
	if runner != nil && writer != nil {
		echoCmd = passthrough.Command("echo", runner, writer)
	} else {
		echoCmd = &cli.Command{
			Name:  "echo",
			Usage: "audited passthrough wrapper around /bin/echo",
		}
	}
	echoCmd.Description = `Audited pass-through wrapper around /bin/echo. Any argv after ` + "`echo`" + `
is forwarded to the binary after policy.ValidateArg rejection of
shell metacharacters. Every call lands a JSONL row in the audit log
with timestamp, full argv, cwd, exit code.

Examples:

    # forward to /bin/echo
    passthrough-demo -- echo hello world
    # hello world
    # audit log: $TMPDIR/cli-guard-passthrough.jsonl

    # rejected by policy
    passthrough-demo -- echo 'hello; rm -rf /'
    # rejected by policy: shell metacharacter in argv

    # SkipFlagParsing means flags after the binary name go straight
    # through. -n is a real /bin/echo flag, not a consumer flag.
    passthrough-demo -- echo -n no-newline

What the wrapper does NOT do: validate semantics. ` + "`-- echo --version`" + `
runs ` + "`echo --version`" + `, which prints "--version" because /bin/echo
ignores unknown flags. The consumer does not parse the wrapped tool's flag
syntax. Choosing safe binaries is part of the integration design.

Agent behavior: surface rejection errors verbatim; do not retry with
quoted/escaped variants. If a binary's legitimate use requires shell
metacharacters in argv (rare), the right answer is a consumer-side
wrapper script that pre-tokenizes the input, not a runtime escape.`
	return &cli.Command{
		Name:    "passthrough-demo",
		Usage:   "wrap an existing binary as an audited subcommand",
		Version: "v0.0.0",
		Description: `passthrough-demo exercises cli-guard's thin pass-through. The pattern
wraps any external CLI (aws, gh, kubectl, docker, tailscale, plus
every package manager) as a single consumer verb with two properties:

    1. Audit. Every invocation produces a JSONL row in
       ~/.ward/audit/*.jsonl with timestamp, full argv, cwd, exit
       code. Reconstructable from the row alone.
    2. Argv validation. policy.ValidateArg rejects shell metacharacters
       before execve. Blocks the dumbest injection paths (` + "`passthrough-demo kubectl" + `
       ` + "get pod 'foo; curl bad'`" + `).

What the wrapper does NOT buy:

    - Not a sandbox. A malicious binary or post-install hook
      installed on the host runs with the user's privileges. The
      wrapper is audit + gate, not isolation. Sandboxing belongs in
      agentcontainers, not here.
    - Not deep verb modeling. The pass-through has no idea what
      ` + "`kubectl apply`" + ` does differently from ` + "`kubectl get`" + `. Per-leaf
      readonly-vs-mutator gating is delegated to the lockdown deny
      list, which fires before the consumer ever runs.

Why per-CLI subcommand trees were ripped (issue #27): generator-
driven trees that wrapped aws/gh/kubectl earned their cost only via
the readonly-vs-mutator gate already handled by the lockdown deny
list. The remaining benefits (help mirroring, tab completion below
` + "`passthrough-demo aws`" + `) were convenience without security value, and the
cost was ~80k lines of generated Go plus a weekly refresh cadence
per upstream. SkipFlagParsing + verb.Wrap is the same security
boundary in 60 lines.

Operating model for an agent calling pass-through verbs:

    - The wrapper does not pre-validate the wrapped tool's flag
      syntax. ` + "`passthrough-demo aws --unknown-flag`" + ` reaches aws as
      ` + "`aws --unknown-flag`" + `, and aws decides what to do with it.
    - On policy rejection (exit 2), the argv never reached the
      binary. Do not retry; the input contained a forbidden byte.
    - On upstream failure (exit 3), the binary ran. Stderr from the
      binary is the authoritative reason; the consumer's wrapping error is
      just the envelope.`,
		Commands: []*cli.Command{echoCmd},
	}
}

// Policy is the tree for examples/policy.
func Policy() *cli.Command {
	return &cli.Command{
		Name:    "policy-demo",
		Usage:   "show shell-metacharacter argv rejection",
		Version: "v0.0.0",
		Description: `policy-demo exercises cli-guard's argv pre-validation gate. Every call
into a wrapped Action passes through policy.ValidateArg before execve.
The gate rejects any string containing one of these bytes:

    ` + "`" + `  $  ;  &  |  <  >  (  )  {  }  \  \n  \r  \t

Why this exists, even though cli-guard always builds an explicit argv
slice and never invokes /bin/sh: a non-trivial fraction of downstream
tools hand their last positional argument to a remote shell. Examples:

    ssh user@host '<remote-command>'
    kubectl exec pod -- sh -c '<command>'
    git config --global core.editor '<editor-cmd>'

If the agent driving cli-guard never sanitizes inputs and the wrapped
tool unsplats argv into a shell on the other side, a single semicolon
in an argument turns a benign verb into a chained injection. Rejecting
the metacharacters at the consumer boundary keeps that one-layer leak
from becoming an execution surprise two hops downstream.

Operating model for an agent calling these commands:

    - Any rejected input fails the verb deterministically with
      exitcode.PolicyDenied (2). The argv never reaches execve.
    - The error format is stable and parseable:
        policy: shell metacharacter rejected: arg <name> contains '<char>' at index <i>
    - On rejection, DO NOT retry with a quoted or escaped variant. The
      input is hostile by definition. Surface the rejection to the
      operator.
    - The gate is content-only. It does not check semantics. "rm -rf /"
      with no metacharacters passes the gate; whether the wrapped tool
      should run it is a separate verb-allowlist decision.

The two leaves below share an identical Action body. The only
difference is the input the operator types. That is the point: the
gate is the gate, not the leaf.`,
		Commands: []*cli.Command{
			{
				Name:      "safe",
				Usage:     "validates a single positional arg",
				ArgsUsage: "<value>",
				Description: `Validates a single positional argument against the ShellMeta byte set.
Prints the accepted value on success. Returns ErrShellMeta on
rejection (exit 2).

Examples:

    # accepted - no metacharacters
    policy-demo safe hello
    # accepted: [hello]

    # accepted - whitespace inside a single arg is fine, only \t \n \r reject
    policy-demo safe "hello world"
    # accepted: [hello world]

    # rejected - same code path as ` + "`unsafe`" + `, demonstrating the gate
    # is content-driven, not name-driven
    policy-demo safe 'hello; rm -rf /'
    # policy: shell metacharacter rejected: arg positional[0] contains ';' at index 5

The empty string passes the gate. Required-ness is a separate concern;
check len(args) before calling ValidateArgSlice if the verb needs at
least one value.`,
				Action: func(_ context.Context, c *cli.Command) error {
					vals := c.Args().Slice()
					if err := policy.ValidateArgSlice("positional", vals); err != nil {
						return err
					}
					fmt.Printf("accepted: %v\n", vals)
					return nil
				},
			},
			{
				Name:      "unsafe",
				Usage:     "demonstrate the rejection path",
				ArgsUsage: "<value-with-shell-metachar>",
				Description: `Identical Action body to ` + "`safe`" + `. Named "unsafe" only because the docs
expect the operator to feed it a metacharacter-bearing input. Useful
for an agent verifying the gate is wired before trusting the rest of
cli-guard's audit chain.

Examples:

    # the canonical rejection
    policy-demo unsafe 'foo; rm -rf /'
    # rejected: policy: shell metacharacter rejected: arg positional[0] contains ';' at index 3

    # backticks - command substitution attempt
    policy-demo unsafe '` + "`whoami`" + `'
    # rejected: policy: shell metacharacter rejected: arg positional[0] contains '` + "`" + `' at index 0

    # newline - multi-line injection attempt
    printf 'foo\nbar' | xargs -0 policy-demo unsafe
    # rejected: policy: shell metacharacter rejected: arg positional[0] contains '\n' at index 3

    # a clean input still passes here - the name is operator-facing,
    # the gate behavior is not
    policy-demo unsafe hello
    # accepted: [hello]

Agent behavior on rejection: parse the error string, report to the
operator, do not attempt to bypass. ShellMeta is a fixed list and is
not configurable per call. If a verb genuinely needs one of these
bytes in its input (rare; usually a sign the verb should be split
into pre-tokenized arguments instead), the right move is a code
change to that verb's ArgsFunc, not a runtime escape.`,
				Action: func(_ context.Context, c *cli.Command) error {
					vals := c.Args().Slice()
					if err := policy.ValidateArgSlice("positional", vals); err != nil {
						return err
					}
					fmt.Printf("accepted (unexpected): %v\n", vals)
					return nil
				},
			},
		},
	}
}

// Repocfg is the tree for examples/repocfg. cfg may be nil for doc
// rendering since Actions are not executed.
func Repocfg(cfg *repocfg.Config) *cli.Command {
	return &cli.Command{
		Name:    "repocfg-demo",
		Usage:   "show per-repo command allowlist loading",
		Version: "v0.0.0",
		Description: `repocfg-demo exercises the per-repo command allowlist. cli-guard
walks up from cwd looking for a .ward/ward.yaml that declares
named verbs (build, test, lint, deploy, ...). Each verb is a fixed
argv. Each argv token is policy.ValidateArg'd at load time, so the
exposed surface at runtime can never be a shell pipeline or contain
shell metacharacters.

Why a per-repo file vs embedding the verbs in the consumer itself:

    - Repo-specific dev tools change too often to warrant a
      rebuild+install cycle.
    - The file is checked into git where a human reviews diffs.
      Adding a verb is a PR-visible event.
    - The blast radius is bounded by the same policy.ValidateArg
      gate as every other consumer verb. There is no "it's just a dev
      script" carve-out.

Threat-model corner cases the gate addresses:

    - A repo-yaml verb that names a shell pipeline as its argv:
      rejected at load time, the verb never becomes invocable.
    - A repo-yaml verb that names a fixed argv but at runtime the
      user appends a metacharacter-bearing extra arg: rejected at
      call time by the same gate.
    - A repo-yaml verb in a repo that is not actually checked out
      (downloaded zipball, malicious upstream): the verb loads, but
      every audit row binds to the resolved git toplevel, so an
      audit reader sees the call did not come from a real commit.

Operating model for an agent calling repo verbs:

    - The discovery walk stops at the first .ward/ward.yaml. If a
      sub-repo wants different verbs, the file in the sub-repo
      shadows the parent's.
    - There is no merge / override semantics. The closest yaml
      wins; the rest is ignored.
    - Verb arg appending: ` + "`repocfg-demo run test -v ./pkg/...`" + ` appends ` + "`-v" + `
      ` + "./pkg/...`" + ` to the yaml-declared argv, after the same gate.`,
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "print the verbs declared in ward.yaml",
				Description: `Prints every verb declared in the discovered ward.yaml as
"<name>: <argv-joined-by-space>". Read-only; does not execute any
verb.

Examples:

    # in a repo with build + greet verbs declared
    cd examples/repocfg
    repocfg-demo list
    # build: go build ./...
    # greet: echo hello world

    # no ward.yaml found anywhere in the cwd ancestry
    cd /tmp
    repocfg-demo list
    # error: no .ward/ward.yaml found in cwd ancestry
    # exit: 1

Use ` + "`list`" + ` to inventory the verb surface before calling ` + "`run`" + `. An
agent that discovers a verb here can then invoke it; an agent that
does not see the verb listed should not try to invoke it under a
different spelling.`,
				Action: func(_ context.Context, _ *cli.Command) error {
					if cfg == nil {
						return errors.New("no config loaded")
					}
					for _, v := range cfg.Commands {
						fmt.Printf("%s: %s\n", v.Name, strings.Join(v.Argv, " "))
					}
					return nil
				},
			},
			{
				Name:      "run",
				Usage:     "execute a declared verb by name",
				ArgsUsage: "<verb>",
				Description: `Executes one verb by name from the discovered ward.yaml. The
verb's argv was policy.ValidateArg'd at load time, so the tokens
themselves cannot contain shell metacharacters. The wrapped exec is
non-shell (` + "exec.Command, not /bin/sh -c)" + `, so the argv tokens are
forwarded literally.

Examples:

    # invoke a declared verb
    cd examples/repocfg
    repocfg-demo run greet
    # hello world

    # invoke a verb that does not exist
    repocfg-demo run nonexistent
    # error: no such verb: nonexistent
    # exit: 1

    # required arg missing
    repocfg-demo run
    # error: usage: run <verb>
    # exit: 1

What ` + "`run`" + ` does NOT do:

    - It does not parse the wrapped tool's flag syntax. The argv is
      taken from ward.yaml as-is.
    - It does not let you mutate the verb at the call site. To
      change what ` + "`go build ./...`" + ` does, edit ward.yaml and commit.
      That diff is the audit trail.
    - It does not bypass policy validation. The yaml-loaded argv
      was already validated; any user-appended extras pass through
      ValidateArg too.

Agent behavior on "no such verb": surface to operator with the
output of ` + "`list`" + `. Do not guess at alternative spellings; the verb
catalog is the catalog.`,
				Action: func(ctx context.Context, c *cli.Command) error {
					if cfg == nil {
						return errors.New("no config loaded")
					}
					name := c.Args().First()
					if name == "" {
						return errors.New("usage: run <verb>")
					}
					for _, v := range cfg.Commands {
						if v.Name == name {
							cmd := exec.CommandContext(ctx, v.Argv[0], v.Argv[1:]...) //nolint:gosec // tokens validated at load time
							cmd.Stdout = os.Stdout
							cmd.Stderr = os.Stderr
							return cmd.Run()
						}
					}
					return fmt.Errorf("no such verb: %s", name)
				},
			},
		},
	}
}
