# Pluggable dispatch backends and pre-flight verdicts

The [dispatch](FEATURES.md) subsystem fires `claude` against a real open issue. Its core - owner allow-listing (`AllowedOwner` / `AllowedOwners`), reference parsing (`IssueRef` / `ParseIssueRef`), and the sidequest registry - is shared. Two seams let a consumer drive the detached surfaces (`headless`, `cascade`) without re-hosting that core: the run **backend** and the pre-flight **verdict** stage.

## Backend seam

`Config.Backend` selects where and how a detached worker runs. Nil installs the historical worktree backend (a per-issue git worktree on the host running a detached `claude -p`), built from the existing `Worktree*` / `SpawnDetached` seams, so existing consumers are unchanged. A consumer supplies its own `Backend` to run workers in containers instead.

The `Backend` interface is the worker lifecycle a consumer swaps as a unit:

- **`Reserve(ctx, ref) (Reservation, error)`** - claim capacity before any provisioning. The worktree backend returns a no-op reservation. A container backend takes a host slot with a TTL (the reservation notice on the issue). dispatch releases the reservation if the launch aborts after Reserve; a successful spawn hands it to the worker and `Reap` frees it later.
- **`Prepare(ctx, repoPath, ref, title, dryRun) (string, error)`** - return the working directory the worker runs in, creating it unless `dryRun`. The worktree backend resolves the per-issue worktree path.
- **`Spawn(ctx, plan) (int, error)`** - launch the detached worker described by the `SpawnPlan` (resolved ref + issue, cwd, claude bin + argv, env, log path, reservation) and return a pid used by `dispatch status` and the startup-crash watch.
- **`Reap(ctx) ([]string, error)`** - remove finished artifacts (merged worktrees, exited containers). Runs at the head of every detached dispatch and behind the `dispatch reap` verb, so it must never block or hard-fail.

When `Backend` is set, `WorktreeRoot` is no longer required: a container consumer never touches the host worktree root.

## Pre-flight verdict stage

`Config.Preflight` is the consumer's last word before any backend work. It runs **after** dispatch's own gates (owner allow-list, issue is open), so a foreign-owner ref is refused before the stage ever sees it. The consumer returns a `PreflightResult`:

- **`VerdictGo`** - proceed.
- **`VerdictNoGo`** - refuse outright (a consumer veto: a closed milestone, a label gate, a reservation already held by another agent).
- **`VerdictWrongRepo`** - refuse because the ref targets a repo this host does not serve, distinct from NO-GO so a caller can route the ref elsewhere instead of treating it as a hard failure.

The stage is **fail-closed**: only `VerdictGo` proceeds. A zero/empty or unrecognized verdict refuses just like NO-GO, and a returned error aborts the dispatch. A nil `Preflight` is an implicit GO - the stage is opt-in. The `Reason` on the result is folded into the refusal message.

## Shared core

Whichever backend or verdict a consumer wires, these stay common: `AllowedOwner` / `AllowedOwners` (the security claim), `ParseIssueRef` (the reference grammar, exported so a consumer's `Preflight` or `Backend` parses refs exactly as dispatch does), and the reservation / registry concepts.
