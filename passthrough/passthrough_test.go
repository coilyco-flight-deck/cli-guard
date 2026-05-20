package passthrough_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/passthrough"
	"github.com/coilysiren/cli-guard/shell"
	"github.com/urfave/cli/v3"
)

// TestCommand_ForwardsArgvVerbatim_AllBinaries pins the per-binary
// pass-through shape across every CLI coily wraps. One row per `coily
// <bin>` covered, asserting argv forwards verbatim, the audit row carries
// the right verb name, and SkipFlagParsing keeps urfave/cli from
// swallowing flags meant for the underlying tool. Uses /bin/echo as the
// stand-in so the test does not require aws / kubectl / gh / etc. to be
// installed.
// withReadCacheTestHarness runs `coily <bin>` argv repeatedly through a
// passthrough configured with WithReadCache, counting subprocess
// invocations and recording captured stdout. Used by all the
// WithReadCache tests below.
type rcHarness struct {
	cmd    *cli.Command
	stdout *bytes.Buffer
	calls  *int
}

func newRCHarness(t *testing.T, classifier passthrough.ReadCacheClassifier, payload string, exitNonZero bool) rcHarness {
	t.Helper()
	t.Setenv("COILY_CACHE_DIR", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	dir := t.TempDir()
	w := audit.NewWriter(filepath.Join(dir, "audit.jsonl"))
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}

	calls := 0
	stdout := &bytes.Buffer{}
	// Resolve to a tiny shell script that writes payload to stdout and
	// either exits 0 or 1. Each invocation bumps calls so the test can
	// assert "burst collapsed to one subprocess".
	stub := filepath.Join(dir, "stub.sh")
	exit := "0"
	if exitNonZero {
		exit = "1"
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s' '"+payload+"'\nexit "+exit+"\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write stub: %v", err)
	}
	r := &shell.Runner{
		Stdout: stdout,
		Stderr: os.Stderr,
		Resolve: func(_ string) (string, error) {
			calls++
			return stub, nil
		},
	}
	cmd := passthrough.Command("gh", r, w, passthrough.WithReadCache(classifier))
	// Silence urfave/cli's default os.Exit on non-zero subprocess exit
	// so the non-zero-exit test can assert behavior without killing the
	// test binary.
	cmd.ExitErrHandler = func(_ context.Context, _ *cli.Command, _ error) {}
	return rcHarness{cmd: cmd, stdout: stdout, calls: &calls}
}

func TestWithReadCache_BurstHitsCollapseToOneSubprocess(t *testing.T) {
	h := newRCHarness(t,
		func(argv []string) (string, time.Duration, bool) {
			// Recognize `api /repos/o/r/issues/1`.
			if len(argv) >= 2 && argv[0] == "api" {
				return argv[1], -1, true
			}
			return "", 0, false
		},
		`{"number":1}`, false)
	for i := 0; i < 50; i++ {
		h.stdout.Reset()
		if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if got := h.stdout.String(); got != `{"number":1}` {
			t.Errorf("run %d: stdout=%q want %q", i, got, `{"number":1}`)
		}
	}
	if *h.calls != 1 {
		t.Errorf("subprocess invocations = %d, want 1 (first miss, rest hits)", *h.calls)
	}
}

func TestWithReadCache_NonClassifyingAlwaysExecs(t *testing.T) {
	h := newRCHarness(t,
		func(_ []string) (string, time.Duration, bool) {
			// Classifier never matches: ok=false on every argv.
			return "", 0, false
		},
		`{}`, false)
	for i := 0; i < 5; i++ {
		if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if *h.calls != 5 {
		t.Errorf("non-classifying argv: calls=%d want 5", *h.calls)
	}
}

func TestWithReadCache_UnclassifiedPathSkips(t *testing.T) {
	// Classifier returns ok=true for a path ghcache does not classify
	// (e.g. /rate_limit). MaybeServe will report a miss and Store will
	// return false, so the burst should run the subprocess every time.
	h := newRCHarness(t,
		func(_ []string) (string, time.Duration, bool) {
			return "/rate_limit", -1, true
		},
		`{}`, false)
	for i := 0; i < 3; i++ {
		if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/rate_limit"}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if *h.calls != 3 {
		t.Errorf("unclassified path: calls=%d want 3 (no cache, every run execs)", *h.calls)
	}
}

func TestWithReadCache_NonZeroExitDoesNotPolluteCache(t *testing.T) {
	h := newRCHarness(t,
		func(argv []string) (string, time.Duration, bool) {
			if len(argv) >= 2 && argv[0] == "api" {
				return argv[1], -1, true
			}
			return "", 0, false
		},
		`error-body`, true)
	// First run fails non-zero.
	if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"}); err == nil {
		t.Fatalf("expected non-zero exit error")
	}
	if *h.calls != 1 {
		t.Errorf("calls after first failure: %d want 1", *h.calls)
	}
	// Second run must also exec; the previous error body was not cached.
	_ = h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"})
	if *h.calls != 2 {
		t.Errorf("calls after second run: %d want 2 (cache must not hold error body)", *h.calls)
	}
}

