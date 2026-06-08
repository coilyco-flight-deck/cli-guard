# Lockdown default permission rules

Canonical Claude Code permission rules that `coily lockdown` writes into a repo's `.claude/settings.json`, embedded into the coily binary at build time (`cli/lockdown/defaults.yaml`). Changes require editing the YAML, rebuilding, and sudo-installing: the same review gate as any code change.

The deny list maps 1:1 to the binaries coily wraps. Bare invocation is denied so the agent is forced through `coily pkg <pkgmgr>` / `coily ops <bin>` / `coily docker` / `coily tailscale`, and the audit log plus argv validation become the system of record for every wrapped call. Tools coily does not wrap (helm, terraform, scripting interpreters, shells, Windows execution vectors) are not denied here: they fall back to Claude Code's auto-mode classifier, same as bare `rm`. Defense-in-depth at the prefix layer is not free - every entry is also rendered into the per-repo PreToolUse script, and an overbroad deny leaves dead allowlist entries piling up across the catalog. See cli-guard#13, #14.

## Allow

- `coily` itself - the kernel boundary.
- Read-only filesystem and search - `ls cat head tail wc file stat grep rg find tree`.
- Pure non-shell evaluators - `jq yq`. Same safety class as grep: argv is a query expression in the binary's own DSL, no shell-out.
- `git`, full surface. Destructive shapes are denied below; everything else is operator-trusted per Kai's git workflow (commit-to-main, no PRs unless asked).

## Deny

- Package managers (`npm pnpm yarn bun uv pip pipx poetry cargo gem bundle nix brew scoop`) - wrapped by `coily pkg <pkgmgr>`.
- Privileged-op binaries (`aws gh kubectl flyctl gcloud mcporter`) - wrapped by `coily ops <bin>`.
- Top-level wrappers (`docker tailscale`) - each has a dedicated `coily <bin>` verb at the umbrella layer.
- `ssh` - no coily wrapper (the SSH transport and `coily ssh` passthrough were removed); bare ssh stays denied with no recovery path.
- git destructive shapes (`reset --hard`, `clean -fd`, `push --force`, `push -f`) - workflow-forbidden; force-push is off-limits, amend pre-push only.
- `git -c` config-injection RCE (cli-guard#84). `git -c <key>=<value>` can set a knob that runs a command (`core.sshCommand`, `core.fsmonitor`, `core.pager`, `alias.*=!sh`). This leading-token deny catches the top-level form at the CLI gate; the rendered hook adds a content scan for `--config-env` / `--exec-path=` and the positional `--upload-pack` / `--receive-pack` remote-exec flags. The `Bash(git:*)` allow would otherwise auto-approve all of these.
