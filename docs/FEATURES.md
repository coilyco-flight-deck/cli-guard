# cli-guard features

Inventory of what cli-guard does today. Scope changes should land in the same commit that touches code, so this file stays a faithful mirror of the public API. Pair with `examples/<feature>/` to see each primitive end-to-end.

## Framework primitives

- **audit** - Append-only JSONL invocation log with lumberjack rotation. Foundation for every other primitive.
- **policy** - Argv validation rejecting shell metacharacters before they reach `execve`.
- **hook** - Shared Claude Code PreToolUse engine. Consumers register integrity rules and routing hints; the engine owns a non-configurable deny on arbitrary-code execution (interpreter invocation, execution from a writable scratch dir) that fires on every segment of a compound command, so a denied interpreter cannot launder behind an allowed token or via a `/tmp` shebang script.
- **verb** - Middleware wrapping every `*cli.Command.Action` in the standard pipeline (validate → execute → audit).
- **scope** - Resolve `--commit-scope=auto` to a git toplevel so every audit row binds to a reconstructable commit.
- **exitcode** - Public exit-code taxonomy (success / generic / policy-denied / upstream-failed / internal / user-error) for orchestrators.
- **gittree** - Clean+synced gate refusing repo-shaped verbs on a dirty tree.
- **passthrough** - Thin wrapper that embeds an existing binary (aws, gh, kubectl, ...) as an audited urfave subcommand.
- **repocfg** - Per-repo command allowlist loaded from a configurable YAML file.
- **egress** - Per-invocation CONNECT proxy with consumer-supplied allowlist. Enforce / observe modes.
- **mcporter** - Pre-exec preflight for the mcporter tool. Scans `~/.mcporter/mcporter.json` for `${VAR}` references and resolves them via a consumer-supplied `SecretResolver`, injecting values as env vars on the child process only. `WithTTLCache` provides on-disk caching. Wired into passthrough via `WithSecretResolver`.
- **dispatch** - Fire `claude` against a real open GitHub issue, headless or interactive. Prompt comes from the issue body, never free text. Host-specific bits inject through `dispatch.Config`. Carries a `reap` verb for merged worktrees.
- **shell**, **ttlcache**, **workdir** - Supporting utilities.
- **sudo** - Policy-free plumbing for driving interactive sudo over ssh without carrying a password at rest or leaking it through argv. /dev/tty prompt, in-place buffer wipe, stderr sentinel match for `sudo -n` denial.
- **respfmt** - JSON response renderer with optional JMESPath projection and five output formats (yaml, yaml-stream, json, text, table). Mirrors aws CLI's `--query` / `--output` surface so operator muscle memory transfers, with the default flipped to yaml for editor-friendly piped output.
- **skillgen** - Render an urfave/cli command tree into a deterministic markdown lookup table or yaml document. Pairs with the verb package: every wrapped Action is reachable by name from the rendered output, so the rendered file is a machine-checkable mirror of the invocation surface.
- **config** - Layered-config primitives: `~/.coily` and `./.coily` path helpers, `ExpandHome`, audit-slug derivation from `git remote get-url origin`, the `Audit` rotation-knobs struct, and a generic `OverlayFile[T]` helper that consumers call per layer to assemble their own schema.
- **profiles** - Per-host lockdown profile registry. Loads `~/.coily/coily.yaml`, validates each declared profile against the cli-guard/profile axis vocabulary, and resolves a name to a Coordinate. Missing file or unknown name falls back to `profile.Strictest()` deny-everything.
- **decision** - Per-call profile-aware evaluator. Resolves a session profile through the profiles registry and returns an `audit.ProfileDecision` ready to attach to an audit row. Plug in via `verb.Spec.OnEvaluate`. Also ships a default `audit.RedactPolicy` covering common secret-flag names and identifier patterns (AWS account ids, email addresses).

## Repo development

- `.agent-guard/agent-guard.yaml` declares local dev verbs (`agent-guard exec build`, `test`, `vet`, `lint`, `tidy`, `cover`).
- `Makefile` is the source of truth for what each verb actually runs.
- `coily lint` checks the yaml/Makefile contract on every CI run.
- `.golangci.yaml` mirrors urfave/cli's minimal config.
- `staticcheck.conf` enables all checks (mirrors urfave/cli).
- GitHub Actions CI runs `go vet`, `go build`, `go test -race`, and golangci-lint v2.12.2.

## Deferred to v0.1

- **lockdown** - Generic permission-file writer with a `Driver` interface (Claude Code driver as the default). Tracked at [#2](https://github.com/coilysiren/cli-guard/issues/2).

## See also

- [README.md](../README.md) - human-facing intro.
- [AGENTS.md](../AGENTS.md) - agent-facing operating rules.
- [.agent-guard/agent-guard.yaml](../.agent-guard/agent-guard.yaml) - allowlisted commands.

Cross-reference convention from [coilysiren/agentic-os#59](https://github.com/coilysiren/agentic-os/issues/59).
