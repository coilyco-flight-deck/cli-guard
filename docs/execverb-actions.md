# exec-dialect complex actions

An exec-dialect `wrap` block may declare `action` nodes: ordered `call`
sequences with compensating rollback and a canary health watch, run over
granted exec leaves by the shared `pkg/stepflow` engine. The grammar is the
spec dialect's ([specverb-rollback.md](specverb-rollback.md)); the transport is
a captured exec invocation. `ward ops eco {test,promote}` is the first consumer.

## Grammar

```kdl
wrap ward-kdl ops eco server {
    exec ssh {
        argv-prefix "kai@kai-server"
    }
    can run snapshot { argv bash "/scripts/eco-server-snapshot.sh" }
    can run apply { bin scp; argv "-r" }       // bin override: no ssh prefix
    can run health { argv bash "/scripts/eco-server-health-check.sh" }
    can run rollback { argv bash "/scripts/eco-server-rollback.sh" }

    action promote {
        input mod { positional; required; help "mod name" }
        call run snapshot {
            as snap
            compensate run rollback { args "$snap.last_line" }
        }
        call run apply { args "$mod" }
        call run health
        canary run health {
            every "20s"; window "60s"
            degraded-when "exit_code != `0`"
            as health
        }
    }
}
```

## Semantics

- A step is `call run <grant>`: the exec form of the granted-only rule. Step
  `args` are **positional** tokens (`args "$mod" "literal"`), appended after
  the grant's pinned `argv`. Named `args { k v }` blocks are refused.
- Each step's output decodes to `{exit_code, ok, stdout, stderr, last_line,
  kv{...}}`: `last_line` is the final-stdout-line id convention (snapshot ids),
  `kv` holds every `key=val` stdout token - the shapes predicates and
  `$as.field` data-flow read.
- A **forward step failing** (non-zero exit or spawn failure) rolls the
  completed steps back in reverse. A **canary sample's non-zero exit is data**
  for `degraded-when`, not an error; only an unspawnable probe is blind
  (`canary_blind`, which also rolls back).
- **`bin <binary>`** on a grant overrides the wrap binary for that leaf, so an
  ssh area's apply step can run `scp`. The override does **not** inherit the
  wrap's `argv-prefix` - that prefix pins the wrap binary's transport.
- Guards hold everywhere: resolved step args pass the shell-metachar gate and
  the grant's `when`/flag policies; sealed grants take no step args; a
  passthrough funnel or `allow` list takes no actions. Every step fires through
  the consumer's audit wrap as its own row, under the action envelope's row.
- `--dry-run` renders the step plan (argv previews, compensations, canary)
  and fires nothing.

## See also

- [specverb-rollback.md](specverb-rollback.md) - the shared engine and grammar.
- [execverb.md](execverb.md) - the exec dialect this extends.
