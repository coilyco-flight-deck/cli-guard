# sandbox: the namespace jail and its degrade path

`cli/sandbox` jails a gate-spawned child in a Linux **user + mount namespace** so
the tool and its descendants re-enter the gate (they can't reach a wrapped binary
without hitting the shim) and a seccomp denylist blocks escape syscalls. It is a
no-op off Linux. The jail is applied by `shell.Runner.Exec` when the runner
carries a non-nil `sandbox.Spec`; read-only `Capture` calls are never jailed.

## When the environment can't jail

Creating the namespace needs privileges some environments deny. Two failure
modes were hit in the field:

- **Mode 1 - clone denied.** `CLONE_NEWUSER` is blocked (a container without
  userns, some restricted agent shells), so the jailed child never starts:
  `fork/exec <bin>: operation not permitted`.
- **Mode 2 - make-rprivate denied.** The namespace is created but the in-child
  `mount --make-rprivate` is refused: `sandbox: make-rprivate: permission denied`.

Without handling, every sandboxed verb dies in these environments - even though
the environment is *already* an isolation boundary (a container) or one where the
jail adds little.

## Two escapes

**Auto-degrade (mode 1).** When a jailed child fails to *start* with `EPERM`/
`EACCES` (`sandbox.SetupDenied`), `shell.Runner` retries the command unsandboxed
and warns once. The child never ran, so the retry is side-effect-safe. A
started-then-failed run is a real tool error and is never degraded.

**Opt-out env (both modes).** Set **`CLIGUARD_NO_SANDBOX=1`** (also `true`/`yes`/
`on`) and `Wrap` no-ops up front - tools run directly. Use it where the namespace
jail is redundant or always-denied, e.g. `ward container`'s entrypoint exports it
because the container itself is the boundary, which also unblocks the in-container
reaper (mode 2, which auto-degrade does not catch).

## Choosing

- In a **container** that is already the isolation boundary: set
  `CLIGUARD_NO_SANDBOX=1` (deterministic, no per-exec retry, covers both modes).
- In a **restricted shell** where you still want the jail when available: rely on
  auto-degrade - it sandboxes when it can and falls back when it can't.
- On a normal host: leave both unset; the jail applies.

## See also

- [docs/architecture.md](architecture.md) - where the sandbox sits in the gate.
- [docs/SECURITY.md](SECURITY.md) - the boundary's threat model.
