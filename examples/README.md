# cli-guard examples

Each subdirectory is a self-contained urfave/cli app that exercises one feature of cli-guard end-to-end. Every example writes its audit rows somewhere under `$TMPDIR` so nothing pollutes the working directory.

| Example | Demonstrates |
| ------- | ------------ |
| [`audit/`](audit/main.go) | The foundation. `audit.NewWriter` + `verb.Wrap` produce one JSONL row per invocation. |
| [`passthrough/`](passthrough/main.go) | Wrap an existing binary (`echo`) as an audited urfave subcommand via `passthrough.Command`. |
| [`policy/`](policy/main.go) | `policy.ValidateArgSlice` rejecting argv with shell metacharacters. |
| [`gittree/`](gittree/main.go) | `gittree.CheckClean` refusing a verb on a dirty tree. |
| [`repocfg/`](repocfg/main.go) | Per-repo verb allowlist loaded from `.coily/coily.yaml`. |
| [`exitcode/`](exitcode/main.go) | The public exit-code taxonomy for orchestrators. |
| [`egress/`](egress/main.go) | Per-invocation CONNECT proxy with an allowlist (used by `passthrough.WithEgress`). |
| [`dispatch/`](dispatch/main.go) | Wire the cli-guard dispatch subsystem into a host CLI (fires `claude` at a real open issue). |

Every feature is built on top of `audit`. The other examples wire `audit` in implicitly via `verb.Wrap` or `passthrough.Command`; the `audit/` example is the bare-minimum case. `treebuilders/` is not a runnable example: it is a support package exporting each example's command tree for `scripts/gen-webdocs`.

## Running

From the cli-guard root:

```
go run ./examples/audit hello world
go run ./examples/passthrough -- echo hello
go run ./examples/policy unsafe 'foo; rm -rf /'
go run ./examples/gittree build
cd examples/repocfg && go run . list && cd -
go run ./examples/exitcode policy ; echo "exit: $?"
go run ./examples/egress allowed
```

## Reading order

If you are new to cli-guard, read in this order:

1. `audit/` - the minimum useful program
2. `policy/` - what cli-guard refuses by default
3. `passthrough/` - the most common production usage
4. `exitcode/` - the contract with orchestrators
5. `gittree/` and `repocfg/` - the repo-verb pattern
6. `egress/` - the network-layer gate (advanced)
