# fleetconfig - the typed KDL fleet config

`pkg/fleetconfig` is the typed validator for the **fleet config**: the
agent-shaped schema naming which agents a fleet runs and how each launches. It
is **core data-typing, not a guarded surface** (see [below](#why-core)).

## Schema

One KDL document. The embedded form carries a `fleet` block and an optional
`director` seed:

```kdl
fleet {
    schema-version 2
    defaults {
        agent claude
        attribution name="coilyco-ops" email="coilyco-ops@coilysiren.me"
    }
    agent codex {
        binary codex
        context-level 1
        stream "none"; auth "codex-file"; model "gpt-5.4-mini"
        reasoning-effort "low"; verbosity "low"
        argv { headless codex "exec"; interactive codex }
    }
    // ... claude, opencode, goose per the design
    roles {
        role engineer { }
        role advisor { guardfiles "ward-kdl.aws.guardfile.kdl" "ward-kdl.tailscale.guardfile.kdl" }
    }
}
director { default-scope "team" }
```

- **`fleet`** - the embed-only roster block. Exactly one, required in an
  embedded source.
  - **`schema-version`** - integer, must equal `2` (the current dialect); a
    mismatch fails closed.
  - **`defaults`** - `agent` (default when a caller names none) and
    `attribution name=… email=…` (both required).
  - **`agent <name>`** - one roster entry, repeated, at least one required. All
    fields are descriptive, never a permission: `binary` (required),
    `context-level` (integer; unset is `-1`, distinct from an explicit `0`),
    `stream`, `auth`, `model`, `endpoint`, `provider`, `reasoning-effort`,
    `verbosity`, and `argv { preflight; headless; interactive }` (each a full
    token list, binary first).
  - **`roles`** - optional per-role capability roster (ward#578): each
    `role <name>` names the guardfile set it holds (a flat list, or a single
    `prefix="..."`). Entries are descriptive names, never grants (see [below](#why-core)).
- **`director`** - per-host settings; `default-scope` seeds the coordinate
  scope. Accepted by both sources.

## One parser, two sources

One grammar, two accepted subsets, selected by a `Source`:

- **`Embedded`** - the config shipped inside a binary. Accepts the full schema
  (`fleet` plus an optional `director` seed). `Parse(src)` is
  `ParseSource(src, Embedded)`.
- **`OperatorLocal`** - a per-host operator file. Accepts only the narrow
  per-host node set (`director`) and **rejects the embed-only `fleet` block**.

```go
f, err := fleetconfig.Parse(embeddedBytes)                            // full schema
h, err := fleetconfig.ParseSource(perHost, fleetconfig.OperatorLocal) // director only
```

It fails closed throughout: an unknown node, wrong argument count or kind, a
schema-version mismatch, a missing required field, or an embed-only block in an
operator-local source is an error, never a silent default.

## Why core, not a fourth surface {#why-core}

The three guarded surfaces (`cli/`, `http/`, `mcp/`) express **permissions**;
`pkg/fleetconfig` deliberately does not. Its vocabulary has **no `mount`, no
`exec`, no `can run`** - those tokens are rejected by name - so it structurally
cannot express a grant. That package boundary **is** the config/permission
partition: config validation is a core concern, not a guarded surface, keeping
the three-surface least-privilege identity legible. See
[architecture.md](architecture.md).
