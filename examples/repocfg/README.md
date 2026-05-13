# repocfg example

Loads a per-repo verb allowlist from `.coily/coily.yaml`. Each declared verb is a pre-validated argv slice; runtime cannot turn it into a shell pipeline or add metacharacters.

```
$ cd examples/repocfg
$ go run . list
build: go build ./...
greet: echo hello world
$ go run . run greet
hello world
```
