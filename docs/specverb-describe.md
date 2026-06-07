# describe model + generated visibility

Gold-standard visibility for the fanatically thin generated CLI. The surface is spec-driven with no hand-written verbs, so its visibility is generated too. The engine never pulls descriptions it cannot trust from the sparse upstream spec; it surfaces the **structure** it always knows. See [specverb.md](specverb.md) for the engine itself.

## The Surface model

`specverb.Describe(Config)` builds a `Surface`: the in-engine model of the mounted surface, the single structural truth shared by help, the describe verb, and (later) completions and the generated skill. It is assembled from the same resolved descriptors the runtime mounts, so it can never name a verb that is not callable.

- **`Surface`** - the command path, resolved base-url, `AuthInfo`, and one `VerbInfo` per mounted leaf in mount order.
- **`VerbInfo`** - the CLI placement (noun -> leaf), the HTTP method/path, the destructive flag, the dotted audit name, the authorizing grant sentence, the optional `describe` note, and `Params`.
- **`ParamInfo`** - each param tagged by kind (`path` positional, `body` flag) plus type and requiredness. The `Kind` taxonomy also names `query`, which the engine does not yet promote to a flag.
- **`AuthInfo`** - the scheme, header, and SSM token **path**. The secret value never appears in the model.

## The two consumer surfaces

- **Rich per-verb help.** Every mounted leaf's `--help` is populated with method/path, the authorizing grant, the `describe` note, each param tagged by kind and required/optional, and the dry-run hint. Always present, even where the upstream spec description is blank.
- **The `describe` verb.** `Build` mounts `describe` as a real verb on the group (e.g. `ward ops forgejo describe`), rendering the `Surface` through the same `--query`/`--output` rail as a live response - per the no-code direction, a driver verb, not a Makefile target.

## Guardfile `describe "..."` annotations

A grant may carry an optional `describe "..."` child node: a per-grant slot to enrich the thin upstream spec where it matters.

```kdl
can delete repos {
    describe "irreversible: deletes the repo and all its data"
}
```

The note flows into `Grant.Describe`, then into the verb's `--help`, the describe model, and (later) the generated skill, so a human can add the description Forgejo's sparse spec omits without touching Go. The parser fails closed on any grant-body node other than `describe`.

## Follow-ups (not silent gaps)

The `Surface` model is the shared source the next consumers read: generated markdown docs and a generated skill (both deferred from this visibility pass), shell completions, and query-param mounting. Origin: [cli-guard#104](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/104).
