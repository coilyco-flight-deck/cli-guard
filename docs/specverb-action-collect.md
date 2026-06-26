# collect actions: auto-pagination

A `collect` action walks a granted list leaf page by page, appending every array
response until a page returns fewer than the page size (the short-page stop),
then emits one accumulated JSON array bound to `as`. Like poll it is
granted-only, audited per page plus an envelope row, and dry-runnable. See
[specverb-actions.md](specverb-actions.md) for the shared invariants.

```kdl
action list-all issue {
    input owner { positional; required; help "repo owner" }
    input repo  { positional; required; help "repo name" }
    collect list issue {
        args { owner $owner; repo $repo }
        page-param    page
        limit-param   limit
        default-limit "50"
        as issues
        cache "10m"          // optional: serve from the on-disk TTL cache
    }
}
```

## `cache "<ttl>"` — TTL cache (collect only)

A heavy auto-paginated read can be served from an on-disk TTL cache instead of
re-fetching every invocation. The modifier is **collect-only** and fails closed
on `poll` (which needs live data) or `call` (which may mutate state): the parser
rejects `cache` anywhere else.

- **TTL** is a Go duration string (`"10m"`, `"1h"`); an unparseable or
  non-positive value fails the build, never silently disabling the cache.
- **Storage** is `config.CacheDir()` (`~/<app-dir>/cache`, overridable via the
  `<APP>_CACHE_DIR` env), reusing [`pkg/ttlcache`](../pkg/ttlcache).
- **Key** is the resolved request shape — method, base, path, and the sorted
  non-pagination query — hashed by `pkg/ttlcache`. Page and limit params are
  excluded (pagination, not identity), and auth lives in headers, so neither
  enters the key. The cache directory is treated as **strictly single-identity**:
  no per-identity partitioning, so a dir shared across credentials is the
  operator's responsibility.
- **Audit.** A cache hit fires no page requests and stamps `cache: "hit"` on the
  envelope audit row; a miss writes the per-page rows as usual.

Flags on a cache-eligible action:

- `--no-cache` bypasses the cache entirely (no read, no write).
- `--refresh` invalidates the entry and refetches, leaving a fresh entry.
- `--dry-run` prints the key, TTL, and directory without firing.
