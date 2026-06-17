# op resolution (L1)

A grant's verb+resource resolve to a spec operation by generic REST convention, so the author does not hand-bind an operationId for the regular cases. An explicit grant-body `op "<operationId>"` is the override seam, not a required binding. `specverb.resolveOp` is the single bridge, called from both the engine (`resolveDescriptor`) and the pruner (`grantedPathMethods`).

## The convention

The canonical CRUD verbs carry a built-in (HTTP method, path shape):

- `get`, `view` - GET, item
- `list` - GET, collection
- `create` - POST, collection
- `edit` - PATCH then PUT fallback, item
- `delete` - DELETE, item

A candidate operation matches when its HTTP method equals the verb's method AND the path's **resource segment** singular-matches the grant's resource. The resource segment is the static path segment that names the collection: for a collection path the trailing static segment (`/repos/{owner}/{repo}/issues` -> `issues`), for an item path the last static segment before the trailing run of `{param}` tokens (`/repos/{owner}/{repo}` -> `repos`). `edit` prefers PATCH (JSON-merge APIs like Forgejo) and falls back to PUT (whole-replace APIs like Trello).

The match is spec-agnostic: only path structure and method, no vendor strings in engine code. The OpenAPI-3 reader lowers into the same internal model, so one resolver drives Swagger 2.0 and OpenAPI 3.x alike.

## Fail-closed

Resolution is deny-by-default. A **unique** candidate resolves. **Zero** candidates, **multiple** candidates, or a **non-CRUD verb** (`comment`, `search`, `close`, `add`, `set`, `remove`, ...) is a fail-closed error that names the candidates and tells the author to pin one with `op`. The engine never silently guesses, and never re-points a grant to a different operation on a spec re-vendor - a newly-ambiguous grant breaks the build instead.

## When to pin `op`

Pin `op` for irregular endpoints convention cannot reach: search endpoints (`list repo` -> `repoSearch` at `GET /repos/search`), sub-resource verbs (`comment issue` -> `issueCreateComment`), fixed-body state toggles (`close issue { op "issueEditIssue"; body state="closed" }`), and cases where two operations legitimately share a method+resource (the resolver lists both in the error).
