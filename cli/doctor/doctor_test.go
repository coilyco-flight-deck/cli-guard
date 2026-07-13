package doctor

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
)

// fakeInfo is a minimal os.FileInfo for exercising CanExec probes.
type fakeInfo struct{ mode os.FileMode }

func (f fakeInfo) Name() string       { return "fake" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

// probes builds a Probes with sensible no-op defaults; tests override the
// fields they exercise.
func probes() Probes {
	return Probes{
		Sudo:      func() (bool, string) { return false, "sudo: a password is required" },
		Stat:      func(string) (os.FileInfo, error) { return fakeInfo{mode: 0o644}, nil },
		CanExec:   func(os.FileInfo) (bool, bool) { return false, true },
		LookupEnv: func(string) (string, bool) { return "", false },
	}
}

// find returns the first finding for check, or a zero Finding.
func find(r Report, check string) (Finding, bool) {
	for _, f := range r.Findings {
		if f.Check == check {
			return f, true
		}
	}
	return Finding{}, false
}

func TestCheckSudo(t *testing.T) {
	cases := []struct {
		name      string
		forbid    bool
		succeeded bool
		stderr    string
		wantSev   Severity
		wantNone  bool
	}{
		{name: "not asserted -> no finding", forbid: false, wantNone: true},
		{name: "passwordless available -> fail", forbid: true, succeeded: true, wantSev: Fail},
		{name: "password required -> pass", forbid: true, succeeded: false, stderr: "sudo: a password is required", wantSev: Pass},
		{name: "other failure -> warn", forbid: true, succeeded: false, stderr: "sudo: command not found", wantSev: Warn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := probes()
			p.Sudo = func() (bool, string) { return tc.succeeded, tc.stderr }
			sec := repocfg.Security{Sudo: repocfg.SudoPolicy{ForbidPasswordless: tc.forbid}}
			r := Check(sec, p)
			f, ok := find(r, "sudo")
			if tc.wantNone {
				if ok {
					t.Fatalf("expected no sudo finding, got %+v", f)
				}
				return
			}
			if !ok {
				t.Fatalf("expected a sudo finding, got none")
			}
			if f.Severity != tc.wantSev {
				t.Errorf("severity = %v, want %v (msg: %s)", f.Severity, tc.wantSev, f.Message)
			}
		})
	}
}

func TestCheckBinaries(t *testing.T) {
	sec := repocfg.Security{ProtectedBinaries: []repocfg.ProtectedBinary{
		{Name: "gcloud", ExpectedRealPaths: []string{"/opt/homebrew/bin/gcloud"}},
	}}

	t.Run("agent-executable real binary -> fail", func(t *testing.T) {
		p := probes()
		p.CanExec = func(os.FileInfo) (bool, bool) { return true, true }
		r := Check(sec, p)
		f, _ := find(r, "binary:gcloud")
		if f.Severity != Fail {
			t.Fatalf("want Fail, got %v (%s)", f.Severity, f.Message)
		}
		if r.OK() {
			t.Error("report should not be OK with a Fail")
		}
	})

	t.Run("locked real binary -> pass", func(t *testing.T) {
		p := probes()
		p.CanExec = func(os.FileInfo) (bool, bool) { return false, true }
		r := Check(sec, p)
		f, _ := find(r, "binary:gcloud")
		if f.Severity != Pass {
			t.Fatalf("want Pass, got %v (%s)", f.Severity, f.Message)
		}
	})

	t.Run("missing real binary -> warn", func(t *testing.T) {
		p := probes()
		p.Stat = func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }
		r := Check(sec, p)
		f, _ := find(r, "binary:gcloud")
		if f.Severity != Warn {
			t.Fatalf("want Warn, got %v (%s)", f.Severity, f.Message)
		}
	})

	t.Run("unknown exec posture -> warn", func(t *testing.T) {
		p := probes()
		p.CanExec = func(os.FileInfo) (bool, bool) { return false, false }
		r := Check(sec, p)
		f, _ := find(r, "binary:gcloud")
		if f.Severity != Warn {
			t.Fatalf("want Warn, got %v (%s)", f.Severity, f.Message)
		}
	})

	t.Run("no expected_real_paths -> warn", func(t *testing.T) {
		bare := repocfg.Security{ProtectedBinaries: []repocfg.ProtectedBinary{{Name: "gcloud"}}}
		r := Check(bare, probes())
		f, ok := find(r, "binary:gcloud")
		if !ok || f.Severity != Warn {
			t.Fatalf("want Warn, got %+v", f)
		}
		if !strings.Contains(f.Message, "identified by basename only") {
			t.Errorf("message should distinguish the basename target: %s", f.Message)
		}
	})
}

