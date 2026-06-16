# exec-dialect verbs (execverb)

`execverb` is the exec-transport sibling of [specverb](specverb.md): the same policy-as-KDL-sentences design, pointed at wrapped binaries instead of HTTP APIs. It covers the verbs specverb cannot - git passthroughs, package managers, and remote-exec shapes like the forgejo in-pod admin CLI - so a consumer's whole surface can be Guardfile-expressed.

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
- **`argv-prefix`** (child of `exec`) - an unoverridable leading argv, the remote-exec transport: `exec ssh { argv-prefix "kai@kai-server" "k3s" "kubectl" ... }` pins the whole invocation shape ahead of the granted subcommand.
- **`can run <subcommand>`** - deny-by-default: only named subcommands mount. A quoted multi-word sentence (`"admin user list"`) mounts as a nested path.
- **`can run "*"`** - the open-passthrough grant: the group becomes one leaf and every service/operation reaches the binary. Must be the only grant. The funnel shape for broad tools (aws); guards below decide what is refused.
- **Flag policy per grant** - `deny-flag` (default-allow minus denials) or `allow-flag` (strict allowlist when any is present). `describe` adds the human note.
- **`when <selector> matches <glob...>`** / **`deny-when ...`** - argv guards in the CLI's own vocabulary, not an opaque gate name. `when` passes only if a value matches; `deny-when` refuses on a match. The selector names an argv slot: a bare **flag name** (`secret-id` reads `--secret-id`'s value), **`any-arg`** (every positional), or **`argN`** (the Nth positional, 0-based, after the matched subcommand path). Optional `{ only-reads }` scopes the guard to read-only aws ops (via `cli/awsgate`). A glob containing `*` needs quoting; selector and operation tokens stay bare.
- **`gate <name> { ... }`** - a registered preflight gate for logic that cannot be said declaratively (`pattern`, `allow`). `aws-read` shipped first; the aws guardfile now uses `deny-when` instead. Unknown gate names fail closed at build.
- **`never run`** - an explicit denial; parses for documentation value, mounts nothing.

Unknown nodes fail closed, like every Guardfile shape.

Per-operation grants read like the invocation, guarding the kwarg or positional that names the resource:

```kdl
can run secretsmanager get-secret-value {
    deny-when secret-id matches "*prod*"
}
can run s3 ls {
    deny-when arg0 matches "*tfstate*"
}
```

A broad `can run "*"` funnel uses `any-arg` to net every positional; `only-reads` scopes the deny to reads.

## Engine

`execverb.Mount(root, Config{Guardfile, Wrap, Run})` mirrors `specverb.Mount`: one leaf per grant under `verb.Wrap` (audit + argv metachar gate), `SkipFlagParsing` so every caller arg passes through to the wrapped binary after the policy check. The resolved invocation is always `bin + argv-prefix + subcommand + caller args` - policy decides whether the call happens, never what it targets.

`Run` is injectable for tests; nil execs for real with inherited stdio. Audit rows carry dotted names (`ward.git.commit`) identical to the hand-written verb pattern.

## Driver integration

`specverb-gen` generates exec consumers and merges them into a spec binary; `execverb.Describe` gives reference-doc parity. See [mixed transports](specverb-mixed-transports.md).

## Named follow-ups

- env/egress policy blocks (compose with `cli/passthrough`'s options)

Design: [#130](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/130). Part of the security-pure-engine refactor ([#123](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/123)).
