# `doc-link` footer

A wrap block may carry one or more `doc-link` nodes. Each renders as a `## See
also` bullet at the end of pulled describe output, a back-pointer to a
hand-written companion doc. The node is shared by both dialects: the
exec-dialect [`execverb.Surface.Markdown`](execverb.md) and the spec-dialect
[describe surface](specverb-describe.md).

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

## Why it stays in policy

The generated CLI cannot recover a source-tree relationship from its binary
path. Keeping the link beside the policy lets every pulled describe render
reproduce the hand-written documentation relationship without making generated
Markdown a committed source.

## See also

- [execverb.md](execverb.md) - the exec dialect and its grammar.
- [specverb-describe.md](specverb-describe.md) - the describe model and generated skill.
