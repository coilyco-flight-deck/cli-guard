# opcore typed query fields

Inline operations keep the historical flat query form unchanged and add a
typed block for consumers such as ward-mcp:

```kdl
query {
    field "limit" type="integer" minimum=1 maximum=100
    field "pinned" type="boolean"
    array "author_id" items="string" min-items=1 max-items=25
    field "before" type="string"
    field "after" type="string"
    field "around" type="string"
    mutually-exclusive "before" "after" "around"
}
```

## Grammar and schema

`field` accepts `string`, `boolean`, `integer`, or `number`. `array` accepts one
of those scalar types through `items`. Numeric bounds use inclusive `minimum`
and `maximum`. Array-length bounds use inclusive `min-items` and `max-items`.
Every field may set `required=true` and `upstream="wire"`.

`mutually-exclusive` declares a local-name group where callers may supply at
most one field. Every name must resolve to a query field on that operation.

The neutral draft-07 schema emits `minimum`, `maximum`, `minItems`, and
`maxItems`. Each at-most-one group becomes pairwise `allOf` entries containing
`not` plus `required`, so none or one of its fields may be present.

Unknown nodes, properties, and types fail closed. The parser also rejects
objects, nested values, duplicate names, non-finite bounds, negative array
bounds, impossible bound ranges, and unresolved exclusion names.

## Request values

`opcore.Args.Query` remains `map[string]string` for existing scalar callers.
`opcore.Args.QueryValues` carries typed scalars and scalar arrays. Supplying one
local name through both maps fails closed.

Request assembly enforces required fields, at-most-one groups, types, numeric
bounds, and array lengths before an upstream call. Array inputs encode as
repeated keys in input order. The URL policy gate validates every repeated
value independently.

## See also

* [opcore-inline.md](opcore-inline.md) - complete inline-operation grammar.
* [opcore-query-aliases.md](opcore-query-aliases.md) - local-to-wire names.
* [specverb-request.md](specverb-request.md) - request assembly and gating.
