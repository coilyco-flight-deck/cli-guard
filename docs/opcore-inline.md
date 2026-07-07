# opcore inline-operation source (`ParseInline`)

`opcore.ParseInline` is the second descriptor source of the cli-free engine core.
Where the OpenAPI source resolves a `Descriptor` against a spec, the inline source
**states** it directly from KDL, so a non-CLI consumer (ward-mcp) never couples to
the CLI projection. Both sources feed the one `opcore.Descriptor` type, so every
downstream projection is source-blind. See [specverb.md](specverb.md) and cli-guard#196.

## Grammar

The frozen ward-mcp grammar, parsed with the same node-walking shape as
`guardfile`/`execverb`:

```kdl
wrap ward mcp forgejo {
    base-url "forgejo.coilysiren.me/api/v1"     // or a { value <provider> "..." } block
    auth header-token {                          // header-token | bearer | query-param
        header "Authorization"
        prefix "token "
        value env "FORGEJO_TOKEN"
    }
    restrict owner matches "coilyco-*" "kai"     // wrap-level allowlist, fail-closed

    can create issue {
        path "/repos/{owner}/{repo}/issues"      // required; path params inferred from {template}
        query "state"                            // -> query Fields (typed string)
        body "title" "body"                      // -> body Fields (typed string)
    }
    can close issue {
        path "/repos/{owner}/{repo}/issues/{index}"
        set state="closed"                       // -> FixedBody; no body flags mount alongside
    }
    can delete repo {
        path "/repos/{owner}/{repo}"             // delete is flagged Destructive
    }
}
```

## How each piece maps

* **method** - inferred from the verb via `opcore.MethodForVerb` (create→POST,
  get/list/view/search→GET, edit/close/reopen/archive→PATCH, delete/remove→DELETE,
  add→POST, set→PUT). No operationId, no spec resolution.
* **path params** - inferred from the `{template}` in author order via
  `opcore.PathParamsInOrder`.
* **query / body** - flat field-name lists promote to `Field{Type:"string"}`. An
  inline field carries no schema, so it types as a plain string.
* **set** - `set k=v...` becomes the leaf's `FixedBody`, keeping each value's
  KDL-native type (a boolean stays a boolean). A `set` toggle owns its body, so no
  body flags mount alongside it.
* **auth / base-url / restrict** - parsed by the shared `guardfile` node parsers
  (`ParseAuthNode`, `ParseBaseURL`, `ParseRestrictNode`) into the `RuntimeConfig`.

## Fail-closed and the shared guard

`ParseInline` fails closed on an unknown node, a missing `auth`, a missing `path`,
a malformed sentence, or zero operations. A promoted field that shadows a reserved
engine flag (`dry-run`, `query`, `output`, `body-file`) or another field on the
same leaf is rejected by `opcore.CheckFlagCollisions` - the same guard the resolved
source runs, so an inline descriptor fails closed exactly like a resolved one.

`Providers` and `Client` are the consumer's to fill on the returned `RuntimeConfig`
before `NewRuntime`; the KDL carries no opaque values.

## See also

- [specverb.md](specverb.md) - the resolved OpenAPI source and the CLI projection.
- [specverb-resolution.md](specverb-resolution.md) - the verb→method conventions `MethodForVerb` mirrors.
