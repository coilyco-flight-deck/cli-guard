package execverb

import (
	"context"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// ecoGuardfile is the promote shape the eco pipeline uses: an ssh wrap with pinned
// steps, an scp apply, a snapshot-threading rollback, and a canary on the health leaf.
const ecoGuardfile = `wrap ward-kdl ops eco server {
	exec ssh {
		argv-prefix "kai@kai-server"
	}
	can run snapshot { argv bash "/scripts/snapshot.sh" }
	can run apply { bin scp; argv "-r" }
	can run restart { argv bash "/scripts/restart.sh" }
	can run health { argv bash "/scripts/health.sh" }
	can run rollback { argv bash "/scripts/rollback.sh" }
	action promote {
		describe "guarded promote: snapshot, apply, restart, health, canary"
		input mod { positional; required; help "mod name" }
		call run snapshot {
			as snap
			compensate run rollback { args "$snap.last_line" }
		}
		call run apply { args "$mod" }
		call run restart
		call run health
		canary run health {
			every "5ms"
			window "60ms"
			degraded-when "exit_code != ` + "`0`" + `"
			healthy-when "kv.server_ready == '1' && kv.journal_clean == '1'"
			as health
		}
	}
}`

// capturedCall records one CaptureRunner invocation.
type capturedCall struct {
	bin  string
	argv []string
}

// scriptedCapture fakes step commands: outputs and exits keyed by an argv
// substring, recording every call. healthExits scripts successive health runs.
type scriptedCapture struct {
	calls       []capturedCall
	healthExits []int
	healthSeen  int
}

func (s *scriptedCapture) run(_ context.Context, bin string, argv, _ []string) ([]byte, []byte, int, error) {
	s.calls = append(s.calls, capturedCall{bin: bin, argv: append([]string{}, argv...)})
	joined := strings.Join(argv, " ")
	switch {
	case strings.Contains(joined, "snapshot.sh"):
		return []byte(">>> copying\nsnap-20260703-1\n"), nil, 0, nil
	case strings.Contains(joined, "health.sh"):
		exit := 0
		if s.healthSeen < len(s.healthExits) {
			exit = s.healthExits[s.healthSeen]
		}
		s.healthSeen++
		out := "service_active=1 journal_clean=1 server_ready=1"
		if exit != 0 {
			out = "service_active=0 journal_clean=1 server_ready=0"
		}
		return []byte(out), nil, exit, nil
	default:
		return []byte("ok"), nil, 0, nil
	}
}

// binsOf projects the recorded calls onto "bin:lastScript" labels for ordering
// assertions.
func binsOf(calls []capturedCall) []string {
	var out []string
	for _, c := range calls {
		label := c.bin
		for _, a := range c.argv {
			if strings.Contains(a, ".sh") {
				label += ":" + a[strings.LastIndex(a, "/")+1:]
			}
		}
		out = append(out, label)
	}
	return out
}

// runAction mounts the guardfile with the given capture and runs the action.
func runAction(t *testing.T, src string, capture CaptureRunner, argv ...string) error {
	t.Helper()
	gf, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	group, err := Build(Config{Guardfile: gf, RunCapture: capture})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := &cli.Command{Name: "ward", Commands: []*cli.Command{group}}
	return root.Run(context.Background(), append([]string{"ward", "server"}, argv...))
}

// TestExecActionGreenPath proves the full sequence fires in order over the pinned
// transport, the scp step drops the ssh argv-prefix, and a healthy canary ends clean.
func TestExecActionGreenPath(t *testing.T) {
	rec := &scriptedCapture{}
	if err := runAction(t, ecoGuardfile, rec.run, "promote", "EcoTelemetry"); err != nil {
		t.Fatalf("green promote: %v", err)
	}
	got := binsOf(rec.calls)
	want := []string{"ssh:snapshot.sh", "scp", "ssh:restart.sh", "ssh:health.sh", "ssh:health.sh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	// the ssh transport prefix pins every ssh step; the scp override drops it
	if rec.calls[0].argv[0] != "kai@kai-server" {
		t.Errorf("snapshot argv = %v, want the ssh argv-prefix first", rec.calls[0].argv)
	}
	if got := strings.Join(rec.calls[1].argv, " "); got != "-r EcoTelemetry" {
		t.Errorf("apply argv = %q, want the scp override without the ssh prefix", got)
	}
}

// TestExecActionStepFailureRollsBack proves a failing forward step (restart)
// fires the snapshot compensation with the $snap.last_line data-flow.
func TestExecActionStepFailureRollsBack(t *testing.T) {
	src := strings.Replace(ecoGuardfile, `can run restart { argv bash "/scripts/restart.sh" }`,
		`can run restart { argv bash "/scripts/fail.sh" }`, 1)
	rec := &scriptedCapture{}
	failing := &failWrapCapture{inner: rec}
	err := runAction(t, src, failing.run, "promote", "EcoTelemetry")
	if err == nil {
		t.Fatal("expected a rollback exit, got nil")
	}
	coded := exitcode.From(err)
	if coded == nil || coded.Kind() != "action_rolled_back" {
		t.Fatalf("error = %v, want action_rolled_back", err)
	}
	last := rec.calls[len(rec.calls)-1]
	if !strings.Contains(strings.Join(last.argv, " "), "rollback.sh snap-20260703-1") {
		t.Errorf("compensation argv = %v, want rollback.sh with the snapshot id", last.argv)
	}
}

// failWrapCapture fails any step whose argv names fail.sh, delegating the rest.
type failWrapCapture struct{ inner *scriptedCapture }

func (f *failWrapCapture) run(ctx context.Context, bin string, argv, env []string) ([]byte, []byte, int, error) {
	if strings.Contains(strings.Join(argv, " "), "fail.sh") {
		f.inner.calls = append(f.inner.calls, capturedCall{bin: bin, argv: argv})
		return nil, []byte("boom"), 7, nil
	}
	return f.inner.run(ctx, bin, argv, env)
}

// TestExecActionCanaryDegradesRollsBack proves a degraded canary sample (the
// health script exiting non-zero) drives the rollback path, not a bare error.
func TestExecActionCanaryDegradesRollsBack(t *testing.T) {
	// forward health passes (exit 0), the first canary sample degrades (exit 1)
	rec := &scriptedCapture{healthExits: []int{0, 1}}
	err := runAction(t, ecoGuardfile, rec.run, "promote", "EcoTelemetry")
	if err == nil {
		t.Fatal("expected canary_degraded, got nil")
	}
	coded := exitcode.From(err)
	if coded == nil || coded.Kind() != "canary_degraded" {
		t.Fatalf("error = %v, want canary_degraded", err)
	}
	last := binsOf(rec.calls)
	if !strings.HasSuffix(strings.Join(last, ","), "ssh:health.sh,ssh:rollback.sh") {
		t.Errorf("call tail = %v, want the degraded sample then the rollback", last)
	}
}

// TestExecActionDryRunFiresNothing proves --dry-run renders the plan without
// spawning a single step command.
func TestExecActionDryRunFiresNothing(t *testing.T) {
	rec := &scriptedCapture{}
	if err := runAction(t, ecoGuardfile, rec.run, "promote", "EcoTelemetry", "--dry-run"); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("dry-run fired %d step(s), want 0", len(rec.calls))
	}
}

// TestExecActionMetacharInputRefused proves the step-layer policy gate refuses a
// metacharacter-carrying arg before spawn, and the engine rolls completed steps back.
func TestExecActionMetacharInputRefused(t *testing.T) {
	rec := &scriptedCapture{}
	err := runAction(t, ecoGuardfile, rec.run, "promote", "Eco;rm -rf /")
	if err == nil {
		t.Fatal("expected the metachar gate to refuse, got nil")
	}
	for _, c := range rec.calls {
		if c.bin == "scp" {
			t.Fatalf("the gated apply step still spawned: %v", rec.calls)
		}
	}
	if got := binsOf(rec.calls); !strings.HasSuffix(strings.Join(got, ","), "rollback.sh") {
		t.Errorf("calls = %v, want the snapshot compensated after the refusal", got)
	}
}

// TestExecActionUnknownGrantFailsClosed proves a step naming an ungranted verb
// fails at Build, deny-by-default.
func TestExecActionUnknownGrantFailsClosed(t *testing.T) {
	src := strings.Replace(ecoGuardfile, "call run restart", "call run reboot", 1)
	gf, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Build(Config{Guardfile: gf}); err == nil || !strings.Contains(err.Error(), "deny-by-default") {
		t.Fatalf("Build err = %v, want deny-by-default", err)
	}
}

// TestExecActionSealedStepRejectsArgs proves a sealed grant cannot take step
// args (the seal pins the whole invocation).
func TestExecActionSealedStepRejectsArgs(t *testing.T) {
	src := strings.Replace(ecoGuardfile, `can run rollback { argv bash "/scripts/rollback.sh" }`,
		`can run rollback { argv bash "/scripts/rollback.sh"; sealed }`, 1)
	gf, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Build(Config{Guardfile: gf}); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("Build err = %v, want a sealed-step refusal", err)
	}
}

