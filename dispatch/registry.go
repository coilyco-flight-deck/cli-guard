package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
		return writeRegistryJSON(w, entries)
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

func writeRegistryJSON(w io.Writer, entries []registryEntry) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
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
