# fleetconfig - typed KDL fleet config

`pkg/fleetconfig` validates the fleet config: the agent-shaped schema naming which agents a fleet runs and how each launches. It is core data-typing, not a guarded surface.

## Schema

The embedded form is an `agents` block plus an optional `director` seed.

- `agents` - embed-only roster block. Exactly one in embedded sources.
  - `schema-version` - must equal `2`.
  - `defaults` - `agent` plus required `attribution name=... email=...`.
  - `agent <name>` - one roster entry, repeated, at least one required. Fields are descriptive, never grants: `binary`, `context-level` (`-1` unset), `stream`, `auth`, `model`, `endpoint`, `provider`, `reasoning-effort`, `verbosity`, and `argv { preflight; headless; interactive }`. Sparse top-level agent nodes may leave launch fields empty so a consumer can layer built-ins before validating completeness.
  - `roles` - optional per-role capability roster. Each `role <name>` may carry model-opaque `intent <name> { harness <name> }` defaults, names its guardfile set with repeated `guardfile "..."` children, and may add sparse per-agent display-identity and launch-knob overlays.
    - `intent <name> { harness <name> }` - ordered descriptive route default. Exactly one harness is required. It grants no execution and carries no model or backend data.
    - `agent <name>` - assigns optional `name` and `pronouns` display identity and retunes `model`, `endpoint`, `reasoning-effort`, and `verbosity`. Structural fields stay top-level only.
- `director` - per-host settings. `default-scope` seeds the coordinate scope.
- `description` - optional prose, both sources, empty fails.
- Legacy aliases remain accepted for now. `fleet` still parses as the embedded roster block, and `guardfiles` still parses as the role guardfile alias. They are temporary compatibility shims, not the primary syntax.

## One Parser

`Parse(src)` is `ParseSource(src, Embedded)`. `OperatorLocal` accepts only `director` and rejects the embedded roster block.

It fails closed on unknown nodes, wrong arguments, schema-version mismatch, or an embed-only block in operator-local source. A sparse embedded top-level `agent` node is accepted as data and can be completed by a consumer before validating the resulting roster.

## Why Core

The two guarded surfaces (`cli/`, `http/`) express permissions. `pkg/fleetconfig` does not. Its vocabulary has no `mount`, no `exec`, no `can run`. That package boundary is the config/permission partition, keeping the two-surface least-privilege identity legible. See [architecture.md](architecture.md).
