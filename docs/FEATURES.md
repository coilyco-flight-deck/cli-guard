# cli-guard features

Inventory of cli-guard today. See `examples/<feature>/` for each.

## Framework primitives

Grouped by **guarded surface** over a shared `pkg/`. See [architecture.md](architecture.md).

### CLI passthrough surface (`cli/`)

- **passthrough** - Audited urfave subcommand around an existing binary.
- **execverb** - Exec-dialect KDL verbs + the `passthrough <bin>` funnel. See [execverb.md](execverb.md); actions: [execverb-actions.md](execverb-actions.md).
- **awsgate** - Deny sensitive-glob aws reads.
- **verb** - Middleware around every `*cli.Command.Action`.
- **shell** / **sandbox** - Subprocess exec + seccomp/namespace jail (Linux). See [sandbox](sandbox.md).
- **gittree** - Clean+synced gate for repo-shaped verbs.
- **repocfg** / **allowlist** - Per-repo command allowlist YAML, validated vs the Makefile.
- **catalog** - Assert a repo config carries a `catalog:` block with required keys.
- **hook** / **hookcfg** - PreToolUse engine expanding `repocfg.Security` into the guard registry + installer.
- **shim** - PATH shim per protected binary. See [deny-by-structure.md](deny-by-structure.md).
- **doctor** - Verify the deny-by-structure floor.
- **sudo** - Policy-free interactive sudo plumbing over any stdin transport.
- **dispatch** - Fire `claude` at an open issue; swap resolver, backend, verdict.
- **profiles** / **profile** / **decision** - Profile registry, axes, per-call evaluator.
- **cmd/cli-guard-hook** - PreToolUse binary for shell-only consumers.

### HTTP request surface (`http/`)

- **egress** - Per-invocation CONNECT proxy with consumer allowlist.
- **guardfile** / **specverb** / **kdl-specs** / **codegen** - Spec-driven verbs from a Guardfile (Swagger 2/OpenAPI 3). See [specverb.md](specverb.md).
- **complex actions** - `wrap`-block `poll`/`call`/`collect` verbs; the mount form shadows its leaf. See [specverb-actions.md](specverb-actions.md).
- **guarded rollback** - `compensate` + `canary` health-window rollback. See [specverb-rollback.md](specverb-rollback.md).
- **respfmt** - JSON renderer, JMESPath + five output formats.
- **ghcache** / **ghidcache** / **ghratelimit** / **stscache** - Forgejo/GitHub response, id, rate-limit, STS caches.

### MCP surface (`mcp/`)

- **mcporter** - Pre-exec preflight for the mcporter tool, secret resolver.

### Shared core (`pkg/`)

- **audit** - Append-only rotated JSONL invocation log.
- **policy** - Argv validation rejecting shell metachars.
- **scope** - Resolve cwd to its git toplevel for the audit RepoRoot.
- **exitcode** - Public exit-code taxonomy for orchestrators.
- **valuesource** - Shared `value <provider>` resolution.
- **config** - Layered-config primitives + `OverlayFile[T]`.
- **fleetconfig** - KDL fleet-config validator (core, unguarded). See [fleetconfig.md](fleetconfig.md).
- **stepflow** - Transport-agnostic sequence/rollback/canary engine.
- **ttlcache** - Generic TTL-keyed cache.
- **workdir** - Working-directory resolver.
- **skillgen** - Render an urfave/cli command tree to markdown/yaml.
- **broker** / **credseed** - Credential-broker core + env seeder. See [broker.md](broker.md).
- **scan** / **attribution** / **flock** / **version** / **issueref** / **ownertrust** - Ward-lifted helpers. See [ward-helpers.md](ward-helpers.md).
- **agentid** - Canonical agent-id generator (Go port, aos o2r vector).

## Repo development

- `Makefile` is the source of truth for dev verbs (cli-guard is unguarded).
- `.golangci.yaml` / `staticcheck.conf` mirror urfave/cli; CI runs vet, build, `test -race`, golangci-lint, trufflehog.
- Release is automated, Forgejo-canonical, tag-only; consumers self-bump. See [release-pipeline.md](release-pipeline.md).

## See also

- [README.md](../README.md) - human-facing intro.
- [AGENTS.md](../AGENTS.md) - agent-facing operating rules.
- [features-detail.md](features-detail.md) - per-primitive details.

Cross-reference convention from [coilysiren/agentic-os#59](https://github.com/coilysiren/agentic-os/issues/59).