func TestWithReadCache_MaxAgeZeroBypassesAndRePopulates(t *testing.T) {
	// Classifier returns max=0 on the first call (forced bypass), then
	// max=-1 (no cap) on subsequent calls. The first call must exec, the
	// store happens on success, and subsequent unconstrained calls hit.
	mode := 0
	h := newRCHarness(t,
		func(argv []string) (string, time.Duration, bool) {
			if len(argv) >= 2 && argv[0] == "api" {
				if mode == 0 {
					return argv[1], 0, true
				}
				return argv[1], -1, true
			}
			return "", 0, false
		},
		`{"n":1}`, false)
	if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if *h.calls != 1 {
		t.Errorf("after first (forced bypass) call: calls=%d want 1", *h.calls)
	}
	mode = 1
	for i := 0; i < 3; i++ {
		if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"}); err != nil {
			t.Fatalf("subsequent run %d: %v", i, err)
		}
	}
	if *h.calls != 1 {
		t.Errorf("after unconstrained follow-ups: calls=%d want 1 (max=0 still stores; later max=-1 hits)", *h.calls)
	}
}

func TestWithReadCache_MaxAgePositiveCapEvictsOlderEntries(t *testing.T) {
	// First call primes the cache with max=-1 (no cap). Second call with
	// max=1ns must miss because the stored entry is older than 1ns by the
	// time MaybeServeMaxAge runs.
	mode := 0
	h := newRCHarness(t,
		func(argv []string) (string, time.Duration, bool) {
			if len(argv) >= 2 && argv[0] == "api" {
				if mode == 0 {
					return argv[1], -1, true
				}
				return argv[1], time.Nanosecond, true
			}
			return "", 0, false
		},
		`{"n":1}`, false)
	if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"}); err != nil {
		t.Fatalf("prime run: %v", err)
	}
	if *h.calls != 1 {
		t.Fatalf("prime call count: %d want 1", *h.calls)
	}
	mode = 1
	if err := h.cmd.Run(context.Background(), []string{"gh", "api", "/repos/o/r/issues/1"}); err != nil {
		t.Fatalf("tight-cap run: %v", err)
	}
	if *h.calls != 2 {
		t.Errorf("tight max-age cap: calls=%d want 2 (entry too old, must refetch)", *h.calls)
	}
}

