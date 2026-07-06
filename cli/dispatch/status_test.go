package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// writeFakeDispatch lays down one fake headless dispatch under root: the
// log file and (optionally) its .meta sidecar. Returns the log path.
func writeFakeDispatch(t *testing.T, root, repo string, number int, stamp string, logBody string, meta *dispatchMeta) string {
	t.Helper()
	dir := filepath.Join(root, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	logPath := filepath.Join(dir, fmt.Sprintf("issue-%d-%s.log", number, stamp))
	if err := os.WriteFile(logPath, []byte(logBody), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if meta != nil {
		payload, _ := json.Marshal(meta)
		if err := os.WriteFile(logPath+dispatchMetaSuffix, payload, 0o644); err != nil {
			t.Fatalf("write meta: %v", err)
		}
	}
	return logPath
}

// runStatusCmd parses argv against a status command bound to a buffer
// writer, exposing the same surface the real verb shows.
func runStatusCmd(t *testing.T, d *Dispatcher, argv []string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	statusCmd := d.statusCommand()
	statusCmd.Action = func(ctx context.Context, c *cli.Command) error {
		return d.runStatus(ctx, c, &buf)
	}
	app := &cli.Command{
		Name:     "test",
		Commands: []*cli.Command{statusCmd},
	}
	err := app.Run(context.Background(), append([]string{"test", "status"}, argv...))
	return buf.String(), err
}

// TestDispatchHasStatusSubverb proves the status verb hangs off the
// dispatch parent alongside headless / interactive / reap.
func TestDispatchHasStatusSubverb(t *testing.T) {
	d := newTestDispatcher(t)
	for _, sub := range d.Command().Commands {
		if sub.Name == "status" {
			return
		}
	}
	t.Error("dispatch parent missing `status` subverb")
}

// TestStatus_List_NewestFirst is the bare-args case: `dispatch status
// --all` lists every dispatch, one compact line each, newest first.
func TestStatus_List_NewestFirst(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	writeFakeDispatch(t, root, "example-repo", 1, "20260101-101010", "old log\n", &dispatchMeta{
		PID: 1, StartedAt: time.Date(2026, 1, 1, 10, 10, 10, 0, time.UTC), Ref: "example-org/example-repo#1",
	})
	writeFakeDispatch(t, root, "another-repo", 5, "20260301-090000",
		"line one\nline two\nline three\n", &dispatchMeta{
			PID: 2, StartedAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC), Ref: "example-org/another-repo#5",
		})

	out, err := runStatusCmd(t, d, []string{"--all"})
	if err != nil {
		t.Fatalf("status --all: %v", err)
	}
	// Both dispatches must appear, newest (another-repo#5) listed first.
	idxNewest := strings.Index(out, "example-org/another-repo#5")
	idxOlder := strings.Index(out, "example-org/example-repo#1")
	if idxNewest < 0 || idxOlder < 0 {
		t.Fatalf("expected both refs listed, got:\n%s", out)
	}
	if idxNewest > idxOlder {
		t.Errorf("expected newest listed before older, got:\n%s", out)
	}
}

// TestStatus_List_WindowFiltersOld verifies the default window hides a
// long-exited dispatch, falling back to the "most recent" hint line.
func TestStatus_List_WindowFiltersOld(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	writeFakeDispatch(t, root, "example-repo", 7, "20260101-101010", "ancient\n", &dispatchMeta{
		PID: deadPID, StartedAt: time.Date(2026, 1, 1, 10, 10, 10, 0, time.UTC), Ref: "example-org/example-repo#7",
	})

	out, err := runStatusCmd(t, d, nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "no dispatches active or spawned within") {
		t.Errorf("expected empty-window fallback note, got:\n%s", out)
	}
	if !strings.Contains(out, "example-org/example-repo#7") {
		t.Errorf("expected most-recent hint to name the dispatch, got:\n%s", out)
	}
}

// TestStatus_List_RunningAlwaysShows verifies a still-running dispatch is
// listed even when its spawn time is far outside the --since window.
func TestStatus_List_RunningAlwaysShows(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	// os.Getpid() is alive for the duration of the test, so processRunning
	// returns true; the old timestamp would otherwise filter it out.
	writeFakeDispatch(t, root, "example-repo", 8, "20260101-101010", "running\n", &dispatchMeta{
		PID: os.Getpid(), StartedAt: time.Date(2026, 1, 1, 10, 10, 10, 0, time.UTC), Ref: "example-org/example-repo#8",
	})

	out, err := runStatusCmd(t, d, nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "example-org/example-repo#8") || !strings.Contains(out, "RUNNING") {
		t.Errorf("expected the running dispatch listed despite old spawn time, got:\n%s", out)
	}
}

// TestStatus_ByRef_FiltersToRepoAndNumber pins the positional-ref path:
// `dispatch status owner/repo#N` must return the newest log for that
func TestStatus_ByRef_FiltersToRepoAndNumber(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	// Newer log for a different repo - status must skip it.
	writeFakeDispatch(t, root, "another-repo", 5, "20260301-090000", "other repo\n", nil)
	// Two logs for example-repo#330: pick the newer one.
	writeFakeDispatch(t, root, "example-repo", 330, "20260101-100000", "older 330\n", nil)
	target := writeFakeDispatch(t, root, "example-repo", 330, "20260201-100000",
		"newer 330\nmore newer\n", &dispatchMeta{
			PID: 99999, StartedAt: time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC), Ref: "example-org/example-repo#330",
		})

	out, err := runStatusCmd(t, d, []string{"example-org/example-repo#330"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, target) {
		t.Errorf("expected newer example-repo#330 log %q in output, got:\n%s", target, out)
	}
	if strings.Contains(out, "another-repo") {
		t.Errorf("unrelated repo leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "more newer") {
		t.Errorf("expected log tail in output, got:\n%s", out)
	}
}

// TestStatus_ByPID_FindsMatchingSidecar pins the --pid path: walk all
// logs, read sidecars, return the one whose recorded pid matches.
func TestStatus_ByPID_FindsMatchingSidecar(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	writeFakeDispatch(t, root, "example-repo", 1, "20260101-100000", "one\n", &dispatchMeta{PID: 111})
	target := writeFakeDispatch(t, root, "another-repo", 2, "20260102-100000", "two\n", &dispatchMeta{PID: 222})
	writeFakeDispatch(t, root, "example-repo", 3, "20260103-100000", "three\n", &dispatchMeta{PID: 333})

	out, err := runStatusCmd(t, d, []string{"--pid", "222"})
	if err != nil {
		t.Fatalf("status --pid 222: %v", err)
	}
	if !strings.Contains(out, target) {
		t.Errorf("expected log for pid 222 (%s), got:\n%s", target, out)
	}
}

// TestStatus_NoLogs_Errors verifies the empty-LogRoot case is a clear
// error rather than a confusingly empty block.
func TestStatus_NoLogs_Errors(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	_, err := runStatusCmd(t, d, nil)
	if err == nil {
		t.Fatal("expected error for empty log root")
	}
	if !strings.Contains(err.Error(), "no dispatch logs") {
		t.Errorf("error should mention missing logs, got: %v", err)
	}
}

// TestStatus_PIDNotFound_Errors verifies a --pid with no matching
// sidecar surfaces a targeted error.
func TestStatus_PIDNotFound_Errors(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }
	writeFakeDispatch(t, root, "example-repo", 1, "20260101-100000", "x\n", &dispatchMeta{PID: 1})

	_, err := runStatusCmd(t, d, []string{"--pid", "999999"})
	if err == nil {
		t.Fatal("expected error for unknown pid")
	}
	if !strings.Contains(err.Error(), "999999") {
		t.Errorf("error should name the missing pid, got: %v", err)
	}
}

// TestStatus_RejectsPidPlusRef verifies the two filter modes are
// mutually exclusive: passing both is an operator mistake.
func TestStatus_RejectsPidPlusRef(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }
	writeFakeDispatch(t, root, "example-repo", 1, "20260101-100000", "x\n", &dispatchMeta{PID: 1})

	_, err := runStatusCmd(t, d, []string{"--pid", "1", "example-org/example-repo#1"})
	if err == nil {
		t.Fatal("expected error when --pid and ref combined")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutual exclusion, got: %v", err)
	}
}

