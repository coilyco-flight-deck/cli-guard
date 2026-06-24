# execverb inspect lists (`allow`)

Sugar in the [execverb](execverb.md) dialect: `allow <bin...>` opens N read-only passthrough funnels from one wrap, instead of a separate `exec`/`can run "*"` wrap per binary.

```kdl
wrap ward-kdl inspect {
    allow ls cat head tail wc grep find stat jq yq \
          ps df du free uptime who id env which uname hostname dig host
    deny-when any-arg matches "*secret*" "*.key"
}
```

## Behavior

- Each listed binary mounts as its own leaf (`ward-kdl inspect grep`, `ward-kdl inspect cat`, ...), an independent `can run "*"` open passthrough to that binary. Its dotted audit name is the wrap group plus the binary (`ward-kdl.inspect.grep`).
- **Bare names only.** A path separator (`/`) or shell metacharacter fails closed, so the list can never name an arbitrary executable on disk or smuggle an injection. An empty list, a block body, or a repeated name is also a fail-closed parse error.
- **Mutually exclusive** with `exec` and `can run` in the same wrap: an `allow` wrap has no single binary, so those nodes have nothing to attach to.
- **Wrap-level guards apply to every funnel.** A `when` / `deny-when` declared directly under the wrap (not inside a grant) composes onto all generated leaves - the read-only floor over the whole list. A guard scoped to one binary belongs inside its own `exec`/`can run "*"` wrap instead; a wrap guard without an `allow` list fails closed.

## Desugaring

`allow grep cat` is mechanically `wrap ... grep { exec grep; can run "*" }` plus `wrap ... cat { exec cat; can run "*" }`, both carrying the wrap's guards and reusing the single-binary engine path. An inspect list is therefore exactly as safe as the hand-written wildcard wraps it stands in for.

## Tiering convention

- **Inspect tier** - read-only primitives (`grep`, `cat`, `ps`, `df`, `jq`) in one `allow` mega-list.
- **Host tier** - mutating OS primitives (`systemctl`, `launchctl`) keep one wrap each, so their grants stay explicit.
- **Excluded** - mixed read/write surfaces (`git`, `kubectl`, `aws`) stay declared `can run` verbs behind per-operation guards.

Design: [#157](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/157).
