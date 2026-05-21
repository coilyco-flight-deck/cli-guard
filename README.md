# cli-guard

[![Go Reference][goreference_badge]][goreference_link]
[![Go Report Card][goreportcard_badge]][goreportcard_link]
[![Tests status][test_badge]][test_link]

cli-guard is a **security-boundary** framework for [urfave/cli][urfave/cli] v3 applications, designed to sit between AI agents (or any semi-trusted automation) and the host system, featuring:

- argv validation rejecting shell metacharacters before they reach `execve`
- append-only JSONL audit log with lumberjack rotation
- read / write / delete scope tokens, validated per verb
- `--commit-scope` resolution binding every audit row to a git toplevel
- clean+synced gate refusing repo-shaped verbs on a dirty tree
- per-repo command allowlist loaded from per-repo YAML config files (e.g. `.agent-guard/agent-guard.yaml`, `.coily/coily.yaml`)
- thin pass-through wrapper for embedding existing CLIs as audited subcommands
- per-invocation CONNECT proxy with consumer-supplied egress allowlist
- public exit-code taxonomy for orchestrators
- reusable dispatch subsystem firing `claude` against a real open issue, headless or interactive

## Documentation

See [`docs/FEATURES.md`](docs/FEATURES.md) for a feature inventory, [`examples/`](examples/) for runnable demos one per primitive, and the [CLI reference](https://coilysiren.github.io/cli-guard/cli/) for the rendered command tree of every example. Local dev verbs live in [`.agent-guard/agent-guard.yaml`](.agent-guard/agent-guard.yaml); `agent-guard lint` validates that against the [`Makefile`](Makefile).

## Support

If you found a bug or have a feature request, [create a new issue]. Participation in this community is governed by the [Code of Conduct](CODE_OF_CONDUCT.md). Security disclosures go through [SECURITY.md](SECURITY.md).

Sibling repos in the cli-* family: [cli-mcp], [cli-web-docs], [cli-web-ops].

### License

See [`LICENSE`](./LICENSE).

[test_badge]: https://github.com/coilysiren/cli-guard/actions/workflows/ci.yml/badge.svg
[test_link]: https://github.com/coilysiren/cli-guard/actions/workflows/ci.yml
[goreference_badge]: https://pkg.go.dev/badge/github.com/coilysiren/cli-guard.svg
[goreference_link]: https://pkg.go.dev/github.com/coilysiren/cli-guard
[goreportcard_badge]: https://goreportcard.com/badge/github.com/coilysiren/cli-guard
[goreportcard_link]: https://goreportcard.com/report/github.com/coilysiren/cli-guard
[urfave/cli]: https://github.com/urfave/cli
[create a new issue]: https://github.com/coilysiren/cli-guard/issues/new/choose
[cli-mcp]: https://github.com/coilysiren/cli-mcp
[cli-web-docs]: https://github.com/coilysiren/cli-web-docs
[cli-web-ops]: https://github.com/coilysiren/cli-web-ops

## See also

- [AGENTS.md](AGENTS.md) - agent-facing operating rules.
- [docs/FEATURES.md](docs/FEATURES.md) - inventory of what ships today.
- [.agent-guard/agent-guard.yaml](.agent-guard/agent-guard.yaml) - allowlisted commands.

Cross-reference convention from [coilysiren/agentic-os#59](https://github.com/coilysiren/agentic-os/issues/59).