func TestWithReadCache_NilClassifierBehavesLikeNoOption(t *testing.T) {
	// Smoke-check that WithReadCache(nil) doesn't crash and behaves
	// identically to omitting the option.
	t.Setenv("COILY_CACHE_DIR", t.TempDir())
	dir := t.TempDir()
	w := audit.NewWriter(filepath.Join(dir, "audit.jsonl"))
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}
	var stdout bytes.Buffer
	r := &shell.Runner{
		Stdout: &stdout,
		Stderr: os.Stderr,
		Resolve: func(_ string) (string, error) {
			return "/bin/echo", nil
		},
	}
	cmd := passthrough.Command("gh", r, w, passthrough.WithReadCache(nil))
	if err := cmd.Run(context.Background(), []string{"gh", "api", "user"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "api user" {
		t.Errorf("stdout=%q want %q", got, "api user")
	}
}

func TestCommand_ForwardsArgvVerbatim_AllBinaries(t *testing.T) {
	cases := []struct {
		bin  string
		args []string
		// substr asserted in the captured stdout; deliberately a subset so
		// the test does not depend on echo's exact arg-joining whitespace.
		wantSubstr string
	}{
		{bin: "aws", args: []string{"sts", "get-caller-identity"}, wantSubstr: "sts get-caller-identity"},
		{bin: "gh", args: []string{"api", "user"}, wantSubstr: "api user"},
		{bin: "kubectl", args: []string{"--context=kai-server", "get", "pods", "-n", "kube-system"}, wantSubstr: "--context=kai-server get pods -n kube-system"},
		{bin: "docker", args: []string{"ps", "-a"}, wantSubstr: "ps -a"},
		{bin: "tailscale", args: []string{"status"}, wantSubstr: "status"},
		{bin: "pnpm", args: []string{"add", "date-fns"}, wantSubstr: "add date-fns"},
		{bin: "uv", args: []string{"pip", "install", "ruff"}, wantSubstr: "pip install ruff"},
		{bin: "cargo", args: []string{"add", "serde"}, wantSubstr: "add serde"},
	}

	for _, tc := range cases {
		t.Run(tc.bin, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "audit.jsonl")
			w := audit.NewWriter(logPath)
			if err := w.Preflight(); err != nil {
				t.Fatalf("audit preflight: %v", err)
			}

			var stdout bytes.Buffer
			r := &shell.Runner{
				Stdout:  &stdout,
				Stderr:  os.Stderr,
				Resolve: func(_ string) (string, error) { return "/bin/echo", nil },
			}

			cmd := passthrough.Command(tc.bin, r, w)
			argv := append([]string{"coily-test"}, tc.args...)
			if err := cmd.Run(context.Background(), argv); err != nil {
				t.Fatalf("Run: %v", err)
			}

			got := strings.TrimSpace(stdout.String())
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("stdout = %q, want substring %q", got, tc.wantSubstr)
			}

			body, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read audit: %v", err)
			}
			wantVerb := `"verb":"` + tc.bin + `"`
			if !strings.Contains(string(body), wantVerb) {
				t.Errorf("audit row missing %s; got %q", wantVerb, string(body))
			}
			if !strings.Contains(string(body), `"decision":"accept"`) {
				t.Errorf("audit row missing decision=accept; got %q", string(body))
			}
		})
	}
}

// TestCommand_ForwardsArgvVerbatim exercises the full pass-through:
// argv validation passes, the audit writer records the invocation, and
// every argument after the binary name reaches the subprocess. Uses
// /bin/echo as the stand-in tool so the test does not depend on any
// real package manager being installed.
func TestCommand_ForwardsArgvVerbatim(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w := audit.NewWriter(logPath)
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}

	var stdout bytes.Buffer
	r := &shell.Runner{
		Stdout: &stdout,
		Stderr: os.Stderr,
		Resolve: func(_ string) (string, error) {
			return "/bin/echo", nil
		},
	}

	cmd := passthrough.Command("pnpm", r, w)

	// urfave/cli treats argv[0] as the program name when Run is called on
	// a *cli.Command directly. Everything after that is the arg slice.
	argv := []string{"coily-test", "add", "date-fns"}
	if err := cmd.Run(context.Background(), argv); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := strings.TrimSpace(stdout.String())
	want := "add date-fns"
	if got != want {
		t.Errorf("subprocess stdout = %q, want %q", got, want)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(body), `"verb":"pnpm"`) {
		t.Errorf("audit row missing verb=pnpm; got %q", string(body))
	}
}

