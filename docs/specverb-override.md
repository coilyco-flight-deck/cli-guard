# Precedence and `override` — fail-closed tiering

The load-bearing rule of tiered guardfiles (cli-guard#169), layered over
[`inherit`](specverb-inherit.md):

> **An inherited `never` beats a plain `can`. The only construct that beats an
> inherited `never` is an `override` in a higher tier naming the exact
> verb+resource.**

Posture: **deny low, override high.** The least-privileged base explicitly
`never`s the dangerous classes; those denials inherit upward; the trusted tier
re-grants specific ones by name.

## Wildcards stay safe

A higher tier may write `can delete "*"` and an inherited `never delete issue`
still carves `issue` out — that per-resource carve-out is denied silently, by
design (cli-guard#159). Deny is sticky upward; nothing but a named `override`
reverses it.

## The `override` keyword

```kdl
wrap ward-kdl ops forgejo {
    inherit "ward-kdl.forgejo.write.guardfile.kdl"   // inherits read's `never delete "*"`
    override can delete repo        // lifts the inherited deny for repo ONLY
    override can delete milestone
}
```

- `override can <verb> <resource>` is the **sole** escalation; it carries a `can`
  internally but is the only grant that survives an inherited deny.
- It re-grants **exactly** the named verb+resource — `"*"` is rejected, so every
  escalation a tier holds is enumerated and reviewable in that tier's guardfile.
  With `never delete "*"` in read and only `override can delete repo` in admin,
  `delete issue` stays denied at every tier.

## Build-time guardrails

The rule is enforced when guardfiles flatten, never silently at runtime:

- A **plain explicit** `can <verb> <resource>` shadowed by an **inherited**
  `never` is a build error pointing the author at `override` — an explicit allow
  is never silently swallowed by a parent's deny. A *wildcard* `can <verb> "*"`
  is exempt (its carve-outs are the safe path above).
- An `override` that lifts **no** matching `never` is a build error: silently it
  would be a plain `can`, the fail-open the keyword exists to stop.
- A same-tier `can` + `never` for one class keeps the older defense-in-depth
  behavior (deny wins, the `can` drops). The build error fires only across the
  inherit seam, where a dropped allow would be a surprise.

## Worked example (forgejo)

- **read**: the reads, plus `never delete "*"`, `never create label`, the other
  base denials. Deny-by-default made explicit at the most-exposed tier.
- **write**: inherits read, adds curated `create`/`edit`. The inherited
  `never create label` still beats a broad `can create "*"` — no override, so it
  stays denied.
- **admin**: inherits write, adds `override can delete repo`, `override can delete
  milestone`, `override can delete release` — and only those. `delete issue`,
  `delete org` are never overridden, so they stay denied at admin.