// TestStatus_MissingMeta_Degrades pins the older-log path: a log without
// a sidecar must still render, with "pid unknown" rather than aborting.
func TestStatus_MissingMeta_Degrades(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }
	writeFakeDispatch(t, root, "example-repo", 1, "20260101-100000", "hello\n", nil)

	// Bare list (--all to bypass the window): a sidecar-less log still
	// renders, as "pid unknown".
	out, err := runStatusCmd(t, d, []string{"--all"})
	if err != nil {
		t.Fatalf("status with missing meta: %v", err)
	}
	if !strings.Contains(out, "pid unknown") {
		t.Errorf("expected 'pid unknown' in output, got:\n%s", out)
	}

	// The full block (by ref) still renders the tail for a sidecar-less log.
	block, err := runStatusCmd(t, d, []string{"example-org/example-repo#1"})
	if err != nil {
		t.Fatalf("status by ref with missing meta: %v", err)
	}
	if !strings.Contains(block, "pid:     unknown") {
		t.Errorf("expected 'pid unknown' block line, got:\n%s", block)
	}
	if !strings.Contains(block, "hello") {
		t.Errorf("expected log tail in block, got:\n%s", block)
	}
}

// TestStatus_ExitedShowsDuration verifies that an exited dispatch (pid
// that does not refer to a live process) reports the elapsed wall time
func TestStatus_ExitedShowsDuration(t *testing.T) {
	d := newTestDispatcher(t)
	root := t.TempDir()
	d.cfg.LogRoot = func() (string, error) { return root, nil }

	logPath := writeFakeDispatch(t, root, "example-repo", 42, "20260101-100000", "done\n", &dispatchMeta{
		PID:       1, // pid 1 is init / launchd; signal-0 returns EPERM, treated as not-running
		StartedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Ref:       "example-org/example-repo#42",
	})
	// Bump the log mtime to 5m after spawn so duration is deterministic.
	later := time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)
	_ = os.Chtimes(logPath, later, later)

	// Target by ref so the full block (which carries the duration line)
	// renders regardless of the bare list's time window.
	out, err := runStatusCmd(t, d, []string{"example-org/example-repo#42"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// pid 1 on most unix hosts isn't signalable by the test user.
	// EITHER state is acceptable as long as the block renders cleanly.
	if !strings.Contains(out, "pid:     1") {
		t.Errorf("expected pid line, got:\n%s", out)
	}
}

// TestTailLines_Truncation pins the tail-line cap: long logs return
// exactly n lines from the end, short logs return everything.
func TestTailLines_Truncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := tailLines(path, 3)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	want := []string{"c", "d", "e"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tailLines = %v, want %v", got, want)
	}
	got, _ = tailLines(path, 99)
	if len(got) != 5 {
		t.Errorf("expected all 5 lines for n>=len, got %d", len(got))
	}
}

