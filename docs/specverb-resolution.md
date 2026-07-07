# op resolution (L1)

A grant's verb+resource resolve to a spec operation by generic convention, so the author rarely hand-binds an operationId. An explicit grant-body `op "<operationId>"` is the override seam. `specverb.resolveOp` is the single bridge, called from the engine (`resolveDescriptor`) and the pruner (`grantedPathMethods`). The conventions are pure path+method structure - no vendor strings - so one resolver drives Swagger 2.0 and OpenAPI 3.x alike.

## Verbs

- **CRUD** - `get`/`view` (GET item), `list` (GET collection), `create` (POST collection), `edit` (PATCH then PUT, item), `delete` (DELETE item).
- **State toggles** - `close`/`reopen`/`archive`/`unarchive` resolve like `edit` (PATCH/PUT item) and carry a fixed `body`.
- **Membership** - `add` (POST collection), `set` (PUT collection), `remove` (DELETE item) - attach/replace/detach an element of a sub-collection.
- **`search`** - GET a path ending in `<resource-collection>/search` (e.g. `search issue` -> `GET /repos/{o}/{r}/issues/search`).
- **`list-<child>`** - GET the named sub-collection of the resource (`list-cards board` -> `GET /boards/{id}/cards`).
- **`create-on-<parent>`** - POST the resource collection nested under `<parent>` (`create-on-board list` -> `POST /boards/{id}/lists`).
- **Any other verb** - its trailing noun is read as a child sub-collection to create on the resource (`comment issue` -> `POST .../issues/{i}/comments`).

## Resources

A resource may be a `parent-child` compound: `issue-label` targets the `labels` sub-collection nested under an `issue`. Each ancestor must appear, in order, as a static segment before the leaf's resource segment.

A resource may also be the wildcard sentinel `"*"`: `can <verb> "*"` / `never <verb> "*"` applies the verb across every resource the spec exposes for it, expanded into a concrete per-resource grant before resolution. See [specverb-wildcard.md](specverb-wildcard.md).

## Path matching and disambiguation

For the lowered (method, shape, leaf, ancestors), the resource segment is the static segment naming the collection - the trailing static segment (collection) or the last static segment before the trailing `{param}` run (item). Among matches, the winner is chosen by: prefer a true **plural collection** segment over a singular singleton, then the **least-nested** path (the canonical resource lives at the shallowest depth).

## operationId fallback

When no path-structure candidate matches, the resolver matches the grant's verb and resource against the **words of each operationId** (camelCase / kebab / snake split). This reaches endpoints whose path does not name the resource: `get policy` -> `getPolicyFile` (path `/tailnet/{tailnet}/acl`, words `[get, policy, file]`). The verb is split into words too, so `ai-search` matches `aiSearchSkills`, and `search` falls through here when the spec's search endpoint has no `<resource>/search` segment (skillsmp `GET /search` -> `searchSkills`).

**Exact word-set beats superset.** When one operationId's words are set-equal to the grant (`search skills` -> `searchSkills`) and others only contain them as a superset (`aiSearchSkills`), the exact match wins and supersets drop out - no `op` pin needed. A genuine tie (two exact, or two equal supersets) still fails closed.

## Fail-closed

Resolution is deny-by-default. A unique winner resolves; **zero candidates or a remaining tie is a fail-closed error** that names the candidates and tells the author to pin one with `op`. The engine never silently guesses. Because disambiguation prefers shallow/plural paths, a re-vendor that adds a shallower colliding path can change a resolved op - the `lock` diff and skew check surface that for review.

## When to pin `op`

With the structural conventions plus the operationId fallback, `op` is rarely needed. Pin it only when resolution is genuinely ambiguous (the error names the candidates) or neither the path nor operationId names the verb+resource.
