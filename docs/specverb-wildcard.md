# wildcard resource `"*"` (verb-global allow/deny)

A grant whose resource is the sentinel `"*"` applies its verb across **every**
resource in the spec that exposes it. One sentence replaces hand-enumerating the
whole spec, and the rule fail-closes onto resources the spec grows later.

```kdl
wrap ward-kdl ops forgejo {
    can get "*"          // allow get on every resource that exposes GET-item
    can list "*"         // allow list on every collection
    never delete "*"     // deny delete on every resource that exposes DELETE-item
}
```

## What it expands to

At build (and at prune) time the engine replaces each `"*"` grant with one
concrete grant per matching resource, then runs the ordinary pipeline. A resource
is a match when its `(verb, resource)` resolves unambiguously to one operation by
the same [convention](specverb-resolution.md) a hand-written grant uses:

- `can <verb> "*"` mounts the verb's leaf for every matching resource.
- `never <verb> "*"` / `cannot <verb> "*"` mounts a teaching deny leaf for every
  matching resource, carrying the wildcard's `message`/`describe` on each.

Only the **built-in convention verbs** enumerate under `"*"`: `get`/`view`,
`list`, `create`, `edit`, `delete`, the state toggles
(`close`/`reopen`/`archive`/`unarchive`), and the membership verbs
(`add`/`set`/`remove`). A wildcard over any other verb is a fail-closed error,
because `"*"` carries no `op` to pin a tie. The expanded resource name is the
spec's bare singular leaf (`org`, `issue`), so it string-matches a hand-written
`can <verb> <leaf>` and the precedence rules below compose unchanged.

## Precedence — deny still beats allow

Wildcards and specifics compose through the existing deny-wins rule
([`deny.go`](../http/specverb/deny.go)); nothing about precedence is special-cased:

- `never <verb> "*"` shadows a specific `can <verb> X` — the deny expands to cover
  `X`, so the allow is dropped and a teaching leaf stands in its place.
- A specific `never <verb> X` carves an exception out of `can <verb> "*"` — the
  wildcard expands to every resource *except* the one the specific deny names
  (allow-all-except-`X`).
- An **explicit** grant of the same deny-class + verb + resource wins over the
  wildcard: its authored `op`/`message`/`describe` stands, and the wildcard never
  double-mounts that leaf.

## Fail-closed posture

- **Default stays deny.** A `can <verb> "*"` only mounts ops that actually exist
  in the spec; an unlisted `(verb, resource)` is still not mounted
  ([`prune.go`](../http/specverb/prune.go)).
- **Future-proofing.** A new spec resource exposing `delete` is auto-denied by
  `never delete "*"` with no guardfile edit — the deny-by-structure direction of
  the local wildcard grant rule.
- **Empty expansion is an error.** A wildcard whose verb no resource exposes (a
  typo'd verb, or a verb the spec never declares) fails the build rather than
  mounting nothing silently.
- **Ambiguous resources are skipped.** A resource whose `(verb, resource)` is
  ambiguous by convention is left unmounted — exactly as a hand-written grant
  would fail closed, except the wildcard has no `op` to break the tie, so the
  author must add an explicit grant to reach it.

## Help and describe

Every expanded leaf names the verb-global grant that authorized it — `can get
"*"` — in its help and in the `describe` surface, so an operator reading one leaf
sees it exists because of a wildcard rule, not a per-resource line.

## Why

`readonly` becomes `can get "*"` + `can list "*"` (everything else unmounted =
denied); a no-delete `admin` build adds `never delete "*"`. Without wildcards each
build hand-lists every `(verb, resource)` across the whole spec and rots as the
spec grows. See the ward wildcard design note
(readonly/write/admin forgejo builds) and
the local wildcard design note.
