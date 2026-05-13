# audit example

The minimum useful cli-guard program. Wires `audit.NewWriter` with `verb.Wrap` so every invocation of the `hello` subcommand lands one JSONL row in `$TMPDIR/cli-guard-demo.jsonl`.

```
$ go run ./examples/audit hello world
hello, world
$ cat $TMPDIR/cli-guard-demo.jsonl | tail -1 | jq .verb
"hello"
```
