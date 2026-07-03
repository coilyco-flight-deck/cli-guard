package specverb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// rollbackGuardfile declares a two-step action whose first call carries a
// compensation; the second call is the step a test fails to trigger rollback.
func rollbackGuardfile(t *testing.T) *guardfile.Guardfile {
	t.Helper()
	gf, err := guardfile.Parse([]byte(`wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		base-url "https://forgejo.coilysiren.me/api/v1"
		auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }
		can create issue { op "issueCreateIssue" }
		can close issue { op "issueEditIssue"; body state="closed" }
		can list tasks { op "ListActionTasks" }
		action deploy {
			describe "Create a prep issue, then run a step; roll back on failure."
			input repo { positional; required; help "owner/name" }
			call create issue {
				args { owner-repo $repo; title "prep" }
				as prep
				compensate close issue { args { owner-repo $repo; index $prep.number } }
			}
			call list tasks { args { owner-repo $repo } }
		}
	}`))
	if err != nil {
		t.Fatalf("parse rollback guardfile: %v", err)
	}
	return gf
}

// TestCallActionRollsBackOnFailure drives the saga: the second call 500s, so the
// engine fires the first call's compensation (closing the created issue).
func TestCallActionRollsBackOnFailure(t *testing.T) {
	var closePath, closeBody string
	var closed int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost: // create
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7,"title":"prep"}`))
		case http.MethodGet: // the second step: fail it
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case http.MethodPatch: // the compensation
			atomic.AddInt32(&closed, 1)
			b, _ := io.ReadAll(r.Body)
			closePath, closeBody = r.URL.Path, string(b)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"number":7,"state":"closed"}`))
		}
	}))
	defer srv.Close()

	cfg := Config{Guardfile: rollbackGuardfile(t), Spec: actionSpec(t), BaseURL: srv.URL,
		Providers: map[string]Provider{"ssm": func(context.Context, string) (string, error) { return "x", nil }}}
	_, err := runTree(t, cfg, "forgejo", "action", "deploy", "kai/demo", "--output", "json")
	if err == nil {
		t.Fatal("expected a non-zero exit from the failed step, got nil")
	}
	coded := exitcode.From(err)
	if coded == nil || coded.Code() != exitcode.UpstreamFailed {
		t.Fatalf("error = %v, want a coded UpstreamFailed", err)
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("error should name the rollback: %v", err)
	}
	if atomic.LoadInt32(&closed) != 1 {
		t.Fatalf("compensation fired %d times, want 1", closed)
	}
	if closePath != "/repos/kai/demo/issues/7" {
		t.Errorf("compensation path = %q, want the created issue (data-flow from $prep.number)", closePath)
	}
	if closeBody != `{"state":"closed"}` {
		t.Errorf("compensation body = %q, want the fixed close body", closeBody)
	}
}

// firedStep records one step a fakeStepRunner fired: its leaf and the resolved
// arg values, so a test can assert the sequence and rollback order with no HTTP.
type firedStep struct {
	leaf     string
	resolved map[string]string
}

// fakeStepRunner is a non-HTTP stepRunner recording each fired step: it proves
// the sequence/rollback/canary engine is transport-agnostic (the step seam).
type fakeStepRunner struct {
	responses map[string]any
	failOn    string
	fired     []firedStep
}

func (f *fakeStepRunner) fireStep(_ context.Context, _ *cli.Command, leaf opDescriptor, args []guardfile.ArgBind, resolve func(string) (string, error)) (any, []byte, error) {
	rec := firedStep{leaf: leaf.Leaf, resolved: map[string]string{}}
	for _, a := range args {
		v, err := resolve(a.Value)
		if err != nil {
			return nil, nil, err
		}
		rec.resolved[a.Name] = v
	}
	f.fired = append(f.fired, rec)
	if leaf.Leaf == f.failOn {
		return nil, nil, fmt.Errorf("fake: %s failed", leaf.Leaf)
	}
	resp := f.responses[leaf.Leaf]
	raw, _ := json.Marshal(resp)
	return resp, raw, nil
}

func (f *fakeStepRunner) planStep(_ context.Context, leaf opDescriptor, _ []guardfile.ArgBind, _ func(string) (string, error)) (map[string]any, error) {
	return map[string]any{"leaf": leaf.Leaf}, nil
}

