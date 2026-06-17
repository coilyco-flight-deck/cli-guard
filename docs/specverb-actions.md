# complex actions

A **complex action** is a named composite verb authored inside a `wrap` block: it
orchestrates a bounded sequence of already-granted leaves with control flow. It
is sugar over the allowlist, never an escape from it. The first use case is a
native CI-watch verb (poll a CI run to completion, then surface failing jobs),
retiring the hand-written poll loop in ward's `scripts/watch-ci.sh`. See
[specverb.md](specverb.md) for the engine and cli-guard#140 for the design.

## The five invariants

1. **Granted-only.** An action may only `poll` an op the same Guardfile grants
   via `can`. An ungranted target fails at `lock`/`build` time, not runtime.
2. **Bounded.** Every poll loop carries a mandatory `every` and `timeout`. No
   unbounded iteration exists in the grammar - that is what makes it reviewable.
3. **Per-call audit.** Each poll tick writes its own leaf `verb.Wrap` audit row
   (named for the leaf, e.g. `ward.ops.forgejo.task.list`), and the action
   writes one envelope row (`...action.<name>`) tying them together.
4. **Dry-run is a plan.** `--dry-run` prints the call with its bound params and
   the compiled `until`, firing nothing.
5. **One expression engine.** Conditions are JMESPath, the same engine `--query`
   uses (`http/respfmt`), extended with native `$input` variables.

## Grammar

```kdl
wrap ward ops forgejo {
    spec forgejo.swagger.v1.json
    base-url "forgejo.coilysiren.me/api/v1"
    auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }

    can list tasks                       // the action may only poll a granted leaf

    action ci-watch {
        describe "Watch a CI run to completion, then surface failing-job status."
        input repo { positional; required; help "owner/name" }
        input run  { flag; help "run number" }

        poll list tasks {
            args { owner-repo $repo }    // owner-repo: splits owner/name across the two path params
            until """
                length([?run_number==$run && status!='success'
                        && status!='failure' && status!='cancelled'
                        && status!='skipped']) == `0`
                """
            every   "10s"                // durations are quoted: KDL rejects a bare 10s
            timeout "30m"
            as run_tasks
        }

        fail-when "length($run_tasks[?status=='failure']) > `0`"
    }
}
```

## Conditions and bindings

- `until` is evaluated each tick against the decoded response as its **root**
  (so `[?...]` filters the listing directly). Truthy ends the loop and binds the
  final response to `as`.
- Inputs reach conditions as native `$variables` through the jmespath-community
  interpreter's scope injection (`respfmt.Eval`), **not** string substitution -
  no injection surface. A numeric input is coerced so `run_number==$run`
  compares number to number. An unset optional flag binds in no scope, so a
  condition naming it fails closed rather than looping on a silent null.
- Author the multiline `until` as a KDL `"""..."""` string: the parser dedents
  it and JMESPath treats the newlines as whitespace, so it reads like formatted
  code, not a blob. Use a raw `#"""..."""#` only for a literal `"`.
- `fail-when` runs after the loop against the final response, with the `as`
  binding available as a `$variable`. Truthy is a non-zero exit - how a CI-watch
  tells the shell a job failed.

## Reserved for later (do not author in v1)

The grammar reserves, and fails closed on, the forward-design keywords so log
tailing is a later addition, not a rewrite: `emit`, `cursor`, `each`/`yield`,
and `call`/`read`/`follow`/`stream`/`tail`. The leaf seam is kind-agnostic: a
poll target may be a generated op now or a hand-written gated leaf later.
