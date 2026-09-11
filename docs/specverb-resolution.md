# op resolution, wildcards, and unrecognised verbs

A grant's verb+resource resolve to a spec operation by convention, so the author rarely hand-binds an operationId. A grant-body `op` is the override seam. The conventions are pure path+method structure, no vendor strings, so one resolver drives Swagger 2.0 and OpenAPI 3.x alike.

## Verbs

- **CRUD** - `get`/`view` (GET item), `list` (GET collection), `create` (POST collection), `edit` (PATCH then PUT), `delete` (DELETE item).
- **State toggles** - `close`/`reopen`/`archive`/`unarchive` resolve like `edit` and carry a fixed `body`. **Membership** - `add` (POST), `set` (PUT), `remove` (DELETE).
- **`search`**, **`list-<child>`**, **`create-on-<parent>`** - GET `<collection>/search`, GET that sub-collection, POST under `<parent>`.
- **`comment`** / **`pin`** - POST, named rather than reached through the fallthrough.
- **Any other verb** - its trailing noun is read as a child sub-collection to create on the resource (`transfer repo` -> `POST .../repos/{o}/{r}/transfers`).

## Resources

A resource may be a `parent-child` compound: `issue-label` targets the `labels` sub-collection under an `issue`. The resource segment is the trailing static segment, or the last before the trailing `{param}` run. Among matches, prefer a plural collection over a singleton, then the least-nested path.

With no path-structure candidate, the resolver matches verb and resource against the **words of each operationId**, reaching endpoints whose path does not name the resource. **Exact word-set beats superset**, so `search skills` -> `searchSkills` beats `aiSearchSkills` with no pin. Resolution is deny-by-default: zero candidates or a tie is a fail-closed error naming them, and that is when to pin `op`.

## Addressing a spec that names no operations

`operationId` is OPTIONAL in the OpenAPI Specification, RECOMMENDED rather than required, so a fully conformant document may omit it on every operation. Both addressing paths above need one: an explicit `op` has nothing to name, and the fallback tokenizes a string that does not exist. That locked out a whole class of valid upstream, not one vendor's quirk. Teable's self-hosted document is 634 paths and 756 operations with zero operationIds among them (umbra#6824).

The second form of `op` addresses the operation directly:

```kdl
can create field {
    op method="POST" path="/table/{tableId}/field"
}
```

Method plus path is unique within an OpenAPI document **by construction**, so this is a total addressing scheme rather than a heuristic, and it is the one form that needs nothing from the document beyond what the document must already have. The method is case-insensitive in the guardfile. The path is matched verbatim against the spec's path template, braces and all.

The two forms are exclusive: `op "<operationId>"` takes an argument, `op method= path=` takes both properties, and mixing them or supplying only one property fails closed at parse. Every existing operationId grant resolves exactly as before, and the convention fallback is untouched.

Prefer the operationId when the document has one. It survives an upstream re-pathing, which a route address does not.

## Unrecognised verbs

That fallthrough is the **one place the grammar infers rather than refuses**, and a wrong POST against a real endpoint may not fail loudly the way a wrong GET does. So the guess is not silent: `MethodForVerb` reports `ok=false`, the parser records `Descriptor.MethodInferred`, and `ParseInlineWithWarnings` returns one note per inferred grant. Guardfiles keep working. For a novel verb state `method "PUT"`.

## Wildcard resource `"*"`

`can get "*"` applies a verb across every resource exposing it and `never delete "*"` denies it everywhere, expanding at build and prune time into one grant per match. Only convention verbs enumerate, because `"*"` carries no `op` to break a tie. Any other fails closed.

Precedence is the ordinary deny-wins rule rather than a special case: a wildcard deny shadows a specific allow, a specific deny carves an exception out of a wildcard allow, and an explicit same-class grant wins rather than double-mounting. A wildcard mounts only ops the spec has, a new resource exposing `delete` is auto-denied with no edit, an empty expansion fails the build, and an ambiguous resource stays unmounted.
