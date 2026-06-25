# ward helper packages

Reusable helpers lifted out of ward so any consumer (ward included) imports
one source of truth instead of carrying its own copy. Each is standalone,
dependency-free beyond the stdlib, and importable from a different binary
without consumer-specific types leaking in. Indexed from [FEATURES.md](FEATURES.md).

## `pkg/scan` - junk-scan

Pure policy over a caller-supplied `[]Entry` (path, bytes, binary). `Diff`
returns one `Finding` per flagged path: vendored/generated trees
(`node_modules`, `vendor`, `.venv`, `target`, ...), credential-shaped files
(`.env`, `id_rsa`, `*.pem`, ... with `.env.example` / `.sample` allowed), and
oversized (>=5 MiB) or large-binary (>=1 MiB) blobs. First rule per path wins.
No git or filesystem access, so a reaper, a pre-merge gate, and a CI step
share one ruleset. `HumanBytes` renders a compact size for report text.

## `pkg/attribution` - agent identity + signing

`Identity{Name, Pronouns}.Label()` renders "Claude (she/her)" or "Goose". A
`Signer` carries the identity plus consumer-supplied text: an idempotency
`Marker`, a footer tail `Via`, and a trailer `Email`. `SignBody` appends a
hidden-marker footer exactly once (idempotent, empty-body-safe; a no-op when
`Marker` is empty), and `CommitTrailer` renders a git `Co-Authored-By` line.
No baked-in agent roster: the caller supplies who signs.

## `pkg/flock` - advisory file lock

`Exclusive` / `Unlock` over a shared lock `*os.File`, wrapping BSD advisory
`flock(2)` (LOCK_EX / LOCK_UN) for cross-process mutual exclusion - one
warm-cache writer at a time. Unix-only; the non-unix build degrades both to a
no-op, since the syscall does not exist there.

## `pkg/version` - release-tag compare

`Parse` splits a `vX.Y.Z` tag into three ints, tolerating a missing `v`, a
short tag, and a `-pre` / `+build` suffix (so `v0.5.2-rc1` parses as 0.5.2).
`Behind(current, latest)` powers a self-update nag: it returns true only when
both tags parse and `current` unambiguously trails, so it never cries wolf on
a dev or unparseable build. `LooksReleased` screens the `dev` / blank build.

## `pkg/issueref` - issue-ref parse

`Parse(s, baseURL)` turns a ref string into `Ref{Owner, Repo, Number}`. Three
forms parse: `owner/repo#N`, a bare `#N` / `N` (owner/repo left empty for the
caller to fill from context), and a `<baseURL>/owner/repo/issues/N` Forgejo
URL with a tolerated trailing slash / `?query` / `#fragment`. `baseURL` may be
empty to disable URL parsing. The Dispatcher-bound parse in `cli/dispatch`
keeps its own variant for its flow; this is the dependency-free form.

## `pkg/ownertrust` - owner allow-list gate

`List{Primary, Extra}.Allowed(owner)` is the single yes/no an elevated agent
needs before it fans out into a repo (an empty owner is never allowed).
`Label` renders the accepted set for a refusal message: `primary/*` for one
owner, `{primary, a, b}/*` when `Extra` adds more.

## `cli/hook` - registry + settings-installer

Two additions to the PreToolUse engine. A guard `Registry` maps detectable
guards (name + config `Marker` + Route / IntegrityRule tables); `Detect`
walks up from a directory to the nearest marker and returns the guard name,
falling back to `Default`. A settings `Installer` idempotently registers a
PreToolUse hook command in a Claude Code `settings.json`: `Ensure` does the
additive merge (other hooks and unrelated keys preserved), with
`LoadSettings` / `MarshalSettings` / `WriteSettings` / `ResolveSettingsPath`
around it. The consumer supplies the matcher and command, so any harness
registers its own hook line.

## See also

- [FEATURES.md](FEATURES.md) - inventory index.
- [features-detail.md](features-detail.md) - per-primitive detail.
