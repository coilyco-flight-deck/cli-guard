# guarded-rollback primitives

A **complex action** (see [specverb-actions.md](specverb-actions.md)) runs a
bounded sequence of granted leaves. On its own a `call` sequence is forward-only:
a mid-sequence failure aborts and nothing undoes the completed steps. These
primitives close that gap so a `wrap`-block action can express a health-gated ops
pipeline without a compiled state machine. Filed as
the rollback path;
`ward ops eco {test,promote}` is the first consumer.

Three primitives, all deny-by-default and per-call audited:

1. **A transport-agnostic step abstraction.** The engine lives in `pkg/stepflow`
   and fires each step through its `Runner` seam, not a hard-wired HTTP call.
   Two shipped transports: the specverb HTTP runtime, and execverb's captured
   exec/ssh runner (exec-dialect actions, [execverb.md](execverb.md)).
2. **A compensating rollback.** A `call` may declare a `compensate` step. When a
   later step fails, the engine walks the completed steps in reverse and fires
   each compensation, undoing the work instead of aborting bare.
3. **A canary watch.** A `canary` block re-samples a granted health leaf over a
   window after the forward steps. A `degraded-when` match drives the same
   rollback path, so a promotion is undone the moment health regresses.

## Grammar

```kdl
action promote {
    input service { positional; required }

    call create snapshot {
        args { name $service }
        as snap
        compensate delete snapshot { args { id $snap.id } }   // undo: delete it
    }
    call promote deploy {
        args { name $service }
        compensate rollback deploy { args { name $service } } // undo: roll back
    }

    canary get health {
        args { service $service }
        every         "5s"                 // sample interval (quoted: no bare 5s)
        window        "2m"                  // watch bound; elapsing passes
        degraded-when "error_rate > `0.05`"
        healthy-when  "stable == `true`"    // optional early-pass verdict
        as health
    }
}
```

## Semantics

- A **compensation runs only for a completed step.** When step N fails, the
  engine compensates N-1, N-2, ... 0 in reverse. Its `args` resolve against the
  same scope as forward steps, so `$snap.id` threads the created id back out.
- Rollback is **best-effort**: a failed compensation is recorded and the rest
  still run. The exit is coded `action_rolled_back` (or `action_failed` when none
  were declared or a compensation also failed).
- The **canary** runs after the `call` steps, so it needs at least one (a canary
  on a poll/collect action is a grammar error). Each tick evaluates
  `degraded-when` against the sample, with `$input` and every `$<as>` in scope.
- A **`degraded-when` match** rolls the completed steps back and exits
  `canary_degraded`. An optional **`healthy-when`** ends the watch early and
  clean. The **window elapsing** with neither verdict is a pass.
- A **blind sample** (the probe could not run at all) also rolls back, exiting
  `canary_blind`: an unobservable target is never left promoted.
- `--dry-run` prints each call with its compensation and the canary block, firing
  nothing; `describe` documents both.

## Testing the seam

The `stepflow.Runner` interface is the unit-test seam: fake runners
(`pkg/stepflow/stepflow_test.go`, `action_rollback_test.go`) record fired steps,
proving the engine is transport-independent. End-to-end tests drive the HTTP
path against an `httptest` server and the exec path against a scripted capture.

## See also

- [specverb-actions.md](specverb-actions.md) - the engine these primitives extend.
- [execverb.md](execverb.md) - the exec/ssh transport (exec-dialect actions).
