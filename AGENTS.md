# Agent instructions

Workspace-level conventions (git workflow, test/lint autonomy, readonly ops, writing voice, deploy knowledge) are loaded globally via `~/.claude/CLAUDE.md` → `coilyco-ai/AGENTS.md`. This file covers only what's specific to this repo.

## What cli-guard is

A security-boundary framework for urfave/cli v3 applications. Extracted from [coilysiren/coily](https://github.com/coilysiren/coily)'s `pkg/` tree; treat the framework primitives here as the load-bearing core that coily and any future consumer depend on. Inventory: [`docs/FEATURES.md`](docs/FEATURES.md). Per-feature demos: [`examples/`](examples/).

## Dev verbs

Route through [agent-guard](https://github.com/coilysiren/agent-guard), not bare go. agent-guard is the generic-purpose cli-guard consumer; external contributors install it via `brew install coilysiren/tap/agent-guard`. The `.agent-guard/agent-guard.yaml` ↔ `Makefile` contract is checked on every CI run via `agent-guard lint`:

- `agent-guard exec build` - compile every package.
- `agent-guard exec test` - run the unit test suite.
- `agent-guard exec lint` - golangci-lint v2.12.2 with the urfave-mirrored `.golangci.yaml`.
- `agent-guard exec vet` - `go vet ./...`.
- `agent-guard exec tidy` - `go mod tidy`.
- `agent-guard exec cover` - tests with a coverage profile.

cli-guard itself doesn't care which filename: any consumer that wraps the repocfg primitive can pick its own (`.agent-guard/agent-guard.yaml` here, `.coily/coily.yaml` in Kai-internal repos, etc.).

## No coily-types in pkg-shaped code

Carry forward the rule from coily's `pkg/README.md`: every package here must be importable from a different binary without coily-specific types or defaults leaking in. If a helper needs a coily-shaped argument, define the type in cli-guard and have the consumer adapt to it, not the other way around. This is the property that makes the future "coily imports cli-guard" migration mechanical rather than archaeological.

## v0 API discipline

v0.x. Minor API breaks ship in `main` with a note in the commit body; no semver deprecation cycle until v1.0.0. Consumers (coily, future others) pin a specific commit until then. Once a second consumer lands, lock the API and bump.

## Lockdown is deferred

The Claude Code lockdown writer in `coily/pkg/lockdown` did not come over. Generalizing it via a `Driver` interface is tracked at [#2](https://github.com/coilysiren/cli-guard/issues/2). Until then, coily keeps its own lockdown package; do not reintroduce the Claude-specific paths here without the driver refactor.

## Filing issues

One issue per discrete additive change, per [the workspace rule](https://github.com/coilysiren/coilyco-ai/blob/main/AGENTS.md). Every commit closes a same-repo issue with `closes #N`.
