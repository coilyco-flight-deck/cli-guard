# what a complex action cannot express

The boundary [complex actions](specverb-actions.md) draws, why it is deliberate, and what to reach for instead.

## The two limits

`poll`, `collect`, and `call` are **mutually exclusive** within one action, refused at parse rather than at runtime. So an action may page a list, or run an ordered call sequence, or watch one leaf until a condition holds, and never two of those. There is also no per-element fan-out: nothing iterates a collected array to issue a call per element.

Both fall out of invariant 2 and are deliberate. A grammar that could page a collection and then emit one write per row emits an unbounded number of writes, and an action stops being reviewable by reading it. That is the property the whole dialect is built to keep.

The cost is real and worth naming, because it lands hardest on the operations that most want a safety net. **Snapshot, mutate, snapshot again, diff, and write back what the mutation destroyed is structurally outside this dialect** and always will be. Teable's `PUT /field/{id}/convert` emptied 6,536 values in a column while returning 200 with exactly the property the caller asked for. Only a before/after value comparison catches that, and only a per-row write-back recovers from it (umbra#6822). Any convert-with-recovery is a real binary, not an action.

What **is** expressible, and is usually the better guarantee:

- **A pre-flight guard**, when the upstream computes the before-picture itself. Teable's `PUT /table/{tableId}/field/{fieldId}/plan` returns a calculation plan in one call, no pagination and no fan-out, so `plan` then `convert` with a `fail-when` over the plan is an ordinary two-step action. Refusal-before-damage beats recovery-after, and it needs none of what the grammar withholds.
- **A read-back guard** over an independent GET, the shape `action comment issue` already uses: an ordered call sequence plus `fail-when` on a response the write did not produce.

So before concluding a guard is impossible here, check whether the upstream offers a one-call answer. It often does, and the grammar's limit is not the API's.
