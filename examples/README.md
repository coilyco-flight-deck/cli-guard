# umbra examples

Each subdirectory is a self-contained urfave/cli app that exercises one feature of umbra end-to-end. Every example writes its audit rows somewhere under `$TMPDIR` so nothing pollutes the working directory.

| Example | Demonstrates |
| ------- | ------------ |
| [`audit/`](audit/main.go) | The foundation. `audit.NewWriter` + `verb.Wrap` produce one JSONL row per invocation. |
| [`policy/`](policy/main.go) | `policy.ValidateArgSlice` rejecting argv with shell metacharacters. |
| [`exitcode/`](exitcode/main.go) | The public exit-code taxonomy for orchestrators. |
| [`mcpverb/`](mcpverb/main.go) | The mcp dialect against an MCP server the example starts itself: a granted tool, a guarded argument, and two absent ones. |
| [`mcpverb-cli/`](mcpverb-cli/README.md) | The same dialect the product way: KDL policy plus a committed lock, no Go, over the published MCP reference server via stdio. |
| [`replacement/`](replacement/README.md) | An occluded replacement: no Go, one guardfile, and a `git` on PATH that is only what the guardfile grants. |
| [`mcpapps/`](mcpapps/main.go) | The MCP Apps host bridge: a widget's real frame sequence replayed against a live session, with the calls its `widget` block does not grant refused. |

Every feature is built on top of `audit`. The other examples wire it in implicitly via `verb.Wrap`; the `audit/` example is the bare-minimum case. `treebuilders/` is not a runnable example: it is a support package exporting each example's command tree for `scripts/gen-webdocs`. `mcpverb/` stays out of it, because its tree is built from a live server's tool surface rather than from a literal. `mcpverb-cli/` and `replacement/` have no Go at all: each is a `.umbra` project built by the driver.

## Running

From the umbra root:

```
go run ./examples/audit hello world
go run ./examples/policy unsafe 'foo; rm -rf /'
go run ./examples/exitcode policy ; echo "exit: $?"
go run ./examples/mcpverb ops demo list-issue --owner coilyco
go run ./examples/mcpverb ops demo list-issue --owner admin  # refused by the deny guard
go run ./examples/mcpapps                                   # the frame log, granted calls and refusals
```

`mcpverb-cli/` is built rather than `go run`, and needs `npx` on PATH for its
upstream. See [its README](mcpverb-cli/README.md). `replacement/` is installed
rather than built, onto a PATH directory of its own. See
[its README](replacement/README.md).

## Reading order

If you are new to umbra, read in this order:

1. `audit/` - the minimum useful program
2. `policy/` - what umbra refuses by default
3. `exitcode/` - the contract with orchestrators
4. `mcpverb/` then `mcpverb-cli/` - the same dialect in Go and then with no Go at all
5. `replacement/` - the same policy with the binary taken away
6. `mcpapps/` - the MCP Apps host under a widget grant (advanced)
