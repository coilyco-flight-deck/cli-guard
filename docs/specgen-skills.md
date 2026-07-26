# specgen generated skills

Specgen can turn a selected binary's complete merged command tree into a native
agent skill. This output is opt-in. Normal `gen`, `lock`, `build`, and `run`
calls write no skill or Markdown reference into the consumer tree.

## Generate

Pass the persistent `--skills-out <root>` flag before a verb:

```text
specgen --project-root .specgen \
  --skills-out .agents/generated lock
```

The selected binary writes:

* `<root>/<binary>/SKILL.md` - valid frontmatter and a short orientation.
* `<root>/<binary>/references/commands.yaml` - every reachable leaf, summary,
  and canonical flag from the merged urfave tree.

The binary name is normalized to a lowercase skill-safe directory and
frontmatter name. Identical specs, locks, runtime binary names, and generator
versions produce identical paths and content.

## Discovery boundary

The eager `SKILL.md` stays deliberately small. It tells an agent to start with
the binary's `--help`, follow group help, and use `describe` where the generated
surface exposes it. The lazy YAML index makes every leaf discoverable without
copying exhaustive CLI reference prose into startup context.

The running CLI remains authoritative. The generated skill grants no
permission, resolves no credential, and does not replace runtime policy.

## Lifecycle

* `gen` renders a skill only when `--skills-out` is explicit. Spec members need
  committed locks first.
* `lock` may refresh locks and the skill in one explicit call.
* `build` and `run` keep out-of-band materialization. They add skill output only
  when the caller supplies the flag.
* Mixed spec and exec members contribute to one command index after mounting
  under the shared runtime binary.
* Per-member `<member>.md` reference generation is retired. Human-maintained
  product documentation stays outside specgen output.

## See also

* [specgen](specgen.md) - driver lifecycle and locks.
* [describe](specverb-describe.md) - pulled spec-backed reference behavior.
* [mixed transports](specverb-mixed-transports.md) - one binary across both
  transport dialects.

This is cli-guard's independent boundary in
[inbox#267](https://forgejo.coilysiren.me/coilysiren/inbox/issues/267).
