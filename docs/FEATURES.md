# cli-guard features

Inventory of what cli-guard does today. Scope changes should land in the same commit that touches code, so this file stays a faithful mirror of the public API. Pair with `examples/<feature>/` to see each primitive end-to-end.

## Framework primitives

- **audit** - Append-only JSONL invocation log with lumberjack rotation. Foundation for every other primitive.
- **policy** - Argv validation rejecting shell metacharacters before they reach `execve`.
- **verb** - Middleware wrapping every `*cli.Command.Action` in the standard pipeline (validate → execute → audit).
- **scope** - Resolve `--commit-scope=auto` to a git toplevel so every audit row binds to a reconstructable commit.
- **exitcode** - Public exit-code taxonomy (success / generic / policy-denied / upstream-failed / internal / user-error) for orchestrators.
- **gittree** - Clean+synced gate refusing repo-shaped verbs on a dirty tree.
- **passthrough** - Thin wrapper that embeds an existing binary (aws, gh, kubectl, ...) as an audited urfave subcommand.
- **repocfg** - Per-repo command allowlist loaded from a configurable YAML file.
- **egress** - Per-invocation CONNECT proxy with consumer-supplied allowlist. Enforce / observe modes.
- **shell**, **ttlcache**, **workdir** - Supporting utilities.
- **sudo** - Policy-free plumbing for driving interactive sudo over ssh without carrying a password at rest or leaking it through argv. /dev/tty prompt, in-place buffer wipe, stderr sentinel match for `sudo -n` denial.
- **respfmt** - JSON response renderer with optional JMESPath projection and five output formats (yaml, yaml-stream, json, text, table). Mirrors aws CLI's `--query` / `--output` surface so operator muscle memory transfers, with the default flipped to yaml for editor-friendly piped output.
- **skillgen** - Render an urfave/cli command tree into a deterministic markdown lookup table or yaml document. Pairs with the verb package: every wrapped Action is reachable by name from the rendered output, so the rendered file is a machine-checkable mirror of the invocation surface.
- **config** - Three-layer YAML config loader: Go-literal defaults overlaid by `~/.coily/config.yaml` overlaid by `./.coily/config.yaml`, with per-key precedence and `~/` expansion. Audit log path defaults to `~/.coily/audit/<repo-slug>.jsonl`, slug derived from `git remote get-url origin`. `COILY_AUDIT_LOG` env var wins over all layers.
- **profiles** - Per-host lockdown profile registry. Loads `~/.coily/coily.yaml`, validates each declared profile against the cli-guard/profile axis vocabulary, and resolves a name to a Coordinate. Missing file or unknown name falls back to `profile.Strictest()` deny-everything.
- **decision** - Per-call profile-aware evaluator. Resolves a session profile through the profiles registry and returns an `audit.ProfileDecision` ready to attach to an audit row. Plug in via `verb.Spec.OnEvaluate`. Also ships a default `audit.RedactPolicy` covering common secret-flag names and identifier patterns (AWS account ids, email addresses).

## Repo development

- `.coily/coily.yaml` declares local dev verbs (`coily exec build`, `test`, `vet`, `lint`, `tidy`, `cover`).
- `Makefile` is the source of truth for what each verb actually runs.
- `coily lint` checks the yaml/Makefile contract on every CI run.
- `.golangci.yaml` mirrors urfave/cli's minimal config.
- `staticcheck.conf` enables all checks (mirrors urfave/cli).
- GitHub Actions CI runs `go vet`, `go build`, `go test -race`, and golangci-lint v2.12.2.

## Deferred to v0.1

- **lockdown** - Generic permission-file writer with a `Driver` interface (Claude Code driver as the default). Tracked at [#2](https://github.com/coilysiren/cli-guard/issues/2).
