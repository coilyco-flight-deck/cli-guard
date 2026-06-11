# the no-code driver (specdrv / cmd/specverb-gen)

`ward-kdl` is a **no-code** CLI: the consumer authors only policy plus its committed locks, never Go or build glue. `specverb-gen` is the uv-style driver that makes that real - the `uv run` / `uv lock` model, not make targets. See [specverb.md](specverb.md) for the engine the driver wraps.

## Discovery and merging

A `--guardfile` selects a **binary**, not the whole build: the driver reads every `*.guardfile.kdl` in that file's directory sharing its `wrap <binary>` name (`Group[0]`) and merges them, each mounting as its own command group. With no `--guardfile`, the cwd must resolve to one binary, else the driver lists them and asks you to pick. A different wrap name is a **separate** binary, never merged in.

Separate per guardfile: the spec lock and reference doc. Shared by the merge: the generated `main.go` and the one `specverb.lock` (the union of every member's deps). The override env var is keyed on the full wrap group (e.g. `WARD_OPS_FORGEJO_SPEC`), so merged specs never collide.

## The five verbs

- **`gen`** - render the merged `main.go` into the cache (or `--out` to inspect it), and write each member's reference doc (`<name>.md`) beside its Guardfile from the committed spec lock. A debug step for the source; `run` materializes for you.
- **`lock`** - the deliberate online step. For each member, fetches the upstream Swagger to its committed spec lock (`<spec>.lock.json`) and refreshes its reference doc; then resolves the merged build's module graph (`go mod tidy` in a throwaway module) once and freezes it into the shared `specverb.lock`. `--cli-guard-ref` pins the framework version (defaults to the driver's own); `--cli-guard-replace` points at a local checkout for development.
- **`skew`** - for each member, diff the committed spec lock against live upstream at the operation level (added / removed / changed paths and definitions), each drift line prefixed with the member it came from. Exit 3 on any drift, never writes; a fetch failure is a plain error, so offline is distinguishable from drift.
- **`build`** - materialize the consumer binary **out-of-band** (same cache + staleness path as `run`) and copy it to `--out` (default `bin`) instead of execing it, so the consumer keeps a real binary on disk to invoke directly. `--out` naming follows `go build -o`: an existing directory (or a trailing separator) takes the Guardfile-derived binary name; anything else is the explicit file path. Refuses without committed locks.
- **`run`** - materialize the consumer binary **out-of-band** and exec it with the passed-through args.

## Out-of-band materialization

`run`'s generated merged `main.go`, the build's `go.mod`/`go.sum` (replayed from `specverb.lock`), every member's embed inputs, and the compiled binary all live in a cache under `config.CacheDir()`, keyed by the **sorted set of member paths** so two repos sharing a binary name never collide - never in the consumer repo, the analog of a venv. A `.stamp.json` of input hashes (combined over all guardfiles and all spec locks) makes the common path a no-op exec; a rebuild fires only when a Guardfile, any lock, or the driver version changed. `run` refuses to run without committed locks rather than silently locking, keeping the network step explicit.

## The two locks

The consumer's only source-of-truth build artifacts, the analog of `pyproject.toml` + `uv.lock`:

- **`<spec>.lock.json`** - the embedded upstream API snapshot the binary mounts from, one per merged API.
- **`specverb.lock`** - a dedicated JSON (not a `go.mod` in the repo, which would make the consumer look like a Go module) freezing the resolved dependency graph for a reproducible offline build. One per binary, covering every merged API's deps.

First `run` after a fresh `lock` works offline because `lock`'s `go mod tidy` already warmed the module cache.

Origin: [cli-guard#106](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/106).
