# kdl-specs project discovery

`--project-root <dir>` is kdl-specs' explicit recursive KDL discovery boundary. Every `.kdl` file below it is inspected, and an operation member is recognized by a parsed top-level `wrap` declaration rather than its filename. Existing `*.guardfile.kdl` names remain valid; they are no longer required within an explicit root.

`--guardfile <path>` may point at any member under the root and selects its `wrap <binary>` group (`Group[0]`). With no selector, exactly one binary group must be present. More than one fails with an actionable sorted list; groups are never merged across a binary identity.

Member paths are normalized relative to the selected root and sorted lexically before rendering, hashing, locking, documenting, or building. Re-rooting an unchanged project therefore preserves generated order and cache identity. Per-member locks and reference docs retain those relative directories, so `cloud/api.kdl` and `forge/api.kdl` cannot overwrite one another even with the same basename. `main.go`, `specverb.lock`, and the materialization cache remain per generated binary.

Parsed KDL without a top-level `wrap` is unrelated configuration and is ignored, including the recognized `agents`/legacy `fleet` configuration dialect. A malformed file that indicates operation intent with a `wrap` declaration is not ignored. Unreadable candidates, duplicate logical members, conflicting member artifacts, and symlinks resolving outside the root all fail before generation. Choose a narrower project root when an unrelated malformed KDL file is outside the operation project.

Without `--project-root`, legacy discovery remains deliberately narrow and compatible: kdl-specs considers non-recursive `*.guardfile.kdl` files in the selected member's directory (or cwd).

See [kdl-specs.md](kdl-specs.md) for the driver and [kdl-specs-materialization.md](kdl-specs-materialization.md) for cache and build artifacts.
