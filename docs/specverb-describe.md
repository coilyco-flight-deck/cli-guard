# describe model + generated visibility

Gold-standard visibility for the fanatically thin generated CLI. The surface is spec-driven with no hand-written verbs, so its visibility is generated too. The engine never pulls descriptions it cannot trust from the sparse upstream spec; it surfaces the **structure** it always knows. See [specverb.md](specverb.md) for the engine itself.

## The Surface model

`specverb.Describe(Config)` builds a `Surface`: the in-engine model of the
mounted surface and the single structural truth shared by help and the describe
verb. It is assembled from the same resolved descriptors the runtime mounts, so
it can never name a verb that is not callable.

- **`Surface`** - the command path, resolved base-url, `AuthInfo`, and one `VerbInfo` per mounted leaf in mount order.
- **`VerbInfo`** - the CLI placement (noun -> leaf), the HTTP method/path, the destructive flag, the dotted audit name, the authorizing grant sentence, the optional `describe` note, `Params`, and the `FixedBody` a state-toggle leaf always sends.
- **`ParamInfo`** - each param tagged by kind (`path` positional, `query` flag, `body` flag) plus type (arrays render as `[]elem`) and requiredness. Query aliases also record their upstream parameter.
- **`AuthInfo`** - the scheme, header, and SSM token **path**. The secret value never appears in the model.

## The three consumer surfaces

The mounted command tree and its `Surface` model feed three readers:

- **Rich per-verb help.** Every mounted leaf's `--help` is populated with method/path, the authorizing grant, the `describe` note, each param tagged by kind and required/optional, and the dry-run hint. Always present, even where the upstream spec description is blank.
- **The `describe` verb.** `Build` mounts `describe` as a real verb on the group (e.g. `ward ops forgejo describe`). It renders `Surface.Markdown()` to stdout: a header and plain-language auth sentence, then a stanza per verb whose heading is the **full command path** and whose body frames the HTTP op, grant, and destructive flag in prose above two flat, aligned enumerations - **positional arguments** (the path slots) and **options** (the body flags), kept in separate lists. The verb takes no flags; capture elsewhere is a shell redirect.
- **The generated agent skill.** When the caller supplies `--skills-out`,
  specgen reconstructs the complete merged urfave tree and writes one concise
  `SKILL.md` plus `references/commands.yaml`. The body routes agents to live
  help and describe output. The lazy index records every reachable leaf without
  copying exhaustive help into eager context.

Machine consumers read the mounted command tree directly in Go rather than a
`--query` or `--output` rail on the verb.

## Guardfile `describe "..."` annotations

A grant may carry an optional `describe "..."` child node: a per-grant slot to enrich the thin upstream spec where it matters.

```kdl
can delete repos {
    describe "irreversible: deletes the repo and all its data"
}
```

The note flows into `Grant.Describe`, then into the verb's `--help` and describe
model, so a human can add the description Forgejo's sparse spec omits without
touching Go. The parser fails closed on any grant-body node other than
`describe`.

## Guardfile `doc-link` footer

A wrap block's `doc-link` nodes render as a `## See also` footer in pulled
describe output. See [doc-link.md](doc-link.md).

## Follow-ups (not silent gaps)

The `Surface` model remains the shared source for help, describe, shell
completions, and query-param mounting. Specgen's generated skill reads the
complete merged command tree so spec and exec members share one discovery
contract. Origin: the describe surface.