// TestExecActionNamedArgsRejected proves the exec dialect refuses the spec
// dialect's named `args { name value }` form on steps (positional only).
func TestExecActionNamedArgsRejected(t *testing.T) {
	src := strings.Replace(ecoGuardfile, `compensate run rollback { args "$snap.last_line" }`,
		`compensate run rollback { args { id "$snap.last_line" } }`, 1)
	gf, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Build(Config{Guardfile: gf}); err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("Build err = %v, want a positional-args refusal", err)
	}
}

// TestActionUnderPassthroughRefused proves the funnel sugar cannot carry
// actions (they compose named grants).
func TestActionUnderPassthroughRefused(t *testing.T) {
	src := `wrap ward-kdl ssh {
		passthrough ssh
		action promote { call run snapshot }
	}`
	if _, err := Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), "passthrough") {
		t.Fatalf("Parse err = %v, want a passthrough refusal", err)
	}
}

// TestBinOverrideParsesAndMounts proves the per-grant `bin` override survives
// parse and shows in the leaf usage without the wrap transport prefix.
func TestBinOverrideParsesAndMounts(t *testing.T) {
	gf, err := Parse([]byte(ecoGuardfile))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var apply *Grant
	for i := range gf.Grants {
		if gf.Grants[i].subcommandLabel() == "apply" {
			apply = &gf.Grants[i]
		}
	}
	if apply == nil || apply.Bin != "scp" {
		t.Fatalf("apply grant bin = %+v, want scp", apply)
	}
	if u := leafUsage(gf, *apply); !strings.HasPrefix(u, "exec: scp -r") {
		t.Errorf("usage = %q, want the scp invocation without the ssh prefix", u)
	}
}
