# cli-guard

A security-boundary framework for [urfave/cli](https://github.com/urfave/cli) v3 applications.

cli-guard turns a urfave/cli command tree into an audited, scoped, argv-validated security boundary suitable for use as the bridge between AI agents (or any semi-trusted automation) and the host system. Extracted from [coilysiren/coily](https://github.com/coilysiren/coily), the personal ops CLI that originally hosted this code.

This package is one of four urfave/cli extensions intended for the urfave/cli ecosystem:

- **cli-guard** (this repo): scope tokens, audit log, lockdown writer, argv validation
- [cli-mcp](https://github.com/coilysiren/cli-mcp): project a urfave/cli command tree as an MCP server
- [cli-web-docs](https://github.com/coilysiren/cli-web-docs): static HTML documentation from a command tree
- [cli-web-ops](https://github.com/coilysiren/cli-web-ops): mobile-first web executor over Tailscale

All four are MIT-licensed and donation-ready.

## What's in the box

- `audit/` - append-only JSONL invocation log with lumberjack rotation
- `config/` - default + global + local config layering
- `exitcode/` - public exit-code taxonomy for orchestrators to pattern-match
- `gittree/` - clean+synced working-tree gate (verbs that mutate require a reconstructable history)
- `passthrough/` - thin wrapper for embedding existing CLIs (aws, gh, kubectl, ...) as audited subcommands
- `policy/` - shell-metachar argv validation
- `repocfg/` - per-repo command allowlist loaded from a configurable YAML file
- `scope/` - resolve a `--commit-scope` flag to a git toplevel for audit-row binding
- `shell/`, `ttlcache/`, `verb/`, `workdir/` - supporting utilities

## Usage

```go
import (
    "github.com/coilysiren/cli-guard/audit"
    "github.com/coilysiren/cli-guard/passthrough"
    "github.com/urfave/cli/v3"
)

// Wrap an existing CLI binary as an audited urfave subcommand:
cmd := passthrough.Command("aws", passthrough.Runner{
    Audit: audit.MustOpen("~/.cli-guard/audit"),
})
```

See `examples/demo/` for a tiny end-to-end CLI that wires every package together.

## Planned for v0.1

- `lockdown/` - generic permission-file writer with a `Driver` interface. Built-in: Claude Code. See [#2](https://github.com/coilysiren/cli-guard/issues/2).

## Status

v0. Extracted from coily on 2026-05-13. API will firm up under second-consumer pressure; pin a specific commit until v1.

## License

MIT. See [LICENSE](LICENSE).
