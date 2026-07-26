# opcore query aliases

Inline operations can expose a safe local input name when an upstream HTTP API
requires a query parameter that collides with an opcore engine flag.

```kdl
can search card {
    path "/search"
    query "search_query" upstream="query"
}
```

The MCP or CLI surface exposes `search_query`. Request assembly sends its value
as the upstream `query` parameter. The neutral schema keeps the local property
and annotates it with `x-opcore-upstream-name`. Generated references show both
names.

Typed query blocks use the same mapping on a field:

```kdl
query {
    field "search_query" type="string" upstream="query"
}
```

## Boundary

Aliases change only the outgoing query name:

* The local name still cannot shadow `dry-run`, `query`, `output`, or `body-file`.
* An unaliased collision still fails closed.
* Two local names cannot map to the same outgoing parameter.
* One alias declaration maps exactly one local name.
* Body and form fields cannot declare upstream names.
* Supplying both the local and outgoing names to `Operation` fails closed.
* Mutual-exclusion declarations always name the local inputs.

These checks keep response projection under the engine-owned `query` input while
still allowing an HTTP API to receive a parameter literally named `query`.

## See also

* [opcore-inline.md](opcore-inline.md) - complete inline-operation grammar.
* [specverb-request.md](specverb-request.md) - request assembly and gating.