// fakeRunnerGuardfile declares a three-step action, the first two carrying
// distinct compensations (close index 10 and 20), so reverse order is observable.
func fakeRunnerGuardfile(t *testing.T) *guardfile.Guardfile {
	t.Helper()
	gf, err := guardfile.Parse([]byte(`wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		base-url "https://forgejo.coilysiren.me/api/v1"
		auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }
		can create issue { op "issueCreateIssue" }
		can close issue { op "issueEditIssue"; body state="closed" }
		can list tasks { op "ListActionTasks" }
		action deploy {
			input repo { positional; required; help "owner/name" }
			call create issue {
				args { owner-repo $repo; title "a" }
				as a
				compensate close issue { args { owner-repo $repo; index "10" } }
			}
			call create issue {
				args { owner-repo $repo; title "b" }
				as b
				compensate close issue { args { owner-repo $repo; index "20" } }
			}
			call list tasks { args { owner-repo $repo } }
		}
	}`))
	if err != nil {
		t.Fatalf("parse fake-runner guardfile: %v", err)
	}
	return gf
}

// TestFakeStepRunnerRollbackOrder injects a non-HTTP stepRunner and asserts a
// mid-sequence failure compensates the completed steps in reverse order.
func TestFakeStepRunnerRollbackOrder(t *testing.T) {
	fake := &fakeStepRunner{
		responses: map[string]any{"create": map[string]any{"number": 7.0}},
		failOn:    "list",
	}
	cfg := Config{Guardfile: fakeRunnerGuardfile(t), Spec: actionSpec(t), stepRun: fake,
		Providers: map[string]Provider{"ssm": func(context.Context, string) (string, error) { return "x", nil }}}
	_, err := runTree(t, cfg, "forgejo", "action", "deploy", "kai/demo")
	if err == nil {
		t.Fatal("expected a non-zero exit from the failed step, got nil")
	}
	var order []string
	for _, s := range fake.fired {
		order = append(order, s.leaf)
	}
	// create, create, list (fails), then compensations in reverse: close, close.
	want := []string{"create", "create", "list", "close", "close"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("fire order = %v, want %v", order, want)
	}
	if got := fake.fired[3].resolved["index"]; got != "20" {
		t.Errorf("first compensation index = %q, want 20 (reverse order: step 2 undone first)", got)
	}
	if got := fake.fired[4].resolved["index"]; got != "10" {
		t.Errorf("second compensation index = %q, want 10", got)
	}
}

// canaryGuardfile declares a promote-shaped call action whose forward step
// carries a compensation, followed by a canary watch over a granted health leaf.
func canaryGuardfile(t *testing.T) *guardfile.Guardfile {
	t.Helper()
	gf, err := guardfile.Parse([]byte(`wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		base-url "https://forgejo.coilysiren.me/api/v1"
		auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }
		can create issue { op "issueCreateIssue" }
		can close issue { op "issueEditIssue"; body state="closed" }
		can list tasks { op "ListActionTasks" }
		action promote {
			describe "Promote, then canary-watch health and roll back on degradation."
			input repo { positional; required; help "owner/name" }
			call create issue {
				args { owner-repo $repo; title "promote" }
				as prep
				compensate close issue { args { owner-repo $repo; index $prep.number } }
			}
			canary list tasks {
				args { owner-repo $repo }
				every "5ms"
				window "80ms"
				degraded-when "length([?status=='failure']) > ` + "`0`" + `"
				healthy-when "length([?status=='success']) > ` + "`0`" + `"
				as health
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse canary guardfile: %v", err)
	}
	return gf
}

// TestCanaryDegradesRollsBack drives the canary path: a failing health sample
// rolls the promotion back (firing the compensation) and exits non-zero.
func TestCanaryDegradesRollsBack(t *testing.T) {
	var closed int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost: // the promote step
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7}`))
		case http.MethodGet: // the canary sample: degraded
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"status":"failure"}]`))
		case http.MethodPatch: // the compensation
			atomic.AddInt32(&closed, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"state":"closed"}`))
		}
	}))
	defer srv.Close()

	cfg := Config{Guardfile: canaryGuardfile(t), Spec: actionSpec(t), BaseURL: srv.URL,
		Providers: map[string]Provider{"ssm": func(context.Context, string) (string, error) { return "x", nil }}}
	_, err := runTree(t, cfg, "forgejo", "action", "promote", "kai/demo", "--output", "json")
	if err == nil {
		t.Fatal("expected a non-zero exit from canary degradation, got nil")
	}
	coded := exitcode.From(err)
	if coded == nil || coded.Kind() != "canary_degraded" {
		t.Fatalf("error = %v, want a coded canary_degraded", err)
	}
	if atomic.LoadInt32(&closed) != 1 {
		t.Errorf("compensation fired %d times, want 1 (canary rollback)", closed)
	}
}

