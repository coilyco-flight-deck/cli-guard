# cli-guard features

Inventory of what cli-guard does today. Scope changes land in the same commit that touches code. Pair with `examples/<feature>/` to see each primitive end-to-end.

## Framework primitives

Packages are grouped by **guarded surface** plus a shared `pkg/` core; `cli/`, `http/`, `mcp/` depend downward on `pkg/`, never on each other. See [architecture.md](architecture.md) for the surface model and import rule, [features-detail.md](features-detail.md) for per-primitive depth.

### CLI passthrough surface (`cli/`)

- **passthrough** - Audited urfave subcommand around an existing binary.
- **execverb** - Exec-dialect KDL verbs. See [execverb.md](execverb.md).
- **verb** - Middleware around every `*cli.Command.Action`.
- **shell** / **sandbox** - Subprocess execution plus its seccomp/namespace jail (Linux); the execve enforcement boundary.
- **gittree** - Clean+synced gate for repo-shaped verbs.
- **repocfg** / **allowlist** - Per-repo command allowlist from a configurable YAML, validated against the repo Makefile.
- **catalog** - Assert a repo's config YAML carries a `catalog:` block with the required keys.
- **hook** / **hookcfg** - Claude Code PreToolUse engine and the `repocfg.Security` -> `hook.Protected` mapping.
- **shim** - PATH shim per protected binary (UX shadowing, not enforcement). See [deny-by-structure.md](deny-by-structure.md).
- **doctor** - Verify the deny-by-structure floor (no passwordless sudo, real binary not agent-executable, no credential env).
- **sudo** - Policy-free interactive sudo plumbing over any stdin-piping transport.
- **dispatch** - Fire `claude` against a real open issue; consumer swaps the resolver via `Config.IssueFetcher`.
- **lockdown** / **profiles** / **profile** / **decision** - Permission-file writer, per-host profile registry, shared profile type, and the per-call profile-aware evaluator.
- **cmd/cli-guard-hook** - Buildable PreToolUse binary for shell-only consumers.

### HTTP request surface (`http/`)

- **egress** - Per-invocation CONNECT proxy with consumer allowlist.
- **guardfile** / **specverb** / **specgen** / **specdrv** - Spec-driven verbs from a KDL Guardfile + Swagger spec; no-code driver. See [specverb.md](specverb.md), [specverb-driver.md](specverb-driver.md).
- **respfmt** - JSON response renderer with JMESPath + five output formats.
- **ghcache** / **ghidcache** / **ghratelimit** / **stscache** - Forgejo/GitHub response, id, rate-limit, and STS credential caches.

### MCP surface (`mcp/`)

- **mcporter** - Pre-exec preflight for the mcporter tool, secret resolver.

### Shared core (`pkg/`)

- **audit** - Append-only JSONL invocation log with lumberjack rotation.
- **policy** - Argv validation rejecting shell metacharacters.
- **scope** - Resolve cwd to its git toplevel for the audit row's RepoRoot.
- **exitcode** - Public exit-code taxonomy for orchestrators.
- **config** - Layered-config primitives and a generic `OverlayFile[T]`.
- **ttlcache** - Generic TTL-keyed cache machinery (backs the surface caches).
- **workdir** - Working-directory resolution helper.
- **skillgen** - Render an urfave/cli command tree into markdown or yaml.

## Repo development

- `Makefile` is the source of truth for the dev verbs (cli-guard is unguarded - it carries no `.ward`/`.coily` config and runs dev verbs straight through `make`).
- `.golangci.yaml` mirrors urfave/cli's minimal config.
- `staticcheck.conf` enables all checks (mirrors urfave/cli).
- CI runs `go vet`, `go build`, `go test -race`, golangci-lint v2.12.2.
- Release is automated and Forgejo-canonical, tag-only; consumers (ward, coily) self-bump off its tags. See [release-pipeline.md](release-pipeline.md).

## See also

- [README.md](../README.md) - human-facing intro.
- [AGENTS.md](../AGENTS.md) - agent-facing operating rules.
- [features-detail.md](features-detail.md) - per-primitive details.

Cross-reference convention from [coilysiren/agentic-os#59](https://github.com/coilysiren/agentic-os/issues/59).
