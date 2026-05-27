package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// registry.go is the active-sidequests view over dispatchMeta sidecars.
// See coilysiren/cli-guard#20 and coilysiren/coily#119.

// registryEntry is one active sidequest as rendered to operators.
type registryEntry struct {
	PID           int       `json:"pid"`
	Ref           string    `json:"ref"`
	URL           string    `json:"url,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	ParentSession string    `json:"parent_session,omitempty"`
	PathsClaimed  []string  `json:"paths_claimed,omitempty"`
	LogPath       string    `json:"log_path"`
}

// registryCommand wires the read-only registry verb. Not Wrap'd: list
// signals pid 0 and reads disk, no privileged ops.
func (d *Dispatcher) registryCommand() *cli.Command {
	bin := d.cfg.BinaryName
	return &cli.Command{
		Name:  "registry",
		Usage: "List active sidequests (headless dispatches whose pid is alive).",
		Description: fmt.Sprintf(`registry surfaces still-running headless dispatches so a parent
Claude session or sibling sidequest can see who is currently editing
what before writing shared paths.

  %s dispatch registry list        list active sidequests (default if no subverb)
  %s dispatch registry list --json emit JSON, one object per active sidequest

A sidequest is "active" when its recorded pid still responds to signal
0 on this host. Exited sidequests stay on disk as .meta sidecars until
the surrounding log rotation prunes them; registry hides them.`, bin, bin),
		Commands: []*cli.Command{
			d.registryListCommand(),
			d.registryCheckCommand(),
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return d.runRegistryList(ctx, c, os.Stdout)
		},
	}
}

func (d *Dispatcher) registryListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "Print every active sidequest, oldest first.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit JSON, one object per active sidequest",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return d.runRegistryList(ctx, c, os.Stdout)
		},
	}
}

// runRegistryList walks LogRoot, filters to alive PIDs, and renders.
func (d *Dispatcher) runRegistryList(_ context.Context, c *cli.Command, w io.Writer) error {
	entries, err := d.collectActiveSidequests()
	if err != nil {
		return err
	}
	if c.Bool("json") {
		return writeJSON(w, entries)
	}
	return writeRegistryText(w, entries)
}

// collectActiveSidequests returns dispatchMetas with alive PIDs, oldest first.
func (d *Dispatcher) collectActiveSidequests() ([]registryEntry, error) {
	logs, err := d.walkDispatchLogs("", 0)
	if err != nil {
		return nil, err
	}
	out := make([]registryEntry, 0, len(logs))
	for _, e := range logs {
		m, ok := readDispatchMeta(e.Path)
		if !ok {
			continue
		}
		if !processRunning(m.PID) {
			continue
		}
		out = append(out, registryEntry{
			PID:           m.PID,
			Ref:           m.Ref,
			URL:           m.URL,
			StartedAt:     m.StartedAt,
			ParentSession: m.ParentSession,
			PathsClaimed:  m.PathsClaimed,
			LogPath:       e.Path,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// writeJSON emits indented JSON. Used for both list and check output.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// parseClaims absolutizes a comma-separated --claims value.
// Relative paths join against cwd at dispatch time; empty input → nil.
func parseClaims(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve cwd for relative claims: %w", err)
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		out = append(out, filepath.Clean(abs))
	}
	return out, nil
}

// claimConflict is one (sidequest, claim) pair overlapping the query.
type claimConflict struct {
	PID    int    `json:"pid"`
	Ref    string `json:"ref"`
	Claim  string `json:"claim"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (d *Dispatcher) registryCheckCommand() *cli.Command {
	return &cli.Command{
		Name:      "check",
		Usage:     "Check whether a path is claimed by an active sidequest.",
		ArgsUsage: "<path>",
		Description: `check resolves <path> to an absolute path (relative paths join against
cwd), then walks active sidequests. Exit 0 with no output means no
conflict. Exit non-zero with one line per conflict means at least one
active sidequest has claimed an identical or ancestor path.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit JSON, one object per conflict",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return d.runRegistryCheck(ctx, c, os.Stdout)
		},
	}
}

func (d *Dispatcher) runRegistryCheck(_ context.Context, c *cli.Command, w io.Writer) error {
	args := c.Args().Slice()
	if len(args) != 1 {
		return fmt.Errorf("dispatch registry check: pass exactly one path (got %d args)", len(args))
	}
	abs := args[0]
	if !filepath.IsAbs(abs) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)
	entries, err := d.collectActiveSidequests()
	if err != nil {
		return err
	}
	conflicts := findConflicts(abs, entries)
	if c.Bool("json") {
		if err := writeJSON(w, conflicts); err != nil {
			return err
		}
	} else {
		for _, cf := range conflicts {
			if _, err := fmt.Fprintf(w, "pid=%d ref=%s claim=%s reason=%s\n", cf.PID, cf.Ref, cf.Claim, cf.Reason); err != nil {
				return err
			}
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("dispatch registry check: %d conflict(s) for %s", len(conflicts), abs)
	}
	return nil
}

// findConflicts returns every (sidequest, claim) overlap for path.
func findConflicts(path string, entries []registryEntry) []claimConflict {
	var out []claimConflict
	for _, e := range entries {
		for _, claim := range e.PathsClaimed {
			reason := matchReason(path, claim)
			if reason == "" {
				continue
			}
			out = append(out, claimConflict{
				PID: e.PID, Ref: e.Ref, Claim: claim, Path: path, Reason: reason,
			})
		}
	}
	return out
}

func matchReason(path, claim string) string {
	if path == claim {
		return "exact"
	}
	if strings.HasPrefix(path, claim+string(filepath.Separator)) {
		return "ancestor"
	}
	return ""
}

func writeRegistryText(w io.Writer, entries []registryEntry) error {
	if len(entries) == 0 {
		_, err := fmt.Fprintln(w, "no active sidequests")
		return err
	}
	for _, e := range entries {
		parent := e.ParentSession
		if parent == "" {
			parent = "-"
		}
		claims := "-"
		if len(e.PathsClaimed) > 0 {
			claims = strings.Join(e.PathsClaimed, ",")
		}
		if _, err := fmt.Fprintf(w, "pid=%d ref=%s started=%s parent=%s claims=%s log=%s\n",
			e.PID, e.Ref, e.StartedAt.UTC().Format(time.RFC3339), parent, claims, e.LogPath,
		); err != nil {
			return err
		}
	}
	return nil
}
