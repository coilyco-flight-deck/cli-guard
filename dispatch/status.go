package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
)

// status.go owns the read-only verb that bundles the two questions an
// operator asks about a headless dispatch: is it still running, and what
// has it printed lately. Discovery uses the log filename shape
// dispatchLogPath produces; pid + spawn time are persisted in a JSON
// sidecar at <logPath>.meta written by writeDispatchMeta right after
// runHeadless spawns the child. See coilysiren/cli-guard#90.

// dispatchStatusTailLines is the default tail size, matching the issue
// spec. Overridable per call via --tail.
const dispatchStatusTailLines = 15

// dispatchMeta is the sidecar JSON written next to a headless dispatch
// log so `dispatch status` can render pid + spawn time + ref without
// scraping ps output or re-parsing the log. Field tags are the wire
// format - rename with care.
type dispatchMeta struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Ref       string    `json:"ref"`
	URL       string    `json:"url"`
}

// dispatchMetaSuffix is the suffix appended to a headless log path to
// derive its sidecar metadata file. Stable so status can locate it from
// the log path alone.
const dispatchMetaSuffix = ".meta"

func metaPathFor(logPath string) string { return logPath + dispatchMetaSuffix }

// writeDispatchMeta writes the sidecar JSON for one headless dispatch.
// Best-effort by intent: a missing or corrupted meta degrades status
// output to "pid unknown" but never breaks the dispatch itself, so the
// caller treats failures as soft.
func writeDispatchMeta(logPath string, m dispatchMeta) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal dispatch meta: %w", err)
	}
	if err := os.WriteFile(metaPathFor(logPath), payload, 0o644); err != nil {
		return fmt.Errorf("write dispatch meta %s: %w", metaPathFor(logPath), err)
	}
	return nil
}

// readDispatchMeta reads the sidecar JSON; ok=false on any read or parse
// error, since a missing meta is a normal state for older logs.
func readDispatchMeta(logPath string) (dispatchMeta, bool) {
	b, err := os.ReadFile(metaPathFor(logPath))
	if err != nil {
		return dispatchMeta{}, false
	}
	var m dispatchMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return dispatchMeta{}, false
	}
	return m, true
}

// dispatchLogNameRE matches the filename shape produced by
// dispatchLogPath: issue-<N>-<YYYYMMDD-HHMMSS>.log.
var dispatchLogNameRE = regexp.MustCompile(`^issue-(\d+)-(\d{8}-\d{6})\.log$`)

// logEntry is one discovered headless dispatch log.
type logEntry struct {
	Repo    string
	Number  int
	Path    string
	Stamp   time.Time
	ModTime time.Time
}

// parseLogStamp parses the timestamp embedded in a dispatch log filename
// as a local-time value, matching the format dispatchLogPath writes.
func parseLogStamp(s string) (time.Time, error) {
	return time.ParseInLocation("20060102-150405", s, time.Local)
}

// walkDispatchLogs lists every headless dispatch log under LogRoot,
// newest first by embedded timestamp. filterRepo and filterNumber narrow
// the result; pass "" / 0 to skip a filter.
func (d *Dispatcher) walkDispatchLogs(filterRepo string, filterNumber int) ([]logEntry, error) {
	root, err := d.cfg.LogRoot()
	if err != nil {
		return nil, err
	}
	repos, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read log root %s: %w", root, err)
	}
	var out []logEntry
	for _, repoEntry := range repos {
		if !repoEntry.IsDir() {
			continue
		}
		if filterRepo != "" && repoEntry.Name() != filterRepo {
			continue
		}
		out = append(out, walkRepoLogs(root, repoEntry.Name(), filterNumber)...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Stamp.After(out[j].Stamp)
	})
	return out, nil
}

