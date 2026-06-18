# pre-flight input defaulting

An action `input` may carry a `default <jmespath>`. When the operator omits the
input, the action fires the poll leaf **once as a pre-flight**, evaluates the
JMESPath against that response, and binds the result before the loop starts.

This makes the natural ergonomic - `ci-watch owner/repo` with no `--run`,
resolving "the latest run in the listing" - expressible without a hand-rolled
pre-flight in the consumer. It is the cli-guard half of ward#90's "decide where
defaulting belongs": here, in the action engine, since a pure spec-leaf cannot
host a resolver.

```kdl
input run { flag; default "max([].run_number)"; help "latest run if --run absent" }
```

## Invariants (the same ones `poll` carries)

- **Granted-only.** The pre-flight hits only the poll leaf - the op the Guardfile
  already `can`-grants. No new target, no new grant.
- **Per-call audit.** The pre-flight writes its own leaf audit row, exactly like
  a poll tick, under the envelope row.
- **Dry-run is a plan.** When a defaulted input is absent, `--dry-run` names the
  pre-flight call and the bindings it will resolve, firing nothing.
- **Fails closed.** An unresolvable default - empty listing (so the expression is
  `null`), or a non-scalar selection - is a user error, never a loop on a silent
  null. This matches the unset-input behavior in [specverb-actions.md](specverb-actions.md).

## Rules

- `default` and `required` are mutually exclusive (a default only resolves when
  the input is absent).
- A defaulted input may not also bind a poll `arg`: the request is built before
  the default resolves, so the dependency would be circular.
- `default` is a poll-action binding only; it is rejected on `call` actions.
- Supplying the input on the CLI skips the pre-flight entirely - the explicit
  value wins.

Reference: cli-guard#142, ward#90.
