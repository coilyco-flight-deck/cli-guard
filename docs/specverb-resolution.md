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

## Path matching and disambiguation

For the lowered (method, shape, leaf, ancestors), the resource segment is the static segment naming the collection - the trailing static segment (collection) or the last static segment before the trailing `{param}` run (item). Among matches, the winner is chosen by: prefer a true **plural collection** segment over a singular singleton, then the **least-nested** path (the canonical resource lives at the shallowest depth; deeper paths are nested views).

## operationId fallback

When no path-structure candidate matches, the resolver matches the grant's verb and resource against the **words of each operationId** (camelCase / kebab / snake split). This reaches endpoints whose path does not name the resource: Tailscale's `get policy` resolves to `getPolicyFile` (path `/tailnet/{tailnet}/acl`) because the operationId words are `[get, policy, file]`. The same plural/least-nested disambiguation and fail-closed rule apply.

## Fail-closed

Resolution is deny-by-default. A unique winner resolves; **zero candidates or a remaining tie is a fail-closed error** that names the candidates and tells the author to pin one with `op`. The engine never silently guesses. Because disambiguation prefers shallow/plural paths rather than failing on every collision, a spec re-vendor that introduces a shallower colliding path can change a resolved op - the `lock` diff and skew check surface that for review.

## When to pin `op`

With the structural conventions plus the operationId fallback, `op` is rarely needed. Pin it only when resolution is genuinely ambiguous (the error names the colliding candidates) or when neither the path nor the operationId names the verb+resource.
