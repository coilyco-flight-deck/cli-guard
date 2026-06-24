# `inherit` — compose guardfiles by merging grant sets

A wrap may carry one or more `inherit "<path>"` directives that pull in another
guardfile's grants, so a tiered surface (read ⊂ write ⊂ admin) composes by
layering instead of copy-pasting every `can` sentence into each build.

```kdl
wrap ward-kdl ops forgejo {
    inherit "ward-kdl.forgejo.read.guardfile.kdl"
    can create "*"
    can edit "*"
}
```

The chain composes: `admin` inherits `write`, which inherits `read`. Each
referenced file is itself a complete, buildable guardfile.

## What it merges

Resolution is **textual** and runs before the typed parse: each referenced file is
flattened (recursively, so its own `inherit` resolves first), its wrap body is
spliced into the inheriting wrap, and the `inherit` directives are dropped. The
ordinary `resolve` / `prune` / `deny` pipeline then operates on the merged set
unchanged.

- **Grant union.** Child effective grants = parent grants ∪ child grants. Order
  does not matter to correctness: precedence is resolved by class, not position.
- **Singletons** (`spec`, `base-url`, `auth`) are inherited only when the child
  declares none of its own — **the child wins**. Among multiple parents, the
  first to supply a singleton wins.
- **Dedup.** A grant the child restates (same modal + verb + resource) collapses
  to one, keeping the child's body (`op` / `message` / `describe` / `body`), so a
  child can refine an inherited grant without double-mounting its leaf.
- **`restrict` and `action` blocks are child-local** and are *not* inherited; a
  tier that needs them declares them itself. The directive merges grant sets, not
  whole policies.

## Precedence — deny still beats allow

The merged set runs through the existing deny-wins rule
([`deny.go`](../http/specverb/deny.go)); inheritance special-cases nothing. In the
additive tier model parents carry only `can` grants, so no deny-fighting arises. A
child does **not** re-allow a parent `never`: a deny anywhere in the merged set
beats an allow. (Re-opening a denied class is deliberately out of scope; if it is
ever needed it is a separate feature, not a property of `inherit`.)

## Composes with wildcards (#159)

Inherited grants keep their wildcard flag, so a tier expressed as `can <verb>
"*"` expands per-resource through the merged set exactly as a hand-written
wildcard would — read tiers are `can get "*"` + `can list "*"`, write adds `can
create "*"` + `can edit "*"`, and the union surface mounts every leaf each tier
authorizes. See [specverb-wildcard.md](specverb-wildcard.md).

## Fail-closed posture

- **Missing ref.** An `inherit` whose path does not resolve (relative to the
  inheriting file's directory) fails the load with a teaching error rather than
  silently dropping the layer.
- **Cycle.** A chain that re-enters a file already being resolved fails with the
  offending chain spelled out, rather than recursing forever.
- **Unresolved at parse.** `guardfile.Parse` on source that still contains an
  `inherit` directive fails closed and points at `guardfile.ParseFile`; only the
  file-aware path can resolve a relative reference.

## Why

ward#240 builds three forgejo binaries — read, write, admin — from one chain of
guardfiles instead of three hand-maintained copies of the same grant list. The
read tier is the base; write and admin layer their extra `can` sentences on top.
See [ward#240](https://forgejo.coilysiren.me/coilyco-bridge/ward/issues/240) and
[cli-guard#160](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/160).
