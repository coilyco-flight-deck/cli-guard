# Upstream candidates for urfave/cli

Helpers in this repo that would plausibly belong in urfave/cli core, were the urfave team to want them. Not promoting yet (the four cli-* repos aren't all merged and proven), but documenting so the promotion path is visible.

## Candidates contributed by this repo

- **Argv-validation contract** (`policy/`). The shape "every token in argv must pass a metacharacter check before reaching the action" is generic. urfave could expose an `OnArgValidate(func([]string) error)` hook on `cli.Command` so middleware can validate without wrapping every action by hand.
- **Audited action wrapping** (`verb/`). The verb.Wrap pattern (pre-action validation, post-action audit) generalizes to any "middleware around action" use case. urfave has `Before` / `After`, but those are per-command. A tree-wide middleware chain would let cli-mcp and cli-web-ops compose with cli-guard for free.
- **Tool-passthrough builder** (`passthrough/`). The "wrap an existing binary as an audited subcommand" pattern is widely useful. urfave/cli has helpers for slurping flags, but not for a complete pass-through with audit.

## Candidates referenced by other cli-* repos

- **Tree walker** (`cli.Command.Walk(func(*cli.Command))`). Every cli-* extension reimplements the same recursive walk over `cmd.Commands`. A single helper would deduplicate.
- **Path of a command** (`cli.Command.Path() []string`). Used by all four cli-* repos for tool names, anchors, button URLs.

The shape: pin patterns here, copy into each consumer, watch what's stable across consumers, promote then.
