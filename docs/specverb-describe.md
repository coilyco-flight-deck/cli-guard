# describe model + generated visibility

Gold-standard visibility for the fanatically thin generated CLI. The surface is spec-driven with no hand-written verbs, so its visibility is generated too. The engine never pulls descriptions it cannot trust from the sparse upstream spec; it surfaces the **structure** it always knows. See [specverb.md](specverb.md) for the engine itself.

## The Surface model

`specverb.Describe(Config)` builds a `Surface`: the in-engine model of the mounted surface, the single structural truth shared by help, the describe verb, and (later) completions and the generated skill. It is assembled from the same resolved descriptors the runtime mounts, so it can never name a verb that is not callable.

- **`Surface`** - the command path, resolved base-url, `AuthInfo`, and one `VerbInfo` per mounted leaf in mount order.
- **`VerbInfo`** - the CLI placement (noun -> leaf), the HTTP method/path, the destructive flag, the dotted audit name, the authorizing grant sentence, the optional `describe` note, `Params`, and the `FixedBody` a state-toggle leaf always sends.
- **`ParamInfo`** - each param tagged by kind (`path` positional, `query` flag, `body` flag) plus type (arrays render as `[]elem`) and requiredness.
- **`AuthInfo`** - the scheme, header, and SSM token **path**. The secret value never appears in the model.

## The three consumer surfaces

The `Surface` feeds one model into three readers, each `Surface.Markdown()` or a structural read of the same descriptors:

- **Rich per-verb help.** Every mounted leaf's `--help` is populated with method/path, the authorizing grant, the `describe` note, each param tagged by kind and required/optional, and the dry-run hint. Always present, even where the upstream spec description is blank.
- **The `describe` verb.** `Build` mounts `describe` as a real verb on the group (e.g. `ward ops forgejo describe`). It renders `Surface.Markdown()` to stdout: a header and plain-language auth sentence, then a stanza per verb whose heading is the **full command path** and whose body frames the HTTP op, grant, and destructive flag in prose above two flat, aligned enumerations - **positional arguments** (the path slots) and **options** (the body flags), kept in separate lists. The verb takes no flags; capture elsewhere is a shell redirect.
- **The committed reference doc.** At build time the driver (`specverb-gen gen`, and `lock`) writes the same `Surface.Markdown()` beside the Guardfile as `<name>.md`, in the pass that materializes `main.go` and the locks. That is the artifact the consumer commits and reviews in diffs - the binary, embedding its Guardfile, cannot recover the source path at runtime, so the doc is generated where the path is known.

Machine consumers (skills, completions) read the model directly through `specverb.Describe(Config)` in Go rather than a `--query`/`--output` rail on the verb.

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
