# Embedded fixed files

Specgen can compile a reviewed file into a generated exec-dialect binary and
place its absolute runtime path at a fixed argv position. This supports complex
logic without depending on a repository checkout or relative runtime path.

## Guardfile shape

```kdl
wrap example ops measure {
    exec python3
    can run storage {
        argv "-I"
        embed "scripts/storage_measure.py"
        argv "--format" "text"
        sealed
    }
}
```

`argv` fragments and `embed` nodes append fixed tokens in declaration order.
The example executes `python3 -I <absolute-path> --format text`. Help and
describe output show `<embedded:scripts/storage_measure.py>` instead of the
invocation-specific temporary path.

`embed` counts as a pinned argv override, so a grant containing only an
embedded file can be sealed. An unsealed grant still appends validated caller
arguments after every fixed token.

## Build boundary

The source path is relative to the declaring guardfile. It must be normalized,
portable, and confined to that directory. Absolute paths, `..`, backslashes,
unsupported filename characters, symlink escapes, missing files, non-regular
files, and artifact collisions fail the build. A single file is limited to 4
MiB.

Specgen reads the source during project discovery, includes its identity and
bytes in the cache hash, copies it into the generated module, and emits a
`go:embed` declaration. Changing only the file content therefore rebuilds the
generated binary.

## Runtime boundary

The generated binary writes embedded files beneath one private temporary
directory using owner-only permissions. Execverb receives a map from the
guardfile source name to an absolute materialized path and fails closed when a
reference is missing or non-absolute. The caller cannot select, replace, or
reorder that path.

The temporary directory exists only while the generated command process runs.
Cleanup executes after success or failure. The build-time source path remains
relative because it is compiler input. The guarded runtime command never uses
a relative script path.

## See also

- [specgen](specgen.md) - discovery, generation, locks, and builds.
- [execverb](execverb.md) - fixed argv, sealing, and runtime policy.
