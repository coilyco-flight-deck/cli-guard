# opcore inline-operation source (`ParseInline`)
`opcore.ParseInline` states descriptors directly from KDL for non-CLI consumers
such as ward-mcp. It feeds the same source-blind core as OpenAPI resolution.
## Grammar

The frozen ward-mcp grammar follows the `guardfile`/`execverb` node shape:

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
        fail-when "number == null"
    }
    can query issue {
        path "/query"
        body {
            field "start" type="integer" required=true
            field "requestType" type="string" required=true
            object "variables" raw=true
            object "compositeQuery" raw=true required=true
        }
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

* **method** - inferred through `opcore.MethodForVerb`, without operationId
  resolution.
* **path params** - inferred from the `{template}` in author order via
  `opcore.PathParamsInOrder`.
* **query / body** - flat names become string fields. Blocks add typed, nested,
  bounded, repeatable, aliased, or mutually exclusive fields. See
  [query types](opcore-query-types.md) and [aliases](opcore-query-aliases.md).
* **set** - `set k=v...` becomes the leaf's `FixedBody`, keeping each value's
  KDL-native type (a boolean stays a boolean). A `set` toggle owns its body, so no
  body flags mount alongside it.
* **fail-when** - a JMESPath predicate over a successful response. A truthy result
  fails the call. Request inputs are available as native `$name` variables.
* **proxy** - guarded upstream MCP passthroughs live in
  [opcore-proxy.md](opcore-proxy.md).
* **auth / base-url / restrict** - parsed by the shared `guardfile` node parsers
  (`ParseAuthNode`, `ParseBaseURL`, `ParseRestrictNode`) into the `RuntimeConfig`.
## Fail-closed and the shared guard

`ParseInline` rejects unknown nodes, missing requirements, malformed predicates,
and input collisions through the same guard as resolved descriptors.
`Providers` and `Client` are the consumer's to fill on the returned `RuntimeConfig`
before `NewRuntime`; the KDL carries no opaque values.
## See also

- [specverb.md](specverb.md) - the resolved OpenAPI source and the CLI projection.
- [specverb-resolution.md](specverb-resolution.md) - the verb→method conventions `MethodForVerb` mirrors.
- [opcore-query-aliases.md](opcore-query-aliases.md) - safe local names for colliding upstream query parameters.