func TestCheckBinariesUsesIdentityForIntegrityHints(t *testing.T) {
	sec := repocfg.Security{ProtectedBinaries: []repocfg.ProtectedBinary{
		{
			Name:              "kubectl",
			ExpectedRealPaths: []string{"/usr/local/bin/kubectl", "/opt/homebrew/bin/kubectl"},
		},
	}}

	p := probes()
	p.Stat = func(path string) (os.FileInfo, error) {
		switch path {
		case "/usr/local/bin/kubectl":
			return fakeInfo{mode: 0o755}, nil
		case "/opt/homebrew/bin/kubectl":
			return fakeInfo{mode: 0o644}, nil
		default:
			t.Fatalf("unexpected path: %s", path)
			return fakeInfo{}, os.ErrNotExist
		}
	}
	p.CanExec = func(info os.FileInfo) (bool, bool) {
		return info.Mode().Perm()&0o111 != 0, true
	}

	r := Check(sec, p)
	f, ok := find(r, "binary:kubectl")
	if !ok {
		t.Fatal("expected a kubectl finding")
	}
	if f.Severity != Fail {
		t.Fatalf("severity = %v, want Fail (%s)", f.Severity, f.Message)
	}
	if !strings.Contains(f.Message, "integrity hint") {
		t.Errorf("message should call the path an integrity hint: %s", f.Message)
	}
	if !strings.Contains(f.Message, "matching basename remains reachable by absolute path") {
		t.Errorf("message should explain the basename-wide risk: %s", f.Message)
	}
}

func TestCheckCredEnv(t *testing.T) {
	sec := repocfg.Security{ProtectedBinaries: []repocfg.ProtectedBinary{
		{Name: "gcloud", ExpectedRealPaths: []string{"/x"}, CredentialEnv: []string{"CLOUDSDK_CONFIG"}},
	}}
	t.Run("cred env present -> warn", func(t *testing.T) {
		p := probes()
		p.LookupEnv = func(k string) (string, bool) { return "/cfg", k == "CLOUDSDK_CONFIG" }
		r := Check(sec, p)
		f, ok := find(r, "credenv:gcloud")
		if !ok || f.Severity != Warn {
			t.Fatalf("want Warn, got %+v (ok=%v)", f, ok)
		}
		if !strings.Contains(f.Message, "CLOUDSDK_CONFIG") {
			t.Errorf("message should name the var: %s", f.Message)
		}
	})
	t.Run("cred env absent -> no finding", func(t *testing.T) {
		r := Check(sec, probes())
		if _, ok := find(r, "credenv:gcloud"); ok {
			t.Error("expected no credenv finding when var absent")
		}
	})
}

func TestReportOKAndWorst(t *testing.T) {
	r := Report{Findings: []Finding{
		{Check: "a", Severity: Pass},
		{Check: "b", Severity: Warn},
	}}
	if !r.OK() {
		t.Error("warnings should not break OK")
	}
	if r.Worst() != Warn {
		t.Errorf("Worst = %v, want Warn", r.Worst())
	}
	r.Findings = append(r.Findings, Finding{Check: "c", Severity: Fail})
	if r.OK() {
		t.Error("a Fail should break OK")
	}
	if r.Worst() != Fail {
		t.Errorf("Worst = %v, want Fail", r.Worst())
	}
}

func TestSeverityString(t *testing.T) {
	for sev, want := range map[Severity]string{Pass: "PASS", Warn: "WARN", Fail: "FAIL", Severity(99): "UNKNOWN"} {
		if got := sev.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, want)
		}
	}
}

func TestOneLine(t *testing.T) {
	got := oneLine("  sudo:\n a password\tis required \n")
	if strings.ContainsAny(got, "\n\t\r") {
		t.Errorf("oneLine left control chars: %q", got)
	}
	if got != "sudo: a password is required" {
		t.Errorf("oneLine = %q", got)
	}
}

// Guard against a real os.FileInfo flowing through defaultCanExec without
// panicking on this platform (result value is environment-dependent).
func TestDefaultProbesCanExecDoesNotPanic(t *testing.T) {
	info, err := os.Stat(os.Args[0])
	if err != nil {
		t.Skip("cannot stat test binary")
	}
	_, _ = DefaultProbes().CanExec(info)
}
