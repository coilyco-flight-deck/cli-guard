# Security Policy

Hello and thank you for your interest! :tada: :lock:

## Supported versions

This package is at v0. Only the latest commit on `main` is supported for security fixes - there are no published releases yet to backport to.

| Version             | Supported          |
| ------------------- | ------------------ |
| `main` (latest)     | :white_check_mark: |
| any pinned commit   | :x: (upgrade)      |

## Reporting a vulnerability

Please disclose any vulnerabilities by emailing [coilysiren@gmail.com](mailto:coilysiren@gmail.com). Expect a first response within 48 hours; follow-up cadence by email after that. This project is run on volunteer time, so please have patience :bow:

## What counts as a vulnerability

cli-guard is a security-boundary framework. Issues here can have outsized impact on every downstream consumer. Specifically interested in reports of:

- argv passing through `policy.ValidateArgSlice` that should have been rejected (shell metacharacter escapes, encoding tricks, locale-dependent bypasses)
- audit log entries that are unparseable, truncatable, or omittable by the wrapped action
- scope-token bypasses (a read token executing a write action, etc)
- `gittree.CheckClean` returning OK on a tree that does not reconstruct from git history
- CONNECT-proxy allowlist bypasses in the `egress` package
- `sandbox` jail escapes: a wrapped tool spawned under the jail — or any descendant of it — invoking a wrapped tool (by name **or** absolute path) without re-entering the consumer gate (shim-mask bypass), or defeating the seccomp denylist / namespace confinement (ptrace, kernel-module load, re-namespacing to undo the bind-mounts). These properties are pinned by `TestSecurityClaim_GrandchildRoutesThroughGate` and `TestSecurityClaim_SeccompDeniesPtrace` in `cli/sandbox/`; a passing test that does not actually hold is itself a vulnerability.

Out of scope (file as regular issues, not vulnerabilities):

- bare urfave/cli framework bugs - report those upstream at [urfave/cli](https://github.com/urfave/cli)
- consumer misuse of the public API (e.g. forgetting to wire `verb.Wrap`) - that is a documentation issue
- crashes on intentionally malformed yaml in `repocfg` - failing loudly is the intended behavior
