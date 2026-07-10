# `inherit` — compose guardfiles by merging grant sets

A wrap may carry one or more `inherit "<path>"` directives that pull in another
guardfile's grants, so a tiered surface (read ⊂ write ⊂ admin) composes by
layering instead of copy-pasting every sentence into each build.

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
flattened (recursively), its wrap body spliced into the inheriting wrap, and the
`inherit` directives dropped. The ordinary `resolve` / `prune` / `deny` pipeline
then operates on the merged set unchanged.

- **Grant union.** Child effective grants = parent grants ∪ child grants. Order
  does not matter: precedence is resolved by class, not position.
- **`never`/`cannot` inherit.** Deny-low is the posture: a base tier `never`s a
  dangerous class once and that denial holds at every higher tier unless a tier
  `override`s it — see [precedence](specverb-override.md).
- **`restrict` inherits**, deduped by param (a child restating the same param
  wins). This supersedes the earlier child-local shape, where every Forgejo tier
  restated `restrict owner matches "coily*"`; now the base declares it once.
- **Singletons** (`spec`, `base-url`, `auth`) are inherited only when the child
  declares none of its own — **the child wins**. Among parents, the first to
  supply a singleton wins.
- **Dedup.** A grant the child restates (same modal + verb + resource) collapses
  to one, keeping the child's body (`op` / `message` / `describe` / `body`).
- **`action` blocks stay child-local** and are *not* inherited.

## Precedence and escalation

An inherited `never` beats a plain `can`; only an `override` in a higher tier
crosses it. That rule, the `override` keyword, and the build-time guardrails live
in [specverb-override.md](specverb-override.md).

## Composes with wildcards

Inherited grants keep their wildcard flag, so a tier expressed as `can <verb>
"*"` expands per-resource through the merged set exactly as a hand-written
wildcard would. See [specverb-wildcard.md](specverb-wildcard.md).

## Fail-closed posture

- **Missing ref.** An `inherit` whose path does not resolve (relative to the
  inheriting file's directory) fails the load with a teaching error.
- **Cycle.** A chain that re-enters a file already being resolved fails with the
  offending chain spelled out, rather than recursing forever.
- **Unresolved at parse.** `guardfile.Parse` on source that still contains an
  `inherit` directive fails closed and points at `guardfile.ParseFile`.

## Why

ward builds three Forgejo binaries - read, write, admin - from one chain of
guardfiles instead of three hand-maintained copies. The read tier is the base;
write and admin layer their extra sentences on top.
