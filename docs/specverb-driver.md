# the no-code driver (specdrv / cmd/specverb-gen)

`ward-kdl` is a **no-code** CLI: the consumer authors only policy plus its committed locks, never Go or build glue. `specverb-gen` is the uv-style driver that makes that real - the `uv run` / `uv lock` model, not make targets. See [specverb.md](specverb.md) for the engine the driver wraps.

## The four verbs

Every verb reads a `--guardfile`, defaulting to the lone `*.guardfile.kdl` in the current directory.

- **`gen`** - render `main.go` into the cache (or `--out` to inspect it). A debug step; `run` does it for you.
- **`lock`** - the deliberate online step. Fetches the upstream Swagger to the committed spec lock (`<spec>.lock.json`), then resolves the build's module graph (`go mod tidy` in a throwaway module) and freezes it into `specverb.lock`. `--cli-guard-ref` pins the framework version (defaults to the driver's own); `--cli-guard-replace` points at a local checkout for development.
- **`skew`** - diff the committed spec lock against live upstream at the operation level (added / removed / changed paths and definitions). Exit 3 on drift, never writes; a fetch failure is a plain error, so offline is distinguishable from drift.
- **`run`** - materialize the consumer binary **out-of-band** and exec it with the passed-through args.

## Out-of-band materialization

`run`'s generated `main.go`, the build's `go.mod`/`go.sum` (replayed from `specverb.lock`), the embed inputs, and the compiled binary all live in a cache under `config.CacheDir()`, keyed by the Guardfile's path - never in the consumer repo, the analog of a venv. A `.stamp.json` of input hashes makes the common path a no-op exec; a rebuild fires only when the Guardfile, either lock, or the driver version changed. `run` refuses to run without committed locks rather than silently locking, keeping the network step explicit.

## The two locks

The consumer's only source-of-truth build artifacts, the analog of `pyproject.toml` + `uv.lock`:

- **`<spec>.lock.json`** - the embedded upstream API snapshot the binary mounts from.
- **`specverb.lock`** - a dedicated JSON (not a `go.mod` in the repo, which would make the consumer look like a Go module) freezing the resolved dependency graph for a reproducible offline build.

First `run` after a fresh `lock` works offline because `lock`'s `go mod tidy` already warmed the module cache.

Origin: [cli-guard#106](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/106).
