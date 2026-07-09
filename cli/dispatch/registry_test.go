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

// deadPID stands in for a gone process: far above any real pid, so it
// always reads dead. Never pid 1 - it is init, alive in a container.
const deadPID = 2_000_000_000

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

// TestRegistryPublicAPI exercises NewRegistry/Active/Conflicts.
func TestRegistryPublicAPI(t *testing.T) {
	root := t.TempDir()
	livePID := os.Getpid()
	writeFakeDispatch(t, root, "example-repo", 119, "20260527-130000", "", &dispatchMeta{
		PID:          livePID,
		StartedAt:    time.Unix(1700000000, 0).UTC(),
		Ref:          issueRefText("example-repo", 119),
		PathsClaimed: []string{"/work/skills/tooling-foo"},
	})
	r := NewRegistry(root)
	active, err := r.Active()
	if err != nil || len(active) != 1 || active[0].PID != livePID {
		t.Fatalf("Active: want 1 sidequest, got %v err=%v", active, err)
	}
	conflicts, err := r.Conflicts("/work/skills/tooling-foo/SKILL.md")
	if err != nil || len(conflicts) != 1 || conflicts[0].Reason != "ancestor" {
		t.Fatalf("Conflicts ancestor: want 1 reason=ancestor, got %v err=%v", conflicts, err)
	}
	clean, err := r.Conflicts("/work/elsewhere")
	if err != nil || len(clean) != 0 {
		t.Fatalf("Conflicts clean: want empty, got %v err=%v", clean, err)
	}
	if nilR := (*Registry)(nil); true {
		got, err := nilR.Active()
		if err != nil || got != nil {
			t.Fatalf("nil registry Active: want (nil,nil), got (%v,%v)", got, err)
		}
	}
	emptyRoot, err := NewRegistry("").Active()
	if err != nil || emptyRoot != nil {
		t.Fatalf("empty logRoot: want (nil,nil), got (%v,%v)", emptyRoot, err)
	}
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
	// A clearly-dead pid: a huge value no live process will hold. pid 1 is
	// wrong here - it is init, alive in a container, so the filter keeps it.
	writeFakeDispatch(t, root, "example-repo", 99, "20260527-130001", "", &dispatchMeta{
		PID:       deadPID,
		StartedAt: time.Now().UTC(),
		Ref:       issueRefText("example-repo", 99),
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
	writeFakeDispatch(t, root, "example-repo", 119, "20260527-130000", "", &dispatchMeta{
		PID:           livePID,
		StartedAt:     time.Unix(1700000000, 0).UTC(),
		Ref:           issueRefText("example-repo", 119),
		ParentSession: "parent-e935",
		PathsClaimed:  []string{"example-repo/.agents/skills/tooling-mcp-servers"},
	})
	out, err := runRegistryCmd(t, d, []string{"list"})
	if err != nil {
		t.Fatalf("registry list: %v", err)
	}
	for _, want := range []string{
		issueRefText("example-repo", 119),
		"parent-e935",
		"example-repo/.agents/skills/tooling-mcp-servers",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got %q", want, out)
		}
	}
}

// runRegistryCheckCmd parses argv against a check command bound to a
// captured writer.
func runRegistryCheckCmd(t *testing.T, d *Dispatcher, argv []string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	regCmd := d.registryCommand()
	for _, sub := range regCmd.Commands {
		if sub.Name == "check" {
			sub.Action = func(ctx context.Context, c *cli.Command) error {
				return d.runRegistryCheck(ctx, c, &buf)
			}
		}
	}
	app := &cli.Command{
		Name:     "test",
		Commands: []*cli.Command{regCmd},
	}
	err := app.Run(context.Background(), append([]string{"test", "registry", "check"}, argv...))
	return buf.String(), err
}

// TestParseClaimsAbsolutizes proves relative claims resolve against
// cwd and absolute claims pass through, with empty input returning nil.
func TestParseClaimsAbsolutizes(t *testing.T) {
	cwd, _ := os.Getwd()
	got, err := parseClaims("foo/bar, /abs/path , , baz")
	if err != nil {
		t.Fatalf("parseClaims: %v", err)
	}
	want := []string{
		cwd + "/foo/bar",
		"/abs/path",
		cwd + "/baz",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d claims, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim[%d]: want %q got %q", i, want[i], got[i])
		}
	}
	empty, err := parseClaims("")
	if err != nil || empty != nil {
		t.Fatalf("empty input: want (nil, nil), got (%v, %v)", empty, err)
	}
}