// TestCanaryHealthyPasses drives the healthy path: the first sample settles
// healthy-when, so the watch ends clean with no rollback and a zero exit.
func TestCanaryHealthyPasses(t *testing.T) {
	var closed int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7}`))
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"status":"success"}]`))
		case http.MethodPatch:
			atomic.AddInt32(&closed, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"state":"closed"}`))
		}
	}))
	defer srv.Close()

	cfg := Config{Guardfile: canaryGuardfile(t), Spec: actionSpec(t), BaseURL: srv.URL,
		Providers: map[string]Provider{"ssm": func(context.Context, string) (string, error) { return "x", nil }}}
	if _, err := runTree(t, cfg, "forgejo", "action", "promote", "kai/demo", "--output", "json"); err != nil {
		t.Fatalf("healthy canary should pass: %v", err)
	}
	if atomic.LoadInt32(&closed) != 0 {
		t.Errorf("a healthy canary must not roll back (compensation fired %d times)", closed)
	}
}

// TestCanaryWindowHolds drives the hold path: samples never degrade nor confirm
// health, so the window elapses and the canary passes without a rollback.
func TestCanaryWindowHolds(t *testing.T) {
	var samples, closed int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7}`))
		case http.MethodGet:
			atomic.AddInt32(&samples, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"status":"running"}]`))
		case http.MethodPatch:
			atomic.AddInt32(&closed, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	cfg := Config{Guardfile: canaryGuardfile(t), Spec: actionSpec(t), BaseURL: srv.URL,
		Providers: map[string]Provider{"ssm": func(context.Context, string) (string, error) { return "x", nil }}}
	if _, err := runTree(t, cfg, "forgejo", "action", "promote", "kai/demo", "--output", "json"); err != nil {
		t.Fatalf("a canary that never degrades should pass when the window elapses: %v", err)
	}
	if atomic.LoadInt32(&samples) < 2 {
		t.Errorf("expected the canary to re-sample over the window, saw %d sample(s)", samples)
	}
	if atomic.LoadInt32(&closed) != 0 {
		t.Errorf("a held canary must not roll back (compensation fired %d times)", closed)
	}
}

// TestCallActionRollbackDryRunPlan proves --dry-run prints the compensation on
// each call and the canary block, firing nothing.
func TestCallActionRollbackDryRunPlan(t *testing.T) {
	cfg := Config{Guardfile: canaryGuardfile(t), Spec: actionSpec(t),
		HTTPClient: &http.Client{Transport: failingTransport{t}},
		Providers: map[string]Provider{"ssm": func(context.Context, string) (string, error) {
			t.Fatal("dry-run must not resolve a secret")
			return "", nil
		}}}
	out, err := runTree(t, cfg, "forgejo", "action", "promote", "kai/demo", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	var plan struct {
		Calls []struct {
			Compensate map[string]any `json:"compensate"`
		} `json:"calls"`
		Canary map[string]any `json:"canary"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("plan is not json: %v\n%s", err, out)
	}
	if len(plan.Calls) != 1 || plan.Calls[0].Compensate == nil {
		t.Fatalf("plan missing the compensation:\n%s", out)
	}
	if plan.Canary == nil || plan.Canary["degraded_when"] == nil {
		t.Fatalf("plan missing the canary block:\n%s", out)
	}
}

// TestRollbackDescribed proves the describe surface and prose document the
// compensation and canary of a guarded-rollback action.
func TestRollbackDescribed(t *testing.T) {
	surface, err := Describe(Config{Guardfile: canaryGuardfile(t), Spec: actionSpec(t)})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(surface.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(surface.Actions))
	}
	a := surface.Actions[0]
	if len(a.Calls) != 1 || a.Calls[0].Compensate == nil {
		t.Fatalf("call compensation missing from surface: %+v", a.Calls)
	}
	if a.Canary == nil || a.Canary.DegradedWhen == "" {
		t.Fatalf("canary missing from surface: %+v", a.Canary)
	}
	md := surface.Markdown()
	for _, want := range []string{"rolls back via", "walks the completed steps in reverse", "Canary."} {
		if !strings.Contains(md, want) {
			t.Errorf("prose missing %q:\n%s", want, md)
		}
	}
}

// TestGrantedOnlyCompensation proves a compensation naming an ungranted op fails
// closed at Build, not runtime - the deny-by-default invariant on the rollback seam.
func TestGrantedOnlyCompensation(t *testing.T) {
	gf := rollbackGuardfile(t)
	// drop the close grant: the first call's compensation now names an ungranted op
	kept := gf.Grants[:0:0]
	for _, g := range gf.Grants {
		if g.Verb != "close" || g.Resource != "issue" {
			kept = append(kept, g)
		}
	}
	gf.Grants = kept
	_, err := Build(Config{Guardfile: gf, Spec: actionSpec(t)})
	if err == nil {
		t.Fatal("expected a deny-by-default build error for an ungranted compensation, got nil")
	}
	if !strings.Contains(err.Error(), "deny-by-default") {
		t.Errorf("error = %v, want deny-by-default", err)
	}
}
