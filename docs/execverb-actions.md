# exec-dialect complex actions

An exec-dialect `wrap` block may declare `action` nodes: ordered `call`
sequences over granted exec leaves, executed by the shared `pkg/stepflow`
engine. The engine authorizes and executes each concrete call; the caller owns
any deployment, health, or recovery policy.

## Grammar

```kdl
wrap ward-kdl ops eco server {
    exec ssh {
        argv-prefix "kai@kai-server"
    }
    can run snapshot { argv bash "/scripts/eco-server-snapshot.sh" }
    can run apply { bin scp; argv "-r" }       // bin override: no ssh prefix
    can run restart { argv bash "/scripts/eco-server-restart.sh" }

    action promote {
        input mod { positional; required; help "mod name" }
        call run snapshot { as snap }
        call run apply { args "$mod" }
        call run restart
    }
}
```

## Semantics

- A step is `call run <grant>`: the exec form of the granted-only rule. Step
  `args` are **positional** tokens (`args "$mod" "literal"`), appended after
  the grant's pinned `argv`. Named `args { k v }` blocks are refused.
- Each step's output decodes to `{exit_code, ok, stdout, stderr, last_line,
  kv{...}}`: `last_line` is the final-stdout-line id convention, and `kv`
  holds every `key=val` stdout token. Later steps may read `$as.field`.
- Steps run in declaration order. A non-zero exit or spawn failure stops the
  sequence and returns a failed action; no later step is run.
- **`bin <binary>`** on a grant overrides the wrap binary for that leaf, so an
  ssh area's apply step can run `scp`. The override does **not** inherit the
  wrap's `argv-prefix` - that prefix pins the wrap binary's transport.
- Guards hold everywhere: resolved step args pass the shell-metachar gate and
  the grant's `when`/flag policies; sealed grants take no step args; a
  passthrough funnel or `allow` list takes no actions. Every step fires through
  the consumer's audit wrap as its own row, under the action envelope's row.
- `--dry-run` renders the step plan (argv previews and bindings) and fires
  nothing.

## See also

- [execverb.md](execverb.md) - the exec dialect this extends.
