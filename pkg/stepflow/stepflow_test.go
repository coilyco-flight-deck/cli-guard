package stepflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// fakeLeaf is the minimal Leaf a transport-free test needs: a name.
type fakeLeaf string

func (f fakeLeaf) Label() string { return string(f) }

// fakeRunner scripts step responses by leaf label and records the fire order,
// with optional per-label failures for Fire and Sample.
type fakeRunner struct {
	responses  map[string]any
	failOn     map[string]bool
	sampleErrs int // Sample errors to return before responding normally
	fired      []string
	resolved   map[string][]string
}

func (f *fakeRunner) record(leaf Leaf, args []guardfile.ArgBind, resolve Resolve) error {
	f.fired = append(f.fired, leaf.Label())
	if f.resolved == nil {
		f.resolved = map[string][]string{}
	}
	for _, a := range args {
		v, err := resolve(a.Value)
		if err != nil {
			return err
		}
		f.resolved[leaf.Label()] = append(f.resolved[leaf.Label()], v)
	}
	return nil
}

func (f *fakeRunner) Fire(_ context.Context, _ *cli.Command, leaf Leaf, args []guardfile.ArgBind, resolve Resolve) (any, []byte, error) {
	if err := f.record(leaf, args, resolve); err != nil {
		return nil, nil, err
	}
	if f.failOn[leaf.Label()] {
		return nil, nil, errors.New("fake: " + leaf.Label() + " failed")
	}
	return f.responses[leaf.Label()], []byte("{}"), nil
}

func (f *fakeRunner) Sample(ctx context.Context, c *cli.Command, leaf Leaf, args []guardfile.ArgBind, resolve Resolve) (any, []byte, error) {
	if f.sampleErrs > 0 {
		f.sampleErrs--
		return nil, nil, errors.New("fake: sample blind")
	}
	return f.Fire(ctx, c, leaf, args, resolve)
}

func (f *fakeRunner) Plan(_ context.Context, leaf Leaf, _ []guardfile.ArgBind, _ Resolve) (map[string]any, error) {
	return map[string]any{"leaf": leaf.Label()}, nil
}

// steps builds a two-step sequence whose steps carry distinct compensations,
// so a test can observe reverse-order rollback and $as data-flow.
func testSteps() []Step {
	return []Step{
		{
			Leaf: fakeLeaf("create-a"), As: "a",
			Args:       []guardfile.ArgBind{{Name: "title", Value: "a"}},
			Compensate: &Compensation{Leaf: fakeLeaf("undo-a"), Args: []guardfile.ArgBind{{Name: "id", Value: "$a.id"}}},
		},
		{
			Leaf: fakeLeaf("create-b"), As: "b",
			Compensate: &Compensation{Leaf: fakeLeaf("undo-b")},
		},
		{Leaf: fakeLeaf("finish")},
	}
}

func TestRunSequenceBindsAndOrders(t *testing.T) {
	r := &fakeRunner{responses: map[string]any{"create-a": map[string]any{"id": 7.0}}}
	bindings, _, err := Run(context.Background(), nil, "deploy", testSteps(), nil, map[string]string{}, map[string]any{}, r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(r.fired, ",") != "create-a,create-b,finish" {
		t.Errorf("fire order = %v", r.fired)
	}
	if bindings["a"] == nil {
		t.Errorf("as-binding missing: %v", bindings)
	}
}

func TestRunRollsBackInReverseWithDataFlow(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]any{"create-a": map[string]any{"id": 7.0}},
		failOn:    map[string]bool{"finish": true},
	}
	_, _, err := Run(context.Background(), nil, "deploy", testSteps(), nil, map[string]string{}, map[string]any{}, r)
	if err == nil {
		t.Fatal("expected a rollback exit, got nil")
	}
	coded := exitcode.From(err)
	if coded == nil || coded.Kind() != "action_rolled_back" {
		t.Fatalf("error = %v, want action_rolled_back", err)
	}
	want := "create-a,create-b,finish,undo-b,undo-a"
	if strings.Join(r.fired, ",") != want {
		t.Errorf("fire order = %v, want %s (reverse compensation)", r.fired, want)
	}
	if got := r.resolved["undo-a"]; len(got) != 1 || got[0] != "7" {
		t.Errorf("compensation args = %v, want the $a.id data-flow (7)", got)
	}
}

