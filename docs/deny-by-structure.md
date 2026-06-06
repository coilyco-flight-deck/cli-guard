# Deny-by-structure: shim + doctor

A denylist is an enumeration problem (N tools x M hosts x every version bump), which is why a rendered deny ruleset churns. A boundary is one invariant. The deny-by-structure model trades the enumeration for a small consumer-owned literal plus three layers that expand from it.

## The literal

A consumer declares its protected tools once, in `repocfg.Security` (the `security:` block of its config). `protected_binaries` names each tool, its `allowed_wrappers`, `expected_real_paths`, and `credential_env`; `sudo.forbid_passwordless` asserts the floor. cli-guard carries no org's binary list - the literal is the only input.

## Three layers from one literal

1. **hook deny (enforcement at the harness).** `hookcfg.ProtectedFor` maps the literal to `[]hook.Protected`. The PreToolUse engine denies the binary basename-aware: bare `gcloud`, `/opt/homebrew/bin/gcloud`, `env X=y gcloud`, and `sudo gcloud` all hit the same deny. This catches the absolute-path spelling a PATH shim alone misses.

2. **shim (UX).** `shim.For` takes the same `[]hook.Protected` and renders one deny shim per binary - a tiny POSIX-sh script, single-quote-escaped and `sh -n` validated, installed under the binary basename ahead of the real install on PATH. It prints the recovery path and exits non-zero. Because shim and hook derive from one literal, the shim set and the deny set cannot drift apart.

3. **doctor (enforcement at the floor).** `doctor.Check` verifies what the hook and shim cannot.

## Boundary honesty

The shim is **UX, not the enforcement boundary.** It only shadows the bare name on PATH; a same-user agent can still reach the real binary by absolute path or by reordering PATH. "Unix permissions do not provide parent-process authorization for same-user invocations." If the binary stays user-executable, you are back to needing a denylist to paper the gap.

The actual enforcement is two invariants the doctor checks:

- **The agent has no broad passwordless sudo.** When `sudo.forbid_passwordless` is set, a successful `sudo -n true` is a **Fail**: the agent can escalate without the human's password, and the password is the human carve-out the whole model depends on.
- **The real binary is not agent-executable.** Each `expected_real_paths` entry that the agent user can execute is a **Fail** - an absolute-path call bypasses the shim. The fix is filesystem posture: root-owned, not user-executable. The human runs `sudo <realtool>` because she has the password.

A third, softer check: any `credential_env` var present in the session is a **Warn** - even a locked binary is moot if another reachable tool can read the credentials from the environment.

## Testability

doctor's filesystem / sudo / env seams inject via `Probes`, so the logic is table-tested without touching root files or invoking real sudo. `DefaultProbes` wires the real host; the ownership-aware exec check is build-tagged for unix and reports unknown (a Warn, not a false Pass/Fail) elsewhere. Run doctor **as the agent user** - run as root and it reports root's reach, not the agent's.

## See also

- [FEATURES.md](FEATURES.md) - the primitive index.
- `hook`, `hookcfg`, `repocfg`, `shim`, `doctor` package docs.
