# `doc-link` footer

A wrap block may carry one or more `doc-link` nodes. Each renders as a `## See also` bullet at the end of the generated reference doc, a back-pointer from that doc to a hand-written companion doc (typically the consumer's hub doc). The node is shared by both dialects: the exec-dialect [`execverb.Surface.Markdown`](execverb.md) and the spec-dialect [describe surface](specverb-describe.md).

```kdl
wrap ward-kdl ops forgejo {
    // ... spec/auth/grants ...
    doc-link "../ward-kdl.md" "ward-kdl.md" "the build-time authoring layer"
    doc-link "ward-kdl-surface.md"
}
```

Renders:

```markdown
## See also

- [ward-kdl.md](../ward-kdl.md) - the build-time authoring layer
- [ward-kdl-surface.md](ward-kdl-surface.md)
```

## Grammar

`doc-link "<href>" ["<text>" ["<desc>"]]`

- **`href`** (required) - the link target: a relative path or a URL.
- **`text`** (optional) - the link text. Defaults to `href` when omitted or empty.
- **`desc`** (optional) - a trailing description after ` - `.

The node gates nothing, so it composes with every surface shape: spec verbs, `exec` funnels, and `allow` inspect lists all carry the footer. The parser fails closed on a block body or more than three arguments.

## Why generated, not hand-written

A consumer's per-area reference doc is regenerated on every build (`specgen gen`/`build`), so a hand-added back-link at the bottom is wiped on the next regeneration. Emitting the footer from a guardfile node makes the back-link **generated** too: it survives every regeneration because it is re-emitted from the source. This is the enabler for the generated ward docs, whose reference pages could not point back to their `docs/ward-kdl.md` hub without it.

## See also

- [execverb.md](execverb.md) - the exec dialect and its grammar.
- [specverb-describe.md](specverb-describe.md) - the describe model and the reference-doc build.