// walkRepoLogs is the per-repo half of walkDispatchLogs. Pulled out so
// the outer walk stays under the cognitive-complexity threshold.
func walkRepoLogs(root, repo string, filterNumber int) []logEntry {
	files, err := os.ReadDir(filepath.Join(root, repo))
	if err != nil {
		return nil
	}
	var out []logEntry
	for _, f := range files {
		m := dispatchLogNameRE.FindStringSubmatch(f.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if filterNumber > 0 && n != filterNumber {
			continue
		}
		stamp, err := parseLogStamp(m[2])
		if err != nil {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		out = append(out, logEntry{
			Repo:    repo,
			Number:  n,
			Path:    filepath.Join(root, repo, f.Name()),
			Stamp:   stamp,
			ModTime: info.ModTime(),
		})
	}
	return out
}

// processRunning reports whether pid points at a live process via the
// signal-0 trick. Note: after the original child exits, its pid can be
// reused; status will report RUNNING in that case. The risk is low
// enough for the audience that we accept it rather than persist start
// time + /proc cross-check.
func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// tailLines returns the last n lines of path. Strips the trailing
// newline so a final blank line doesn't waste a slot.
func tailLines(path string, n int) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= n {
		return lines, nil
	}
	return lines[len(lines)-n:], nil
}

// formatRelative renders a duration as "Xs ago" / "Xm ago" / "Xh ago" /
// "Xd ago" - precision tuned for the at-a-glance status header.
func formatRelative(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// formatDuration is the exited-dispatch counterpart to formatRelative:
// renders elapsed wall time as "Xs" / "XmYYs" / "XhYYm".
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := m / 60
	m %= 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

// statusCommand wires the read-only status verb. Not wrapped through
// d.cfg.Wrap: status reads disk and signals pid 0, no privileged ops -
// the reap sibling follows the same unwrapped pattern.
func (d *Dispatcher) statusCommand() *cli.Command {
	bin := d.cfg.BinaryName
	return &cli.Command{
		Name:      "status",
		Usage:     "Show pid + log tail for a headless dispatch.",
		ArgsUsage: "[owner/repo#N | issue-url]",
		Description: fmt.Sprintf(`status finds a headless dispatch log and prints a one-block summary:
the issue ref, pid + RUNNING/EXITED state, spawn time, log path, and
the last %d lines of the log.

  %s dispatch status                       most recent dispatch across any repo
  %s dispatch status <owner/repo#N>        most recent dispatch for that issue
  %s dispatch status --pid <pid>           dispatch whose sidecar meta matches this pid

Pass --follow to tail -f the log, exiting when the pid exits.

pid + spawn time come from a <logPath>.meta sidecar written at
dispatch headless time. Older logs without a sidecar render with
"pid unknown".`, dispatchStatusTailLines, bin, bin, bin),
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "pid",
				Usage: "find the dispatch whose recorded pid matches this value",
			},
			&cli.BoolFlag{
				Name:  "follow",
				Usage: "tail -f the log, exiting when the pid exits",
			},
			&cli.IntFlag{
				Name:  "tail",
				Usage: "number of log lines to show in the trailing block",
				Value: dispatchStatusTailLines,
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return d.runStatus(ctx, c, os.Stdout)
		},
	}
}

// pickStatusEntry resolves the operator's intent (positional ref, --pid,
// or "newest of anything") to a single log entry + its meta. Routes to
// the per-mode helpers below to keep each branch under the cyclomatic
// threshold.
func (d *Dispatcher) pickStatusEntry(args []string, pidFilter int) (*logEntry, dispatchMeta, bool, error) {
	switch {
	case pidFilter > 0:
		return d.pickStatusEntryByPID(pidFilter)
	case len(args) == 1:
		return d.pickStatusEntryByRef(args[0])
	default:
		return d.pickStatusEntryNewest()
	}
}

func (d *Dispatcher) pickStatusEntryByPID(pid int) (*logEntry, dispatchMeta, bool, error) {
	entries, err := d.walkDispatchLogs("", 0)
	if err != nil {
		return nil, dispatchMeta{}, false, err
	}
	for i := range entries {
		if m, ok := readDispatchMeta(entries[i].Path); ok && m.PID == pid {
			return &entries[i], m, true, nil
		}
	}
	return nil, dispatchMeta{}, false, fmt.Errorf("no dispatch log found for pid %d", pid)
}

func (d *Dispatcher) pickStatusEntryByRef(raw string) (*logEntry, dispatchMeta, bool, error) {
	ref, err := parseIssueRef(raw)
	if err != nil {
		return nil, dispatchMeta{}, false, err
	}
	entries, err := d.walkDispatchLogs(ref.Repo, ref.Number)
	if err != nil {
		return nil, dispatchMeta{}, false, err
	}
	if len(entries) == 0 {
		return nil, dispatchMeta{}, false, fmt.Errorf("no dispatch log found for %s", ref)
	}
	m, ok := readDispatchMeta(entries[0].Path)
	return &entries[0], m, ok, nil
}

func (d *Dispatcher) pickStatusEntryNewest() (*logEntry, dispatchMeta, bool, error) {
	entries, err := d.walkDispatchLogs("", 0)
	if err != nil {
		return nil, dispatchMeta{}, false, err
	}
	if len(entries) == 0 {
		return nil, dispatchMeta{}, false, fmt.Errorf("no dispatch logs found under log root")
	}
	m, ok := readDispatchMeta(entries[0].Path)
	return &entries[0], m, ok, nil
}

// runStatus is the verb action. Split from the cli.Command builder so
// tests can drive it with a captured writer.
func (d *Dispatcher) runStatus(ctx context.Context, c *cli.Command, w io.Writer) error {
	args := c.Args().Slice()
	if len(args) > 1 {
		return fmt.Errorf("dispatch status: pass at most one issue reference (got %d args)", len(args))
	}
	pidFilter := c.Int("pid")
	if pidFilter > 0 && len(args) > 0 {
		return fmt.Errorf("dispatch status: --pid and a positional ref are mutually exclusive")
	}

	entry, meta, hasMeta, err := d.pickStatusEntry(args, pidFilter)
	if err != nil {
		return fmt.Errorf("dispatch status: %w", err)
	}

	tailN := c.Int("tail")
	if tailN <= 0 {
		tailN = dispatchStatusTailLines
	}
	printStatusBlock(w, entry, meta, hasMeta, tailN, d.cfg.AllowedOwner)

	if c.Bool("follow") {
		return followLog(ctx, w, entry.Path, meta.PID, hasMeta)
	}
	return nil
}

// statusRef formats the operator-facing ref string. Prefers the value
// captured in the sidecar (which knows the canonical owner) and falls
// back to reconstructing it from the discovered path + AllowedOwner.
func statusRef(entry *logEntry, meta dispatchMeta, hasMeta bool, allowedOwner string) string {
	if hasMeta && meta.Ref != "" {
		return meta.Ref
	}
	return fmt.Sprintf("%s/%s#%d", allowedOwner, entry.Repo, entry.Number)
}

// statusSpawn picks the most authoritative spawn time we have: meta if
// the sidecar was written, otherwise the timestamp embedded in the log
// filename.
func statusSpawn(entry *logEntry, meta dispatchMeta, hasMeta bool) time.Time {
	if hasMeta && !meta.StartedAt.IsZero() {
		return meta.StartedAt.Local()
	}
	return entry.Stamp
}

// printPidLine emits the pid + RUNNING/EXITED state line, plus the
// duration line for an exited dispatch. Pulled out so printStatusBlock
// stays below the cyclomatic threshold.
func printPidLine(w io.Writer, meta dispatchMeta, hasMeta bool, spawn time.Time, modTime time.Time) {
	if !hasMeta {
		_, _ = fmt.Fprintf(w, "pid:     unknown (no %s sidecar)\n", dispatchMetaSuffix)
		return
	}
	running := processRunning(meta.PID)
	state := "EXITED"
	if running {
		state = "RUNNING"
	}
	_, _ = fmt.Fprintf(w, "pid:     %d   %s\n", meta.PID, state)
	if running || spawn.IsZero() {
		return
	}
	dur := modTime.Sub(spawn)
	if dur > 0 {
		_, _ = fmt.Fprintf(w, "duration: %s\n", formatDuration(dur))
	}
}

// printTail emits the trailing "tail:" header plus the captured lines.
// Read failures surface inline rather than aborting the whole block - a
// stale or rotated log shouldn't lose the rest of the status.
func printTail(w io.Writer, path string, n int) {
	lines, err := tailLines(path, n)
	if err != nil {
		_, _ = fmt.Fprintf(w, "tail:    (cannot read log: %v)\n", err)
		return
	}
	_, _ = fmt.Fprintln(w, "tail:")
	for _, l := range lines {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
}

// printStatusBlock writes the human-readable status summary. allowedOwner
// is used to format the ref for logs whose sidecar didn't record one.
func printStatusBlock(w io.Writer, entry *logEntry, meta dispatchMeta, hasMeta bool, tailN int, allowedOwner string) {
	_, _ = fmt.Fprintf(w, "ref:     %s\n", statusRef(entry, meta, hasMeta, allowedOwner))
	spawn := statusSpawn(entry, meta, hasMeta)
	printPidLine(w, meta, hasMeta, spawn, entry.ModTime)
	if !spawn.IsZero() {
		_, _ = fmt.Fprintf(w, "spawned: %s (%s)\n",
			spawn.Format("2006-01-02T15:04:05"),
			formatRelative(time.Since(spawn)))
	}
	_, _ = fmt.Fprintf(w, "log:     %s\n", entry.Path)
	printTail(w, entry.Path, tailN)
}

// followPollInterval is how often followLog polls the log file for new
// bytes and re-checks the pid. Tight enough to feel like tail -f.
const followPollInterval = 500 * time.Millisecond

// drainNewBytes copies all currently-available bytes from f to w. EOF /
// zero-read is the normal "no more data right now" exit; only true read
// errors are surfaced.
func drainNewBytes(f *os.File, w io.Writer, buf []byte) error {
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if readErr == io.EOF || n == 0 {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("dispatch status --follow: read log: %w", readErr)
		}
	}
}

// followLog tails the log to w until the recorded pid exits or ctx is
// cancelled. Without havePID it tails forever (until ctx cancel) since
// there is no exit signal to wait on.
func followLog(ctx context.Context, w io.Writer, path string, pid int, havePID bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("dispatch status --follow: open log: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("dispatch status --follow: seek log: %w", err)
	}
	buf := make([]byte, 4096)
	ticker := time.NewTicker(followPollInterval)
	defer ticker.Stop()
	for {
		if err := drainNewBytes(f, w, buf); err != nil {
			return err
		}
		if havePID && !processRunning(pid) {
			_, _ = fmt.Fprintf(w, "\n[dispatch status: pid %d exited]\n", pid)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
