# exec-dialect verbs (execverb)

`execverb` is the exec-transport sibling of [specverb](specverb.md): the same policy-as-KDL-sentences design, pointed at wrapped binaries instead of HTTP APIs. It covers what specverb cannot - git passthroughs, package managers, remote-exec, local-agent launchers - so the whole surface is Guardfile-expressed.

## Grammar

```kdl
wrap ward git {
    exec git

    can run status
    can run commit {
        deny-flag "--no-verify"
        describe "record staged changes"
    }
    can run push {
        allow-flag "--force-with-lease"
    }
    never run "reflog expire"
}
```

- **`exec <bin>`** - the real binary, fixed at parse. The caller can never substitute it.
- **`argv-prefix`** (child of `exec`) - an unoverridable leading argv, the remote-exec transport: `exec ssh { argv-prefix "kai@kai-server" "k3s" "kubectl" ... }` pins the invocation shape ahead of the subcommand.
- **`env <NAME> { value <provider> "<addr>" }`** (child of `exec`) - an env var on the wrapped process, resolved at exec time via a provider (so `OLLAMA_HOST` comes from SSM, not the guardfile); `env "NAME" "literal"` for a committed value. Providers/built-ins shared with specverb (`pkg/valuesource`).
- **`can run <subcommand>`** - deny-by-default: only named subcommands mount. A quoted multi-word sentence (`"admin user list"`) mounts as a nested path.
- **`argv <tokens...>`** (child of a grant) - per-grant invocation override: tokens appended after `argv-prefix` in place of the subcommand, decoupling the leaf name from what runs. `argv` alone runs the bare binary (`launch { argv }` -> `claude`); `argv "-p"` -> `claude -p <args>`; `argv "login" "status"` -> `codex login status`. The friendly name keeps the CLI path and audit name. Override tokens are trusted (like `argv-prefix`), skip the flag policy; caller args still checked. Not allowed with `can run "*"`.
- **`can run "*"`** - open-passthrough grant: the group is one leaf and every operation reaches the binary. Must be the only grant. The funnel for broad tools (aws); guards decide what's refused.
- **Flag policy per grant** - `deny-flag` (default-allow minus denials) or `allow-flag` (strict allowlist). `describe` adds the human note.
- **`when <selector> matches <glob...>`** / **`deny-when ...`** - argv guards in CLI vocabulary. `when` passes only on a match, `deny-when` refuses on one. The selector names an argv slot: a **flag name** (`secret-id` reads `--secret-id`), **`any-arg`** (all positionals), or **`argN`** (Nth positional, 0-based). `{ only-reads }` scopes to read-only aws ops. A glob with `*` needs quoting.
- **`gate <name> { ... }`** - a registered preflight gate for logic not sayable declaratively (`pattern`, `allow`), e.g. `aws-read`. Unknown names fail closed.
- **`never run`** - an explicit denial; parses for documentation value, mounts nothing.

Unknown nodes fail closed. Per-operation grants guard the kwarg (`deny-when secret-id matches "*prod*"`) or positional (`deny-when arg0 matches "*tfstate*"`) naming the resource; a `can run "*"` funnel uses `any-arg`.

## Engine

`execverb.Mount(root, Config{Guardfile, Wrap, Run})` mirrors `specverb.Mount`: one leaf per grant under `verb.Wrap` (audit + argv metachar gate), `SkipFlagParsing` so every caller arg passes through after the policy check. The resolved invocation is always `bin + argv-prefix + (subcommand or argv override) + caller args` - policy decides whether a call happens, not what it targets. `Run` is injectable for tests; nil execs for real.

## Driver integration

`specverb-gen` generates and merges exec consumers into a spec binary; `execverb.Describe` gives doc parity. See [mixed transports](specverb-mixed-transports.md). Follow-up: egress policy blocks (compose with `cli/passthrough`).

Design: [#130](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/130), part of the security-pure-engine refactor ([#123](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/123)).
