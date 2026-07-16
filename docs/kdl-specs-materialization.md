# kdl-specs materialization

`run` and `build` materialize the generated consumer binary out-of-band. The consumer keeps policy and locks in source control, but never needs to commit generated Go files or build glue.

The materialized module lives under `config.CacheDir()`. It contains the generated `main.go`, the `go.mod` and `go.sum` replayed from `specverb.lock`, each member's embedded inputs, and the compiled binary.

The cache key is the generated binary name plus the sorted set of member paths. This keeps separate repos from colliding when they share a source `wrap <binary>` name, and lets one repo cache both its source build and a renamed `--binary <name>` build.

The `.stamp.json` records input hashes for the Guardfiles, spec locks, dependency lock, generator version, and version stamp. A rebuild fires only when one of those inputs changes or the compiled binary is missing.

`run` refuses without committed locks rather than silently locking. `lock` is the only online dependency-resolution step.

The consumer's source-of-truth build artifacts are the analog of `pyproject.toml` plus `uv.lock`: each `<spec>.lock.json` is a pruned API snapshot the binary embeds, and `specverb.lock` freezes the resolved Go dependency graph without making the consumer repo look like a Go module.

First `run` after a fresh `lock` works offline because `lock` already ran `go mod tidy` in the throwaway module and warmed the module cache.