func TestRunFailedCompensationIsLoud(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]any{"create-a": map[string]any{"id": 7.0}},
		failOn:    map[string]bool{"finish": true, "undo-b": true},
	}
	_, _, err := Run(context.Background(), nil, "deploy", testSteps(), nil, map[string]string{}, map[string]any{}, r)
	coded := exitcode.From(err)
	if coded == nil || coded.Kind() != "action_failed" {
		t.Fatalf("error = %v, want action_failed when a compensation also fails", err)
	}
	if !strings.Contains(err.Error(), "compensation failed") {
		t.Errorf("error should name the failed compensation: %v", err)
	}
}

// canaryOver builds a canary with a short window; the degraded predicate is
// fixed, the healthy one optional.
func canaryOver(healthy string) *Canary {
	return &Canary{
		Leaf: fakeLeaf("health"), Every: 3 * time.Millisecond, Window: 60 * time.Millisecond,
		DegradedWhen: "ok == `false`", HealthyWhen: healthy, As: "health",
	}
}

func TestCanaryDegradedRollsBack(t *testing.T) {
	r := &fakeRunner{responses: map[string]any{
		"create-a": map[string]any{"id": 7.0},
		"health":   map[string]any{"ok": false},
	}}
	_, _, err := Run(context.Background(), nil, "promote", testSteps(),
		canaryOver(""), map[string]string{}, map[string]any{}, r)
	coded := exitcode.From(err)
	if coded == nil || coded.Kind() != "canary_degraded" {
		t.Fatalf("error = %v, want canary_degraded", err)
	}
	got := strings.Join(r.fired, ",")
	if !strings.HasSuffix(got, "health,undo-b,undo-a") {
		t.Errorf("fire order = %s, want the canary then reverse compensations", got)
	}
}

func TestCanaryBlindSampleRollsBack(t *testing.T) {
	r := &fakeRunner{
		responses:  map[string]any{"create-a": map[string]any{"id": 7.0}},
		sampleErrs: 1,
	}
	_, _, err := Run(context.Background(), nil, "promote", testSteps(),
		canaryOver(""), map[string]string{}, map[string]any{}, r)
	coded := exitcode.From(err)
	if coded == nil || coded.Kind() != "canary_blind" {
		t.Fatalf("error = %v, want canary_blind (an unobservable target is never left promoted)", err)
	}
	if !strings.Contains(strings.Join(r.fired, ","), "undo-b,undo-a") {
		t.Errorf("blind canary must roll back: %v", r.fired)
	}
}

func TestCanaryHealthyPassesEarly(t *testing.T) {
	r := &fakeRunner{responses: map[string]any{
		"create-a": map[string]any{"id": 7.0},
		"health":   map[string]any{"ok": true},
	}}
	_, _, err := Run(context.Background(), nil, "promote", testSteps(),
		canaryOver("ok == `true`"), map[string]string{}, map[string]any{}, r)
	if err != nil {
		t.Fatalf("healthy canary should pass: %v", err)
	}
	if strings.Contains(strings.Join(r.fired, ","), "undo") {
		t.Errorf("healthy canary must not roll back: %v", r.fired)
	}
}

func TestCanaryWindowElapsesAsPass(t *testing.T) {
	r := &fakeRunner{responses: map[string]any{
		"create-a": map[string]any{"id": 7.0},
		"health":   map[string]any{"ok": true},
	}}
	_, _, err := Run(context.Background(), nil, "promote", testSteps(),
		canaryOver(""), map[string]string{}, map[string]any{}, r)
	if err != nil {
		t.Fatalf("a canary that never degrades should pass when the window elapses: %v", err)
	}
}

func TestPlanCallsRendersCompensations(t *testing.T) {
	r := &fakeRunner{}
	resolve := func(v string) (string, error) { return ResolveArgDry(v, nil), nil }
	plan, err := PlanCalls(context.Background(), testSteps(), resolve, r)
	if err != nil {
		t.Fatalf("PlanCalls: %v", err)
	}
	first, ok := plan[0].(map[string]any)
	if !ok || first["compensate"] == nil || first["as"] != "a" {
		t.Errorf("plan[0] missing compensate/as: %v", plan[0])
	}
}
