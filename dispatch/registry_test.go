package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// runRegistryCmd parses argv against a registry command bound to a
// captured writer, mirroring runStatusCmd in status_test.go.
func runRegistryCmd(t *testing.T, d *Dispatcher, argv []string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	regCmd := d.registryCommand()
	regCmd.Action = func(ctx context.Context, c *cli.Command) error {
		return d.runRegistryList(ctx, c, &buf)
	}
	for _, sub := range regCmd.Commands {
		if sub.Name == "list" {
			sub.Action = func(ctx context.Context, c *cli.Command) error {
				return d.runRegistryList(ctx, c, &buf)
			}
		}
	}
	app := &cli.Command{
		Name:     "test",
		Commands: []*cli.Command{regCmd},
	}
	err := app.Run(context.Background(), append([]string{"test", "registry"}, argv...))
	return buf.String(), err
}

// TestDispatchHasRegistrySubverb proves registry hangs off the dispatch
// umbrella alongside the existing maintenance verbs.
func TestDispatchHasRegistrySubverb(t *testing.T) {
	d := newTestDispatcher(t)
	for _, sub := range d.Command().Commands {
		if sub.Name == "registry" {
			return
		}
	}
	t.Fatalf("registry subverb not wired into dispatch command tree")
}

// TestRegistryListEmpty proves an empty log root renders the no-entries
// line rather than erroring.
func TestRegistryListEmpty(t *testing.T) {
	d := newTestDispatcher(t)
	out, err := runRegistryCmd(t, d, []string{"list"})
	if err != nil {
		t.Fatalf("registry list: %v", err)
	}
	if !strings.Contains(out, "no active sidequests") {
		t.Fatalf("expected 'no active sidequests', got %q", out)
	}
}

// TestRegistryListFiltersDeadPIDs proves a recorded meta with a pid
// that no longer exists on this host does not appear in the list.
func TestRegistryListFiltersDeadPIDs(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	writeFakeDispatch(t, root, "coily", 99, "20260527-130000", "", &dispatchMeta{
		PID:           1, // init - alive but not ours; harmless. Use a definitely-dead pid instead.
		StartedAt:     time.Now().UTC(),
		Ref:           "coilysiren/coily#99",
		ParentSession: "parent-abcd",
	})
	// Overwrite with a clearly-dead pid: pick a huge value unlikely to exist.
	writeFakeDispatch(t, root, "coily", 99, "20260527-130001", "", &dispatchMeta{
		PID:       2_000_000_000,
		StartedAt: time.Now().UTC(),
		Ref:       "coilysiren/coily#99",
	})
	out, err := runRegistryCmd(t, d, []string{"list"})
	if err != nil {
		t.Fatalf("registry list: %v", err)
	}
	if !strings.Contains(out, "no active sidequests") {
		t.Fatalf("expected dead-pid entry to be filtered out, got %q", out)
	}
}

// TestRegistryListIncludesAliveSidequest proves a live PID renders.
func TestRegistryListIncludesAliveSidequest(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	livePID := os.Getpid()
	writeFakeDispatch(t, root, "coily", 119, "20260527-130000", "", &dispatchMeta{
		PID:           livePID,
		StartedAt:     time.Unix(1700000000, 0).UTC(),
		Ref:           "coilysiren/coily#119",
		ParentSession: "parent-e935",
		PathsClaimed:  []string{"agentic-os-kai/.agents/skills/tooling-mcp-servers"},
	})
	out, err := runRegistryCmd(t, d, []string{"list"})
	if err != nil {
		t.Fatalf("registry list: %v", err)
	}
	for _, want := range []string{
		"coilysiren/coily#119",
		"parent-e935",
		"agentic-os-kai/.agents/skills/tooling-mcp-servers",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got %q", want, out)
		}
	}
}

// TestRegistryListJSON proves --json emits a parseable array with the
// expected fields rather than the human-readable text format.
func TestRegistryListJSON(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	livePID := os.Getpid()
	writeFakeDispatch(t, root, "coily", 119, "20260527-130000", "", &dispatchMeta{
		PID:       livePID,
		StartedAt: time.Unix(1700000000, 0).UTC(),
		Ref:       "coilysiren/coily#119",
	})
	out, err := runRegistryCmd(t, d, []string{"list", "--json"})
	if err != nil {
		t.Fatalf("registry list --json: %v", err)
	}
	var got []registryEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0].PID != livePID || got[0].Ref != "coilysiren/coily#119" {
		t.Fatalf("unexpected entries: %+v", got)
	}
}
