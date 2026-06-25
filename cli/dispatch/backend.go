package dispatch

import "context"

// backend.go defines the pluggable backend seam for the detached surfaces
// (headless, cascade). See docs/dispatch-backends.md and issue #162.

// Backend abstracts where and how a detached dispatch runs: reserve
// capacity, prepare a working dir, spawn the worker, reap finished work.
type Backend interface {
	// Name labels the backend in output and the audit Event
	// (e.g. "worktree", "container").
	Name() string

	// Reserve claims capacity for one dispatch of ref before provisioning,
	// returning a Reservation the caller releases if the launch aborts.
	Reserve(ctx context.Context, ref *IssueRef) (Reservation, error)

	// Prepare returns (and, unless dryRun, creates) the working directory
	// the worker runs in. repoPath is the canonical local checkout.
	Prepare(ctx context.Context, repoPath string, ref *IssueRef, title string, dryRun bool) (string, error)

	// Spawn launches the detached worker described by plan and returns its
	// pid (or a backend handle's pid-equivalent) for status + crash watch.
	Spawn(ctx context.Context, plan SpawnPlan) (int, error)

	// Reap removes finished artifacts (merged worktrees, exited containers)
	// and returns the removed ids. Best-effort: must never block or fail hard.
	Reap(ctx context.Context) ([]string, error)
}

// Reservation is a backend's claim on capacity for one dispatch, taken by
// Reserve and released if the launch aborts. Worktree's is a no-op.
type Reservation interface {
	// ID identifies the reservation (container name, slot id) in output and
	// audit. The worktree backend returns "".
	ID() string

	// Release frees the reservation. dispatch calls it on a failed launch;
	// a successful spawn leaves it for Reap. Safe to call once.
	Release() error
}

// SpawnPlan carries everything Backend.Spawn needs to launch the detached
// worker. A backend reads the fields it needs and ignores the rest.
type SpawnPlan struct {
	// Ref is the resolved, owner-checked issue reference.
	Ref *IssueRef
	// Issue is the fetched issue (title, body, URL, state).
	Issue *Issue
	// RepoPath is the canonical local checkout the worker lands into.
	RepoPath string
	// Cwd is the working directory returned by Backend.Prepare.
	Cwd string
	// Bin is the resolved claude binary the worker should exec.
	Bin string
	// Argv is the claude argv (prompt, permission mode, allowed tools).
	Argv []string
	// Env is the child environment, including any per-surface extras.
	Env []string
	// LogPath is the per-dispatch log file the worker's stdio lands in.
	LogPath string
	// Reservation is the slot Backend.Reserve claimed for this dispatch.
	Reservation Reservation
}

// worktreeBackend is the default Backend: a per-issue git worktree on the
// host. It delegates to the existing Worktree*/SpawnDetached seams.
type worktreeBackend struct{ d *Dispatcher }

// Name labels the default backend.
func (b worktreeBackend) Name() string { return "worktree" }

// Reserve is a no-op: a worktree needs no capacity reservation.
func (b worktreeBackend) Reserve(context.Context, *IssueRef) (Reservation, error) {
	return noopReservation{}, nil
}

// Prepare resolves (and, unless dryRun, creates) the per-issue worktree.
func (b worktreeBackend) Prepare(ctx context.Context, repoPath string, ref *IssueRef, title string, dryRun bool) (string, error) {
	return b.d.resolveDetachedCwd(ctx, repoPath, ref, title, dryRun)
}

// Spawn launches the detached child via the SpawnDetached seam.
func (b worktreeBackend) Spawn(_ context.Context, plan SpawnPlan) (int, error) {
	return b.d.cfg.SpawnDetached(plan.Cwd, plan.LogPath, plan.Bin, plan.Argv, plan.Env)
}

// Reap removes every merged dispatch worktree under the worktree root.
func (b worktreeBackend) Reap(ctx context.Context) ([]string, error) {
	return b.d.reapDispatchWorktrees(ctx)
}

// noopReservation is the worktree backend's reservation: nothing to hold.
type noopReservation struct{}

// ID reports no reservation id.
func (noopReservation) ID() string { return "" }

// Release is a no-op.
func (noopReservation) Release() error { return nil }
