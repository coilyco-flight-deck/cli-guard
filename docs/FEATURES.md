# cli-guard features

Inventory of cli-guard today. See `examples/<feature>/` for each end-to-end.

## Framework primitives

Packages are grouped by **guarded surface** over a shared `pkg/`. See [architecture.md](architecture.md).

### CLI passthrough surface (`cli/`)

- **passthrough** - Audited urfave subcommand around an existing binary.
- **execverb** - Exec-dialect KDL verbs + the default-allow `passthrough <bin>` funnel. See [execverb.md](execverb.md).
- **awsgate** - Deny sensitive-glob aws reads.
- **verb** - Middleware around every `*cli.Command.Action`.
- **shell** / **sandbox** - Subprocess exec + seccomp/namespace jail (Linux). See [sandbox](sandbox.md).
- **gittree** - Clean+synced gate for repo-shaped verbs.
- **repocfg** / **allowlist** - Per-repo command allowlist YAML, validated vs the Makefile.
- **catalog** - Assert a repo's config YAML carries a `catalog:` block with required keys.
- **hook** / **hookcfg** - PreToolUse engine; `repocfg.Security` -> `hook.Protected` / `ForbiddenArgv` maps, guard `Registry`, settings `Installer`.
- **shim** - PATH shim per protected binary (UX shadowing, not enforcement). See [deny-by-structure.md](deny-by-structure.md).
- **doctor** - Verify the deny-by-structure floor.
- **sudo** - Policy-free interactive sudo plumbing over any stdin transport.
- **dispatch** - Fire `claude` at an open issue; swap resolver, backend, verdict.
- **profiles** / **profile** / **decision** - Profile registry, coordinate axes, per-call evaluator.
- **cmd/cli-guard-hook** - PreToolUse binary for shell-only consumers.

### HTTP request surface (`http/`)

- **egress** - Per-invocation CONNECT proxy with consumer allowlist.
- **guardfile** / **specverb** / **specgen** / **codegen** - Spec-driven verbs from a Guardfile (Swagger 2/OpenAPI 3); `inherit` layers tiers. See [specverb.md](specverb.md).
- **complex actions** - `wrap`-block `poll`/`call` verbs; `action <verb> <resource>` shadows that leaf. See [specverb-actions.md](specverb-actions.md).
- **respfmt** - JSON response renderer with JMESPath + five output formats.
- **ghcache** / **ghidcache** / **ghratelimit** / **stscache** - Forgejo/GitHub response, id, rate-limit, STS caches.

### MCP surface (`mcp/`)

- **mcporter** - Pre-exec preflight for the mcporter tool, secret resolver.

### Shared core (`pkg/`)

- **audit** - Append-only JSONL invocation log with lumberjack rotation.
- **policy** - Argv validation rejecting shell metacharacters.
- **scope** - Resolve cwd to its git toplevel for the audit row's RepoRoot.
- **exitcode** - Public exit-code taxonomy for orchestrators.
- **valuesource** - Shared `value <provider>` resolution (env/file/literal) for both engines.
- **config** - Layered-config primitives and a generic `OverlayFile[T]`.
- **ttlcache** - Generic TTL-keyed cache (backs the surface caches).
- **workdir** - Working-directory resolution helper.
- **skillgen** - Render an urfave/cli command tree into markdown or yaml.
- **broker** / **credseed** - Root credential-broker policy core + creds env-file seeder. See [broker.md](broker.md).
- **scan** / **attribution** / **flock** / **version** / **issueref** / **ownertrust** - Reusable helpers lifted from ward: junk-scan, attribution, advisory flock, semver, issue-ref, owner-trust gate. See [ward-helpers.md](ward-helpers.md).

## Repo development

- `Makefile` is the source of truth for dev verbs (cli-guard is unguarded).
- `.golangci.yaml` / `staticcheck.conf` mirror urfave/cli; CI runs vet, build, `test -race`, golangci-lint v2.12.2, trufflehog.
- Release is automated, Forgejo-canonical, tag-only; consumers self-bump. See [release-pipeline.md](release-pipeline.md).

## See also

- [README.md](../README.md) - human-facing intro.
- [AGENTS.md](../AGENTS.md) - agent-facing operating rules.
- [features-detail.md](features-detail.md) - per-primitive details.

Cross-reference convention from [coilysiren/agentic-os#59](https://github.com/coilysiren/agentic-os/issues/59).
