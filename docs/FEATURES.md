# cli-guard features

Inventory of what cli-guard does today. Scope changes should land in the same commit that touches code, so this file stays a faithful mirror of the public API. Pair with `examples/<feature>/` to see each primitive end-to-end.

## Framework primitives

Per-primitive detail in [features-detail.md](features-detail.md).

- **audit** - Append-only JSONL invocation log with lumberjack rotation.
- **policy** - Argv validation rejecting shell metacharacters.
- **hook** - Shared Claude Code PreToolUse engine.
- **verb** - Middleware around every `*cli.Command.Action`.
- **scope** - Resolve cwd to its git toplevel for the audit row's RepoRoot.
- **exitcode** - Public exit-code taxonomy for orchestrators.
- **gittree** - Clean+synced gate for repo-shaped verbs.
- **passthrough** - Audited urfave subcommand around an existing binary.
- **repocfg** - Per-repo command allowlist from a configurable YAML.
- **allowlist** - Validate a repocfg-shaped yaml against the repo Makefile.
- **hookcfg** - Map `repocfg.Security` into `hook.Protected` for hook consumers.
- **cmd/cli-guard-hook** - Buildable PreToolUse binary for shell-only consumers (kap, future siblings).
- **egress** - Per-invocation CONNECT proxy with consumer allowlist.
- **mcporter** - Pre-exec preflight for the mcporter tool, secret resolver.
- **dispatch** - Fire `claude` against a real open issue. Defaults to GitHub (`gh api`); a consumer swaps the resolver via `Config.IssueFetcher` (or adds Forgejo via `FetchForgejoIssue`).
- **shell**, **ttlcache**, **workdir** - Supporting utilities.
- **sudo** - Policy-free interactive sudo plumbing over any stdin-piping transport.
- **respfmt** - JSON response renderer with JMESPath + five output formats.
- **skillgen** - Render an urfave/cli command tree into markdown or yaml.
- **config** - Layered-config primitives and a generic `OverlayFile[T]`. The consumer sets its app-dir once via `config.SetAppDir(".coily")`; cli-guard derives every per-user path (global config dir, local overlay, cache dir, dispatch queue) and the cache-override env var from it, so the framework hardcodes no consumer's filesystem layout.
- **profiles** - Per-host lockdown profile registry.
- **decision** - Per-call profile-aware evaluator.

## Repo development

- `Makefile` is the source of truth for the dev verbs (cli-guard is unguarded - it carries no `.ward`/`.coily` config and runs dev verbs straight through `make`).
- `.golangci.yaml` mirrors urfave/cli's minimal config.
- `staticcheck.conf` enables all checks (mirrors urfave/cli).
- CI runs `go vet`, `go build`, `go test -race`, golangci-lint v2.12.2.

## Deferred to v0.1

- **lockdown** - Generic permission-file writer with a `Driver` interface. [#2](https://github.com/coilysiren/cli-guard/issues/2).

## See also

- [README.md](../README.md) - human-facing intro.
- [AGENTS.md](../AGENTS.md) - agent-facing operating rules.
- [features-detail.md](features-detail.md) - per-primitive details.

Cross-reference convention from [coilysiren/agentic-os#59](https://github.com/coilysiren/agentic-os/issues/59).