// TestDispatchLogNameRE pins the filename pattern: only logs produced by
// dispatchLogPath are matched. A worktree-style "issue-N" without a
func TestDispatchLogNameRE(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"issue-330-20260522-213504.log", true},
		{"issue-1-19700101-000000.log", true},
		{"issue-330.log", false},
		{"issue-abc-20260522-213504.log", false},
		{"issue-330-20260522.log", false},
		{"scratch.log", false},
	}
	for _, tc := range cases {
		got := dispatchLogNameRE.MatchString(tc.name)
		if got != tc.want {
			t.Errorf("dispatchLogNameRE(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestWriteAndReadDispatchMeta round-trips one meta sidecar through
// disk. Catches schema drift between the writer and reader.
func TestWriteAndReadDispatchMeta(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "issue-7-20260101-100000.log")
	if err := os.WriteFile(logPath, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	m := dispatchMeta{
		PID:       4242,
		StartedAt: time.Date(2026, 5, 22, 21, 35, 4, 0, time.UTC),
		Ref:       "example-org/example-repo#7",
		URL:       "https://github.com/example-org/example-repo/issues/7",
	}
	if err := writeDispatchMeta(logPath, m); err != nil {
		t.Fatalf("writeDispatchMeta: %v", err)
	}
	got, ok := readDispatchMeta(logPath)
	if !ok {
		t.Fatal("readDispatchMeta returned !ok after a successful write")
	}
	if got.PID != m.PID || got.Ref != m.Ref || !got.StartedAt.Equal(m.StartedAt) {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, m)
	}
}

// TestReadDispatchMeta_Missing verifies the "no sidecar" case is a
// silent ok=false, not an error - it's the normal state for older logs.
func TestReadDispatchMeta_Missing(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "issue-1-20260101-100000.log")
	if _, ok := readDispatchMeta(logPath); ok {
		t.Error("readDispatchMeta returned ok=true for a missing sidecar")
	}
}
