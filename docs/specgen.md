# the no-code driver (specgen / cmd/specgen)

`ward-kdl` is a **no-code** CLI: the consumer authors policy plus committed locks, never Go or build glue. `specgen` is the uv-style driver. Every spec may carry a top-level [`description`](kdl-description.md) node: standing context, not a comment header.

## Install

See [install specgen](specgen-install.md) for release binaries, checksums,
`go install`, and the driver-to-framework version contract.

## Discovery and merging

A `--guardfile` selects a **binary**, not the whole build: members compose only when their parsed `wrap <binary>` name (`Group[0]`) agrees. A different wrap name is a **separate** binary, never merged in.

A repository can place its recursive project under `.specgen/` and invoke the driver without discovery flags. Pass `--project-root <dir>` for another explicit boundary; a `--guardfile <path>` inside it selects that member's binary group. See [specgen discovery](specgen-discovery.md) for membership, mixed-dialect, identity, and fail-closed rules.

`gen`, `build`, and `run` accept `--binary <name>` to rename only the generated command and cache/build output; discovery and policy identity still come from `wrap`.

Locks and reference docs are per member and preserve root-relative directories; `main.go` and `specverb.lock` are per binary. The override env var is keyed on the full wrap group. Spec and exec members can share one binary - see [mixed transports](specverb-mixed-transports.md).

## The five verbs

- **`gen`** - render the merged `main.go` into the cache (or `--out` to inspect it), and write each member's root-relative reference doc (`<member>.md`) from the committed spec lock. A debug step; `run` materializes for you. `--binary <name>` overrides the generated app name for inspection or renamed builds.
- **`lock`** - the deliberate online step. For each member, reads a [vendored source](specgen-vendored-sources.md) or fetches upstream Swagger, **prunes it to the granted surface**, writes the deterministic gzip lock, and refreshes its reference doc. It then resolves and freezes the merged module graph in `specverb.lock`. `--cli-guard-ref` pins the framework version; `--cli-guard-replace` points at a local checkout.
- **`skew`** - for each member, prune live upstream to the same granted surface and diff it against the committed lock, each drift line prefixed with its member, so only operations the consumer exposes register as drift. Exit 3 on any drift, never writes; a fetch failure is a plain error, distinguishable from drift.
- **`build`** - materialize the binary **out-of-band** (same cache + staleness path as `run`) and copy it to `--out` (default `bin`) instead of execing it. `--out` follows `go build -o`: a directory (or trailing `/`) takes the generated binary name, else it is the explicit file path. `--binary <name>` sets that generated name, defaulting to the Guardfile-derived binary. `--set-version <v>` stamps the binary's `--version` via `-ldflags`, default `dev`. Refuses without committed locks.
- **`run`** - materialize the consumer binary **out-of-band** and exec it with the passed-through args. `--binary <name>` uses the renamed generated app.

## Out-of-band materialization

`run` and `build` share the [materialization](specgen-materialization.md) cache, keyed by generated binary name and root-relative member identities.

## The two locks

The committed build artifacts are:

- **`<spec>.lock.json.gz`** - one generated, gzip-encoded pruned API snapshot per member, placed beneath that member's root-relative directory when `--project-root` is used. Specgen reads the former plain `<spec>.lock.json` name until the next `lock` refresh replaces it.
- **`specverb.lock`** - one frozen Go dependency graph per binary.

See [specgen-materialization.md](specgen-materialization.md) for cache and offline-build detail.

Origin: the KDL specs surface.
