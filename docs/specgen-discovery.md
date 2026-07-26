# specgen project discovery

With no `--project-root` or `--guardfile`, specgen uses a `.specgen/` directory in the current working directory as its conventional recursive project boundary. An explicit `--project-root <dir>` selects another boundary. Every `.kdl` file below the selected root is inspected, and an operation member is recognized by a parsed top-level `wrap` declaration rather than its filename. Existing `*.guardfile.kdl` names remain valid; they are no longer required within a project root.

`--guardfile <path>` may point at any member under the root and selects its `wrap <binary>` group (`Group[0]`). With no selector, exactly one binary group must be present. More than one fails with an actionable sorted list; groups are never merged across a binary identity.

Member paths are normalized relative to the selected root and sorted lexically
before rendering, hashing, locking, indexing, or building. Re-rooting an
unchanged project therefore preserves generated order and cache identity.
Per-member locks retain those relative directories, so `cloud/api.kdl` and
`forge/api.kdl` cannot overwrite one another even with the same basename.
`main.go`, `specverb.lock`, the generated skill, and the materialization cache
remain per generated binary.

Parsed KDL without a top-level `wrap` is unrelated configuration and is ignored, including the recognized `agents`/legacy `fleet` configuration dialect. A malformed file that indicates operation intent with a `wrap` declaration is not ignored. Unreadable candidates, duplicate logical members, conflicting member artifacts, and symlinks resolving outside the root all fail before generation. Choose a narrower project root when an unrelated malformed KDL file is outside the operation project.

An explicit `--guardfile` without `--project-root` keeps legacy discovery deliberately narrow and compatible: specgen considers non-recursive `*.guardfile.kdl` files in the selected member's directory. With no flags and no `.specgen/` directory, the same legacy discovery runs in the current working directory.

See [specgen.md](specgen.md) for the driver and [specgen-materialization.md](specgen-materialization.md) for cache and build artifacts.
