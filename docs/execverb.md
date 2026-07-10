# exec-dialect verbs (execverb)

`execverb` is the exec-transport sibling of [specverb](specverb.md): the same policy-as-KDL-sentences design, pointed at wrapped binaries instead of HTTP APIs (git, package managers, remote-exec, local-agent launchers).

## Grammar

```kdl
wrap ward git {
    exec git
    can run status
    can run commit { deny-flag "--no-verify" }
    can run push { allow-flag "--force-with-lease" }
    never run "reflog expire"
}
```

- **`exec <bin>`** - the real binary, fixed at parse. The caller can never substitute it.
- **`argv-prefix`** (child of `exec`) - an unoverridable leading argv, the remote-exec transport: `exec ssh { argv-prefix "kai@kai-server" "kubectl" ... }` pins the invocation ahead of the subcommand.
- **`env <NAME> { value <provider> "<addr>" }`** (child of `exec`) - an env var on the wrapped process, resolved at exec time via a provider (so a secret comes from SSM, not the guardfile); `env "NAME" "literal"` for a committed value. Providers via `pkg/valuesource`.
- **`can run <subcommand>`** - deny-by-default: only named subcommands mount. A quoted multi-word sentence (`"admin user list"`) is a nested path.
- **`argv <tokens...>`** (child of a grant) - per-grant invocation override: tokens appended after `argv-prefix` in place of the subcommand, decoupling the leaf name from what runs (`headless { argv "-p" }` -> `claude -p <args>`; bare `argv` runs the bin). Override tokens are trusted and skip the flag policy. Not allowed with `can run "*"`.
- **`sealed`** (child of a grant) - forbids trailing caller args so the pinned `argv` forwards **exactly** - a strict single-resource verb. Requires `argv`; a non-empty caller token is refused before exec.
- **`can run "*"`** - open-passthrough grant: the group is one leaf, every operation reaches the binary. Must be the only grant.
- **`passthrough <bin>`** - funnel sugar (`exec` + `can run "*"`) with wrap-level `never pass`/`only pass` guards. See [passthrough.md](passthrough.md).
- **Flag policy per grant** - `deny-flag` (default-allow minus denials) or `allow-flag` (strict allowlist). `describe` adds a note.
- **`when <selector> matches <glob...>`** / **`deny-when ...`** - argv guards. `when` passes only on a match, `deny-when` refuses on one. The selector names an argv slot: a **flag name** (`secret-id` reads `--secret-id`), **`any-arg`** (all positionals), or **`argN`** (Nth positional, 0-based). `{ only-reads }` scopes to read-only aws ops. A `*` glob needs quoting.
- **`gate <name> { ... }`** - a registered preflight gate for logic not sayable declaratively (`pattern`, `allow`), e.g. `aws-read`. Unknown names fail closed.
- **`never run`** - an explicit denial; parses for docs, mounts none.
- **`allow <bin...>`** - inspect-list sugar: N read-only funnels per wrap. See [execverb-inspect.md](execverb-inspect.md).
- **`action` / `bin`** - complex actions + the per-grant binary override. See [execverb-actions.md](execverb-actions.md).

Unknown nodes fail closed. Per-operation grants guard a kwarg (`deny-when secret-id matches "*prod*"`) or positional (`deny-when arg0 matches "*tfstate*"`); a `can run "*"` funnel uses `any-arg`.

## Engine

`execverb.Mount(root, Config{Guardfile, Wrap, Run})` mirrors `specverb.Mount`: one leaf per grant under `verb.Wrap` (audit + argv metachar gate), `SkipFlagParsing` so every caller arg passes through after the policy check. The resolved invocation is `bin + argv-prefix + (subcommand or argv override) + caller args`; policy decides whether a call happens, not what it targets. A `sealed` grant also pins the target, refusing any caller arg so the invocation is just `bin + argv-prefix + argv`.

## Driver integration

`kdl-specs` merges exec consumers into a spec binary; `execverb.Describe` gives doc parity. See [mixed transports](specverb-mixed-transports.md).

Design: the security-pure-engine refactor. `sealed` is the single-resource variant.
