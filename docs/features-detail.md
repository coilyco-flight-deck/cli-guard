# cli-guard features (detail)

Per-primitive detail behind the [FEATURES.md](FEATURES.md) index.

- **audit** - Append-only JSONL invocation log with lumberjack rotation. Foundation for the rest.
- **policy** - Argv validation rejecting shell metacharacters before they reach `execve`.
- **hook** - Shared Claude Code PreToolUse engine. Consumers register integrity rules and routing hints; the engine owns a non-configurable deny on arbitrary-code execution (interpreter invocation, execution from a writable scratch dir) that fires on every segment of a compound command, so a denied prefix cannot launder behind an allowed token or a `/tmp` shebang.
- **verb** - Middleware wrapping every `*cli.Command.Action` in the standard pipeline (validate → execute → audit).
- **scope** - Resolve cwd to its git toplevel, best-effort, stamping each audit row's forensic RepoRoot (empty outside any repo).
- **exitcode** - Public exit-code taxonomy (success / generic / policy-denied / upstream-failed / internal / user-error) for orchestrators.
- **gittree** - Clean+synced gate refusing repo-shaped verbs on a dirty tree.
- **passthrough** - Thin wrapper that embeds an existing binary (aws, gh, kubectl, ...) as an audited urfave subcommand.
- **repocfg** - Per-repo command allowlist loaded from a configurable YAML file.
- **catalog** - Port of agentic-os's catalog-block-present hook. `Check` uses the first existing candidate path and asserts a top-level `catalog:` mapping with the required descriptor keys (kind, type, system, owner, lifecycle, description, dependsOn). Returns `[]Problem` like allowlist.
- **egress** - Per-invocation CONNECT proxy with consumer-supplied allowlist. Enforce / observe modes.
- **mcporter** - Pre-exec preflight for the mcporter tool. Scans `~/.mcporter/mcporter.json` for `${VAR}` references and resolves via a consumer-supplied `SecretResolver`, injecting values as env vars on the child only. `WithTTLCache` adds on-disk caching; wired into passthrough via `WithSecretResolver`.
- **sudo** - Policy-free plumbing for driving interactive sudo over any stdin-piping transport without carrying a password at rest or leaking it through argv. /dev/tty prompt, in-place buffer wipe, `sudo -n` denial sentinel.
- **respfmt** - JSON response renderer with optional JMESPath projection and five output formats (yaml, yaml-stream, json, text, table). Mirrors aws CLI's `--query` / `--output` surface; default flipped to yaml for editor-friendly piped output.
- **skillgen** - Render an urfave/cli command tree into a deterministic markdown lookup table or yaml document. Pairs with verb: every wrapped Action is reachable by name from the output, so the rendered file mirrors the invocation surface.
- **config** - Layered-config primitives: `~/<app-dir>` and `./<app-dir>` path helpers, `ExpandHome`, audit-slug derivation from `git remote get-url origin`, the `Audit` rotation-knobs struct, and a generic `OverlayFile[T]` helper.
- **profiles** - Per-host lockdown profile registry. Loads `~/<app-dir>/<file>`, validates each declared profile against the profile axis vocabulary, resolves a name to a Coordinate. Missing file or unknown name falls back to `profile.Strictest()`.
- **decision** - Per-call profile-aware evaluator. Resolves a session profile through profiles and returns an `audit.ProfileDecision` ready to attach to an audit row. Plug in via `verb.Spec.OnEvaluate`. Ships a default `audit.RedactPolicy` covering common secret-flag names and identifier patterns.
- **shim**, **doctor** - Deny-by-structure pair (PATH-shim UX + sudo/ownership floor). See [deny-by-structure.md](deny-by-structure.md).
- **guardfile**, **specverb** - Spec-driven verb subsystem. See [specverb.md](specverb.md).
