# cli-guard features

Inventory of what cli-guard does today. Scope changes should land in the same commit that touches code, so this file stays a faithful mirror of the public API. Pair with `examples/<feature>/` to see each primitive end-to-end.

## Framework primitives

Per-primitive detail in [features-detail.md](features-detail.md).

- **audit** - Append-only JSONL invocation log with lumberjack rotation.
- **policy** - Argv validation rejecting shell metacharacters.
- **hook** - Shared Claude Code PreToolUse engine.
- **verb** - Middleware around every `*cli.Command.Action`.
- **scope** - Resolve `--commit-scope=auto` to a git toplevel.
- **exitcode** - Public exit-code taxonomy for orchestrators.
- **gittree** - Clean+synced gate for repo-shaped verbs.
- **passthrough** - Audited urfave subcommand around an existing binary.
- **repocfg** - Per-repo command allowlist from a configurable YAML.
- **egress** - Per-invocation CONNECT proxy with consumer allowlist.
- **mcporter** - Pre-exec preflight for the mcporter tool, secret resolver.
- **dispatch** - Fire `claude` against a real open GitHub issue.
- **shell**, **ttlcache**, **workdir** - Supporting utilities.
- **sudo** - Policy-free interactive sudo plumbing over ssh.
- **respfmt** - JSON response renderer with JMESPath + five output formats.
- **skillgen** - Render an urfave/cli command tree into markdown or yaml.
- **config** - Layered-config primitives and a generic `OverlayFile[T]`.
- **profiles** - Per-host lockdown profile registry.
- **decision** - Per-call profile-aware evaluator.

## Repo development

- `.agent-guard/agent-guard.yaml` declares local dev verbs.
- `Makefile` is the source of truth for what each verb actually runs.
- `coily lint` checks the yaml/Makefile contract on every CI run.
- `.golangci.yaml` mirrors urfave/cli's minimal config.
- `staticcheck.conf` enables all checks (mirrors urfave/cli).
- CI runs `go vet`, `go build`, `go test -race`, golangci-lint v2.12.2.

## Deferred to v0.1

- **lockdown** - Generic permission-file writer with a `Driver` interface. [#2](https://github.com/coilysiren/cli-guard/issues/2).

## See also

- [README.md](../README.md) - human-facing intro.
- [AGENTS.md](../AGENTS.md) - agent-facing operating rules.
- [.agent-guard/agent-guard.yaml](../.agent-guard/agent-guard.yaml) - allowlisted commands.
- [features-detail.md](features-detail.md) - per-primitive details.

Cross-reference convention from [coilysiren/agentic-os#59](https://github.com/coilysiren/agentic-os/issues/59).
