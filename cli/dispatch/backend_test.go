package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"github.com/urfave/cli/v3"
)

// fakeReservation records whether Release was called.
type fakeReservation struct {
	id       string
	released *bool
}

func (r fakeReservation) ID() string { return r.id }
func (r fakeReservation) Release() error {
	*r.released = true
	return nil
}

// fakeBackend is a recording Backend stand-in for a container backend.
type fakeBackend struct {
	name        string
	reserved    bool
	released    bool
	prepareDry  *bool
	reapCalled  bool
	spawnPlan   *SpawnPlan
	spawnErr    error
	prepareCwd  string
	prepareErr  error
	reserveErr  error
	reapRemoved []string
}

func (b *fakeBackend) Name() string { return b.name }

func (b *fakeBackend) Reserve(_ context.Context, _ *IssueRef) (Reservation, error) {
	b.reserved = true
	if b.reserveErr != nil {
		return nil, b.reserveErr
	}
	return fakeReservation{id: "slot-1", released: &b.released}, nil
}

func (b *fakeBackend) Prepare(_ context.Context, _ string, _ *IssueRef, _ string, dryRun bool) (string, error) {
	dr := dryRun
	b.prepareDry = &dr
	if b.prepareErr != nil {
		return "", b.prepareErr
	}
	return b.prepareCwd, nil
}

func (b *fakeBackend) Spawn(_ context.Context, plan SpawnPlan) (int, error) {
	b.spawnPlan = &plan
	if b.spawnErr != nil {
		return 0, b.spawnErr
	}
	return 4242, nil
}

func (b *fakeBackend) Reap(context.Context) ([]string, error) {
	b.reapCalled = true
	return b.reapRemoved, nil
}

