# Examples

Each subdirectory under [`examples/`](https://github.com/coilysiren/cli-guard/tree/main/examples) is a self-contained urfave/cli app that exercises one feature of cli-guard end-to-end. Every example writes its audit rows under `$TMPDIR`.

| Example | Demonstrates |
| ------- | ------------ |
| [`audit/`](https://github.com/coilysiren/cli-guard/tree/main/examples/audit) | The foundation. `audit.NewWriter` + `verb.Wrap` produce one JSONL row per invocation. |
| [`passthrough/`](https://github.com/coilysiren/cli-guard/tree/main/examples/passthrough) | Wrap an existing binary (`echo`) as an audited urfave subcommand via `passthrough.Command`. |
| [`policy/`](https://github.com/coilysiren/cli-guard/tree/main/examples/policy) | `policy.ValidateArgSlice` rejecting argv with shell metacharacters. |
| [`scope/`](https://github.com/coilysiren/cli-guard/tree/main/examples/scope) | `scope.Resolve` mapping `--commit-scope=auto` to a git toplevel. |
| [`gittree/`](https://github.com/coilysiren/cli-guard/tree/main/examples/gittree) | `gittree.CheckClean` refusing a verb on a dirty tree. |
| [`repocfg/`](https://github.com/coilysiren/cli-guard/tree/main/examples/repocfg) | Per-repo verb allowlist loaded from `.coily/coily.yaml`. |
| [`exitcode/`](https://github.com/coilysiren/cli-guard/tree/main/examples/exitcode) | Public exit-code taxonomy for orchestrators. |
| [`egress/`](https://github.com/coilysiren/cli-guard/tree/main/examples/egress) | Per-invocation CONNECT proxy with an allowlist (used by `passthrough.WithEgress`). |

## Running

From the cli-guard repo root:

```bash
go run ./examples/audit hello world
go run ./examples/passthrough -- echo hello
go run ./examples/policy unsafe 'foo; rm -rf /'
go run ./examples/exitcode policy ; echo "exit: $?"
```

## Reading order

If you are new to cli-guard:

1. **audit** - the minimum useful program.
2. **policy** - what cli-guard refuses by default.
3. **scope** - how audit rows bind to git history.
4. **passthrough** - the most common production usage.
5. **exitcode** - the contract with orchestrators.
6. **gittree** and **repocfg** - the repo-verb pattern.
7. **egress** - the network-layer gate (advanced).
