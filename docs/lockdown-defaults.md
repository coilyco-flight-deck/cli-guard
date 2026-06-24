# Lockdown default permission rules

Canonical Claude Code permission rules that cli-guard's lockdown writer emits into a repo's `.claude/settings.json`, embedded into the consumer binary at build time (`cli/lockdown/defaults.yaml`). Changes require editing the YAML, rebuilding, and reinstalling: the same review gate as any code change.

The deny list maps to the binaries the consumer wraps. Bare invocation is denied so the agent is forced through the wrapper - `ward pkg <pkgmgr>` for package managers, `ward-kdl ops <bin>` for privileged-op binaries - and the audit log plus argv validation become the system of record for every wrapped call. Tools the consumer does not wrap (helm, terraform, scripting interpreters, shells, Windows execution vectors) are not denied here: they fall back to Claude Code's auto-mode classifier, same as bare `rm`. Defense-in-depth at the prefix layer is not free - every entry is also rendered into the per-repo PreToolUse script, and an overbroad deny leaves dead allowlist entries piling up across the catalog. See cli-guard#13, #14.

## Allow

- `ward` itself - the kernel boundary.
- Read-only filesystem and search - `ls cat head tail wc file stat grep rg find tree`.
- Pure non-shell evaluators - `jq yq`. Same safety class as grep: argv is a query expression in the binary's own DSL, no shell-out.
- `git`, full surface. Destructive shapes are denied below; everything else is operator-trusted per Kai's git workflow (commit-to-main, no PRs unless asked).

## Deny

- Package managers (`npm pnpm yarn bun uv pip pipx poetry cargo gem bundle nix brew scoop`) - wrapped by `ward pkg <pkgmgr>`.
- Privileged-op binaries (`aws gh kubectl mcporter`) - wrapped by `ward-kdl ops <bin>`. `flyctl` and `gcloud` stay in the deny list but are dropped capabilities with no wrapper (not load-bearing in the ward / ward-kdl cutover).
- Top-level wrappers (`docker tailscale`) - `tailscale` is wrapped by `ward-kdl ops tailscale`; `docker` stays denied while general-use docker is retired.
- `ssh` - no wrapper (the SSH transport and the `ssh` passthrough were removed); bare ssh stays denied with no recovery path.
- git destructive shapes (`reset --hard`, `clean -fd`, `push --force`, `push -f`) - workflow-forbidden; force-push is off-limits, amend pre-push only.
- `git -c` config-injection RCE (cli-guard#84). `git -c <key>=<value>` can set a knob that runs a command (`core.sshCommand`, `core.fsmonitor`, `core.pager`, `alias.*=!sh`). This leading-token deny catches the top-level form at the CLI gate; the rendered hook adds a content scan for `--config-env` / `--exec-path=` and the positional `--upload-pack` / `--receive-pack` remote-exec flags. The `Bash(git:*)` allow would otherwise auto-approve all of these.
