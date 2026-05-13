# policy example

Two leaf subcommands exercise `policy.ValidateArgSlice`, the shell-metacharacter argv gate. Same gate that `verb.Wrap` and `passthrough.Command` install by default.

```
$ go run ./examples/policy safe hello
accepted: [hello]
$ go run ./examples/policy unsafe 'foo; rm -rf /'
rejected: positional[0]: shell metacharacter ';' refused
```
