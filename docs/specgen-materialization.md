# specgen materialization

`run` and `build` materialize the generated consumer binary out-of-band. The consumer keeps policy and locks in source control, but never needs to commit generated Go files or build glue.

The materialized module lives under `config.CacheDir()`. It contains the generated `main.go`, the `go.mod` and `go.sum` replayed from `specverb.lock`, each member's embedded inputs, and the compiled binary.

The cache key is the generated binary name plus the sorted, root-relative member identities. This lets one project cache both its source build and a renamed `--binary <name>` build, and gives an identical project tree the same cache identity after it is moved to another absolute location.

The `.stamp.json` records input hashes for the root-relative Guardfile identities and bytes, decoded spec contracts, dependency lock, generator version, and version stamp. Per-member embeds and locks keep their relative directories in the materialized module, so members with identical basenames cannot overwrite one another. A rebuild fires only when one of those inputs changes or the compiled binary is missing.

`run` refuses without committed locks rather than silently locking. `lock` is the only online dependency-resolution step.

The consumer's source-of-truth build artifacts are the analog of `pyproject.toml` plus `uv.lock`: each `<spec>.lock.json.gz` is a generated, encoded pruned API snapshot. Specgen decodes it before materializing the plain JSON that the binary embeds. `specverb.lock` freezes the resolved Go dependency graph without making the consumer repo look like a Go module. Legacy plain `<spec>.lock.json` files remain readable until the next `lock` refresh replaces them.

The encoded API lock is machine-owned. Specgen decodes it before skew
comparison, reference generation, hashing, materialization, and embedding.
`specverb.lock` stays human-readable because its dependency graph remains useful
in review.

First `run` after a fresh `lock` works offline because `lock` already ran `go mod tidy` in the throwaway module and warmed the module cache.