// newBackendDispatcher wires a Dispatcher to a custom backend + open-issue
// fetcher, health watch disabled, so a detached run flows without processes.
func newBackendDispatcher(t *testing.T, b Backend) *Dispatcher {
	t.Helper()
	repoDir := t.TempDir()
	logRoot := t.TempDir()
	d, err := New(Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		RepoPath:     func(string) (string, error) { return repoDir, nil },
		LogRoot:      func() (string, error) { return logRoot, nil },
		// No WorktreeRoot: a custom backend lifts the requirement.
		Backend:           b,
		SpawnHealthWindow: -1, // disable the startup-crash watch
		ClaudeConfigPath:  func() (string, error) { return filepath.Join(t.TempDir(), ".claude.json"), nil },
		IssueFetcher: func(_ context.Context, _, repo string, n int) (*Issue, error) {
			return &Issue{Number: n, Title: "backend issue", State: "open", URL: "https://example/" + repo, Labels: []string{"headless"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// TestBackend_DefaultIsWorktree proves New installs the worktree backend
// when Config.Backend is nil, so existing consumers are unaffected.
func TestBackend_DefaultIsWorktree(t *testing.T) {
	d := newTestDispatcher(t)
	if got := d.backend.Name(); got != "worktree" {
		t.Errorf("default backend = %q, want worktree", got)
	}
	if _, ok := d.backend.(worktreeBackend); !ok {
		t.Errorf("default backend type = %T, want worktreeBackend", d.backend)
	}
}

// TestBackend_WorktreeRootOptionalWithBackend proves a custom Backend
// lifts the WorktreeRoot requirement, while nil Backend still demands it.
func TestBackend_WorktreeRootOptionalWithBackend(t *testing.T) {
	base := Config{
		Runner:       &shell.Runner{},
		Wrap:         func(s verb.Spec) cli.ActionFunc { return s.Action },
		AllowedOwner: "example-org",
		RepoPath:     func(string) (string, error) { return "/tmp", nil },
		LogRoot:      func() (string, error) { return "/tmp", nil },
	}
	if _, err := New(base); err == nil {
		t.Error("New without WorktreeRoot or Backend should error")
	}
	withBackend := base
	withBackend.Backend = &fakeBackend{name: "container"}
	if _, err := New(withBackend); err != nil {
		t.Errorf("New with a Backend should not require WorktreeRoot, got: %v", err)
	}
}

// TestBackend_DetachedRunUsesBackend drives a headless dispatch through a
// custom backend and pins the lifecycle: Reap, Reserve, Prepare, Spawn.
func TestBackend_DetachedRunUsesBackend(t *testing.T) {
	cwd := t.TempDir()
	b := &fakeBackend{name: "container", prepareCwd: cwd}
	d := newBackendDispatcher(t, b)

	c := parsedHeadlessCmd(t, d, "example-org/example-repo#42")
	if err := d.runDetached(context.Background(), c, detachedSpec{mode: "headless", surface: levelHeadless, prompt: seedPrompt}); err != nil {
		t.Fatalf("runDetached: %v", err)
	}

	if !b.reapCalled {
		t.Error("backend.Reap was not called before dispatch")
	}
	if !b.reserved {
		t.Error("backend.Reserve was not called")
	}
	if b.prepareDry == nil || *b.prepareDry {
		t.Errorf("backend.Prepare dryRun = %v, want false", b.prepareDry)
	}
	if b.spawnPlan == nil {
		t.Fatal("backend.Spawn was not called")
	}
	if b.spawnPlan.Cwd != cwd {
		t.Errorf("SpawnPlan.Cwd = %q, want the prepared cwd %q", b.spawnPlan.Cwd, cwd)
	}
	if b.spawnPlan.Ref == nil || b.spawnPlan.Ref.Number != 42 {
		t.Errorf("SpawnPlan.Ref = %+v, want #42", b.spawnPlan.Ref)
	}
	if !strings.HasSuffix(b.spawnPlan.LogPath, ".log") {
		t.Errorf("SpawnPlan.LogPath = %q, want a .log file", b.spawnPlan.LogPath)
	}
	if b.spawnPlan.Reservation == nil || b.spawnPlan.Reservation.ID() != "slot-1" {
		t.Errorf("SpawnPlan.Reservation = %+v, want the reserved slot", b.spawnPlan.Reservation)
	}
	// A successful spawn hands the reservation to the worker; it must not
	// be released.
	if b.released {
		t.Error("reservation was released after a successful spawn")
	}
}

// TestBackend_SpawnFailureReleasesReservation proves an aborted launch
// frees the slot the backend reserved.
func TestBackend_SpawnFailureReleasesReservation(t *testing.T) {
	b := &fakeBackend{name: "container", prepareCwd: t.TempDir(), spawnErr: errors.New("boom")}
	d := newBackendDispatcher(t, b)

	c := parsedHeadlessCmd(t, d, "example-org/example-repo#42")
	err := d.runDetached(context.Background(), c, detachedSpec{mode: "headless", surface: levelHeadless, prompt: seedPrompt})
	if err == nil {
		t.Fatal("expected spawn failure, got nil")
	}
	if !b.released {
		t.Error("reservation was not released after a failed spawn")
	}
}

// TestBackend_PrepareFailureReleasesReservation proves provisioning
// failure also frees the reserved slot.
func TestBackend_PrepareFailureReleasesReservation(t *testing.T) {
	b := &fakeBackend{name: "container", prepareErr: errors.New("no room")}
	d := newBackendDispatcher(t, b)

	c := parsedHeadlessCmd(t, d, "example-org/example-repo#42")
	if err := d.runDetached(context.Background(), c, detachedSpec{mode: "headless", surface: levelHeadless, prompt: seedPrompt}); err == nil {
		t.Fatal("expected prepare failure, got nil")
	}
	if !b.released {
		t.Error("reservation was not released after a failed prepare")
	}
	if b.spawnPlan != nil {
		t.Error("Spawn must not run after Prepare fails")
	}
}

// TestBackend_DryRunPreparesWithoutReserveOrSpawn proves --dry-run resolves
// via Prepare(dryRun=true) and never reserves or spawns.
func TestBackend_DryRunPreparesWithoutReserveOrSpawn(t *testing.T) {
	b := &fakeBackend{name: "container", prepareCwd: t.TempDir()}
	d := newBackendDispatcher(t, b)

	c := parsedHeadlessCmd(t, d, "--dry-run", "example-org/example-repo#42")
	if err := d.runDetached(context.Background(), c, detachedSpec{mode: "headless", surface: levelHeadless, prompt: seedPrompt}); err != nil {
		t.Fatalf("runDetached dry-run: %v", err)
	}
	if b.prepareDry == nil || !*b.prepareDry {
		t.Errorf("dry-run Prepare dryRun = %v, want true", b.prepareDry)
	}
	if b.reserved {
		t.Error("dry-run must not reserve backend capacity")
	}
	if b.spawnPlan != nil {
		t.Error("dry-run must not spawn")
	}
}