// TestRegistryCheckCleanReturnsZero proves a path with no overlapping
// claim exits 0 with empty stdout.
func TestRegistryCheckCleanReturnsZero(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	writeFakeDispatch(t, root, "example-repo", 119, "20260527-130000", "", &dispatchMeta{
		PID:          os.Getpid(),
		StartedAt:    time.Unix(1700000000, 0).UTC(),
		Ref:          issueRefText("example-repo", 119),
		PathsClaimed: []string{"/work/foo"},
	})
	out, err := runRegistryCheckCmd(t, d, []string{"/work/bar"})
	if err != nil {
		t.Fatalf("check clean: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty stdout on clean check, got %q", out)
	}
}

// TestRegistryCheckExactConflict proves an identical path match exits
// non-zero and names the claim.
func TestRegistryCheckExactConflict(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	writeFakeDispatch(t, root, "example-repo", 119, "20260527-130000", "", &dispatchMeta{
		PID:          os.Getpid(),
		StartedAt:    time.Unix(1700000000, 0).UTC(),
		Ref:          issueRefText("example-repo", 119),
		PathsClaimed: []string{"/work/foo"},
	})
	out, err := runRegistryCheckCmd(t, d, []string{"/work/foo"})
	if err == nil {
		t.Fatalf("expected non-zero on conflict, got nil err")
	}
	if !strings.Contains(out, "reason=exact") {
		t.Fatalf("expected reason=exact in output, got %q", out)
	}
}

// TestRegistryCheckAncestorConflict proves a claim on a directory
// conflicts with a descendant file.
func TestRegistryCheckAncestorConflict(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	writeFakeDispatch(t, root, "example-repo", 119, "20260527-130000", "", &dispatchMeta{
		PID:          os.Getpid(),
		StartedAt:    time.Unix(1700000000, 0).UTC(),
		Ref:          issueRefText("example-repo", 119),
		PathsClaimed: []string{"/work/skills/tooling-foo"},
	})
	out, err := runRegistryCheckCmd(t, d, []string{"/work/skills/tooling-foo/SKILL.md"})
	if err == nil {
		t.Fatalf("expected non-zero on ancestor conflict, got nil err")
	}
	if !strings.Contains(out, "reason=ancestor") {
		t.Fatalf("expected reason=ancestor in output, got %q", out)
	}
}

// TestRegistryCheckAncestorBoundary proves that "/work/skills/tooling-foo"
// claimed does NOT conflict with a sibling "/work/skills/tooling-foobar".
func TestRegistryCheckAncestorBoundary(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	writeFakeDispatch(t, root, "example-repo", 119, "20260527-130000", "", &dispatchMeta{
		PID:          os.Getpid(),
		StartedAt:    time.Unix(1700000000, 0).UTC(),
		Ref:          issueRefText("example-repo", 119),
		PathsClaimed: []string{"/work/skills/tooling-foo"},
	})
	out, err := runRegistryCheckCmd(t, d, []string{"/work/skills/tooling-foobar"})
	if err != nil {
		t.Fatalf("expected clean on sibling-prefix path, got err: %v, out=%q", err, out)
	}
}

// TestRegistryListJSON proves --json emits a parseable array with the
// expected fields rather than the human-readable text format.
func TestRegistryListJSON(t *testing.T) {
	d := newTestDispatcher(t)
	root, _ := d.cfg.LogRoot()
	livePID := os.Getpid()
	writeFakeDispatch(t, root, "example-repo", 119, "20260527-130000", "", &dispatchMeta{
		PID:       livePID,
		StartedAt: time.Unix(1700000000, 0).UTC(),
		Ref:       issueRefText("example-repo", 119),
	})
	out, err := runRegistryCmd(t, d, []string{"list", "--json"})
	if err != nil {
		t.Fatalf("registry list --json: %v", err)
	}
	var got []Sidequest
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0].PID != livePID || got[0].Ref != issueRefText("example-repo", 119) {
		t.Fatalf("unexpected entries: %+v", got)
	}
}