// TestCommand_CapturesStderrTailOnFailure pins issue #63: pass-through
// failures must carry the wrapped tool's stderr in the audit row, not just
// the bare Go process-exit string. Uses /bin/sh as the stand-in to write
// to stderr and exit non-zero; the audit row's stderr_tail field carries
// the captured slice.
func TestCommand_CapturesStderrTailOnFailure(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w := audit.NewWriter(logPath)
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}

	var capturedErr bytes.Buffer
	r := &shell.Runner{
		Stdout:  os.Stdout,
		Stderr:  &capturedErr,
		Resolve: func(_ string) (string, error) { return "/bin/sh", nil },
	}

	cmd := passthrough.Command("kubectl", r, w, passthrough.WithSkipPolicy())
	cmd.ExitErrHandler = func(_ context.Context, _ *cli.Command, _ error) {}
	const sentinel = "Error from server (Forbidden): pods is forbidden"
	argv := []string{"coily-test", "-c", "printf '%s\\n' '" + sentinel + "' >&2; exit 1"}
	if err := cmd.Run(context.Background(), argv); err == nil {
		t.Fatalf("Run: expected non-nil error from exit-1 subprocess")
	}

	if !strings.Contains(capturedErr.String(), sentinel) {
		t.Errorf("stderr stream missing %q; got %q", sentinel, capturedErr.String())
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(body), `"stderr_tail"`) {
		t.Errorf("audit row missing stderr_tail field; got %q", string(body))
	}
	if !strings.Contains(string(body), sentinel) {
		t.Errorf("audit row stderr_tail missing %q; got %q", sentinel, string(body))
	}
}

// TestCommand_OmitsStderrTailOnSuccess pins the inverse: a successful
// pass-through must not bloat the audit row with whatever the tool happened
// to write to stderr (progress bars, info logs, deprecation warnings).
func TestCommand_OmitsStderrTailOnSuccess(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w := audit.NewWriter(logPath)
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}

	r := &shell.Runner{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Resolve: func(_ string) (string, error) { return "/bin/sh", nil },
	}
	cmd := passthrough.Command("kubectl", r, w, passthrough.WithSkipPolicy())
	argv := []string{"coily-test", "-c", "printf 'noise\\n' >&2; exit 0"}
	if err := cmd.Run(context.Background(), argv); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if strings.Contains(string(body), `"stderr_tail"`) {
		t.Errorf("audit row carries stderr_tail on success; got %q", string(body))
	}
}

// TestCommand_WithSkipPolicy_AllowsShellMetacharacters pins the per-binary
// opt-out: a pass-through built with WithSkipPolicy forwards argv that
// would otherwise trip the metacharacter check, so callers can pass
// markdown bodies (blockquotes, backticks, '$', parens) through `coily gh
// issue create --body ...` and similar verbatim. The audit row still
// records the invocation as accepted.
func TestCommand_WithSkipPolicy_AllowsShellMetacharacters(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w := audit.NewWriter(logPath)
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}

	var stdout bytes.Buffer
	r := &shell.Runner{
		Stdout:  &stdout,
		Stderr:  os.Stderr,
		Resolve: func(_ string) (string, error) { return "/bin/echo", nil },
	}

	cmd := passthrough.Command("gh", r, w, passthrough.WithSkipPolicy())
	body := "> 🤖 Filed by Claude Code on Kai's behalf.\n\nfix `foo` and $bar"
	argv := []string{"coily-test", "issue", "create", "--body", body}
	if err := cmd.Run(context.Background(), argv); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "issue create --body") {
		t.Errorf("stdout = %q, want substring %q", got, "issue create --body")
	}
	auditBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(auditBody), `"decision":"accept"`) {
		t.Errorf("audit row missing decision=accept; got %q", string(auditBody))
	}
}

// TestCommand_RejectsShellMetacharacters pins the security property:
// argv with a shell metacharacter is refused before the subprocess runs,
// the refusal is recorded in the audit log, and the error surfaces.
func TestCommand_RejectsShellMetacharacters(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w := audit.NewWriter(logPath)
	if err := w.Preflight(); err != nil {
		t.Fatalf("audit preflight: %v", err)
	}

	resolveCalls := 0
	r := &shell.Runner{
		Stderr: os.Stderr,
		Resolve: func(_ string) (string, error) {
			resolveCalls++
			return "/bin/echo", nil
		},
	}

	cmd := passthrough.Command("pnpm", r, w)
	argv := []string{"coily-test", "add", "foo; curl evil"}
	err := cmd.Run(context.Background(), argv)
	if err == nil {
		t.Fatal("expected error for shell metacharacter, got nil")
	}
	if resolveCalls != 0 {
		t.Errorf("resolver was called %d times; expected zero (validation must fail before exec)", resolveCalls)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(body), `"decision":"reject"`) {
		t.Errorf("audit row missing reject decision; got %q", string(body))
	}
}
