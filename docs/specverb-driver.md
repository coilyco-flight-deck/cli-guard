# the no-code driver (specdrv / cmd/specverb-gen)

`ward-kdl` is a **no-code** CLI: the consumer authors only policy plus its committed locks, never Go or build glue. `specverb-gen` is the uv-style driver that makes that real - the `uv run` / `uv lock` model, not make targets. See [specverb.md](specverb.md) for the engine the driver wraps.

## Discovery and merging

A `--guardfile` selects a **binary**, not the whole build: the driver reads every `*.guardfile.kdl` in that file's directory sharing its `wrap <binary>` name (`Group[0]`) and merges them, each mounting as its own command group. With no `--guardfile`, the cwd must resolve to one binary, else the driver lists them and asks you to pick. A different wrap name is a **separate** binary, never merged in.

Separate per guardfile: the spec lock and reference doc. Shared: the generated `main.go` and the one `specverb.lock`. The override env var is keyed on the full wrap group, so merged specs never collide. Spec and exec members can share one binary - see [mixed transports](specverb-mixed-transports.md).

## The five verbs

- **`gen`** - render the merged `main.go` into the cache (or `--out` to inspect it), and write each member's reference doc (`<name>.md`) from the committed spec lock. A debug step; `run` materializes for you.
- **`lock`** - the deliberate online step. For each member, fetches the upstream Swagger, **prunes it to the granted surface**, writes the committed spec lock (`<spec>.lock.json`), and refreshes its reference doc; then resolves the merged build's module graph (`go mod tidy` in a throwaway module) once and freezes it into the shared `specverb.lock`. `--cli-guard-ref` pins the framework version (defaults to the driver's own); `--cli-guard-replace` points at a local checkout for development.
- **`skew`** - for each member, prune live upstream to the same granted surface and diff it against the committed lock, each drift line prefixed with its member, so only operations the consumer exposes register as drift. Exit 3 on any drift, never writes; a fetch failure is a plain error, distinguishable from drift.
- **`build`** - materialize the binary **out-of-band** (same cache + staleness path as `run`) and copy it to `--out` (default `bin`) instead of execing it. `--out` follows `go build -o`: a directory (or trailing `/`) takes the Guardfile-derived binary name, else it is the explicit file path. `--set-version <v>` stamps the binary's `--version` via `-ldflags`, default `dev`. Refuses without committed locks.
- **`run`** - materialize the consumer binary **out-of-band** and exec it with the passed-through args.

## Out-of-band materialization

The merged `main.go`, the build's `go.mod`/`go.sum` (replayed from `specverb.lock`), every member's embed inputs, and the compiled binary live in a cache under `config.CacheDir()`, keyed by the **sorted set of member paths** so two repos sharing a binary name never collide. A `.stamp.json` of input hashes makes the common path a no-op exec; a rebuild fires only when a Guardfile, any lock, the driver version, or the version stamp changed. `run` refuses without committed locks rather than silently locking.

## The two locks

The consumer's only source-of-truth build artifacts, the analog of `pyproject.toml` + `uv.lock`:

- **`<spec>.lock.json`** - the embedded API snapshot the binary mounts from, one per merged API, **pruned to the granted operations + their transitive `$ref` schemas** (not the full upstream dump). It is the consumer's own contract, small and reviewable.
- **`specverb.lock`** - a dedicated JSON (not a repo `go.mod`, which would make the consumer look like a Go module) freezing the resolved dependency graph for a reproducible offline build. One per binary, covering every merged API's deps.

First `run` after a fresh `lock` works offline because `lock`'s `go mod tidy` already warmed the module cache.

Origin: [cli-guard#106](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/106).
