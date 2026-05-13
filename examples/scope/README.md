# scope example

`scope.Resolve` maps `--commit-scope=auto` to a git toplevel so every audit row binds to a reconstructable commit. Run from inside a checkout vs outside one to see the two paths.

```
$ cd /path/to/some/repo && go run /path/to/cli-guard/examples/scope where
scope: /path/to/some/repo
$ cd /tmp && go run /path/to/cli-guard/examples/scope where
error: scope: cwd is not inside a git repo
```
