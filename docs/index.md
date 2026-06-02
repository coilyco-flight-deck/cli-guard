# cli-guard

cli-guard is a security-boundary framework for [urfave/cli](https://github.com/urfave/cli) v3 applications, designed to sit between AI agents (or any semi-trusted automation) and the host system.

It provides:

- argv validation rejecting shell metacharacters before they reach `execve`
- append-only JSONL audit log with lumberjack rotation
- read / write / delete scope tokens
- best-effort RepoRoot stamping that records each audit row's git toplevel (empty outside any repo)
- clean+synced gate refusing repo-shaped verbs on a dirty tree
- per-repo command allowlist
- thin pass-through wrapper for embedding existing CLIs as audited subcommands
- per-invocation CONNECT proxy with consumer-supplied egress allowlist
- public exit-code taxonomy for orchestrators

## Where to go next

- **[Features](FEATURES.md)** - feature inventory.
- **[Examples](examples.md)** - one runnable demo per primitive.
- **[CLI reference](https://coilysiren.github.io/cli-guard/cli/)** - rendered command tree for every example.
- **[Source on GitHub](https://github.com/coilysiren/cli-guard)** - issues, releases, code.

Sibling repo: [cli-mcp](https://github.com/coilysiren/cli-mcp).
