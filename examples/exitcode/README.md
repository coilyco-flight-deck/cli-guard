# exitcode example

Emits each member of the public exit-code taxonomy so orchestrators can pattern-match on `$?` without parsing stderr. `success=0`, `generic=1`, `policy_denied=2`, `upstream_failed=3`, `internal=4`, `user_error=5`.

```
$ go run ./examples/exitcode success ; echo "exit: $?"
exit: 0
$ go run ./examples/exitcode policy ; echo "exit: $?"
argv rejected
exit: 2
```
