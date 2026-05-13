# passthrough example

Wraps the system `echo` as an audited urfave subcommand via `passthrough.Command`. Argv validation, audit logging, and exit-code propagation come for free.

```
$ go run ./examples/passthrough -- echo hello world
hello world
$ go run ./examples/passthrough -- echo 'rm; bad'
# rejected: argv contains a shell metacharacter
```
