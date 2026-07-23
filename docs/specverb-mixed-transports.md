# mixed transports in one binary

A merged binary can hold both dialects: spec members ([specverb.md](specverb.md), HTTP APIs from a `base-url` + Swagger) and exec members ([execverb.md](execverb.md), wrapped binaries from an `exec <bin>` block). This is what lets `ward-kdl ops forgejo` (spec) and `ward-kdl ops aws` (exec) ship as the one `ward-kdl` binary.

## How the driver merges them

The driver sniffs each `*.guardfile.kdl`'s transport - an `exec` child of the `wrap` block is the exec dialect, otherwise spec - and parses it with the matching parser. Both dialects derive their binary name from `Group[0]`, so guardfiles sharing a wrap binary name merge regardless of transport, each mounting as its own command group.

The generated `main.go` dispatches per member: a spec member mounts through `specverb.Mount`, an exec member through `execverb.Mount`. The spec-only imports and helpers (the HTTP fetch, the SSM token resolver) are gated behind the presence of a spec member, and the `execverb` import behind an exec member, so the binary compiles with either dialect alone or both together.

## What an exec member skips

An exec member carries no upstream spec, so it skips every spec-only seam:

- **No spec lock** - it embeds only its policy guardfile, not a `<spec>.lock.json.gz`.
- **No fetch or skew** - `lock` and `skew` iterate spec members only; an exec member has nothing upstream to fetch or drift against.
- **No SSM token** - `execverb.Mount` takes no auth token; the wrapped binary owns its own credentials.

## Reference doc parity

A spec member's reference doc comes from its committed spec lock (`specverb.Describe`); an exec member's comes from its parsed policy (`execverb.Describe`). Both land beside the guardfile as the same `<name>.md` artifact, refreshed by `gen` / `lock`.

See [specgen.md](specgen.md) for the driver lifecycle and [execverb.md](execverb.md) for the exec dialect.

Origin: the mixed-transport split.
