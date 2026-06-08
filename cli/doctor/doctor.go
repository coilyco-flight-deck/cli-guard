// Package doctor checks a host's posture against a consumer's declared
// repocfg.Security policy: the enforcement floor. See docs/features-detail.md.
package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/sudo"
)

// Severity ranks a finding: Pass informational, Warn unverifiable or softer
// risk, Fail the floor is broken.
type Severity int

const (
	Pass Severity = iota
	Warn
	Fail
)

// String renders the severity as a short uppercase label.
func (s Severity) String() string {
	switch s {
	case Pass:
		return "PASS"
	case Warn:
		return "WARN"
	case Fail:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

// Finding is one posture observation: Check names the dimension, Severity
// the outcome, Message the detail.
type Finding struct {
	Check    string
	Severity Severity
	Message  string
}

// Report is the result of Check. Findings are appended in check order
// (sudo, binaries, credential env).
type Report struct {
	Findings []Finding
}

// OK reports whether the report has no Fail finding. Warnings do not fail OK.
func (r Report) OK() bool {
	for _, f := range r.Findings {
		if f.Severity == Fail {
			return false
		}
	}
	return true
}

// Worst returns the highest severity present, or Pass for an empty report.
func (r Report) Worst() Severity {
	worst := Pass
	for _, f := range r.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

// add appends a formatted finding.
func (r *Report) add(check string, sev Severity, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{Check: check, Severity: sev, Message: fmt.Sprintf(format, args...)})
}

// Probes are the host seams Check reads. DefaultProbes wires the real
// sudo / filesystem / environment; tests inject fakes.
type Probes struct {
	// Sudo runs `sudo -n true` and reports success plus stderr. success
	// means the agent has passwordless sudo.
	Sudo func() (succeeded bool, stderr string)
	// Stat resolves a path to file info, like os.Stat.
	Stat func(path string) (os.FileInfo, error)
	// CanExec reports whether the agent user can exec the file, and whether
	// that was determinable (known=false on unsupported platforms).
	CanExec func(info os.FileInfo) (canExec bool, known bool)
	// LookupEnv resolves an environment variable, like os.LookupEnv.
	LookupEnv func(key string) (value string, present bool)
}

// Check runs every posture check the policy asks for and returns a Report.
// It reads only what the policy declares.
func Check(sec repocfg.Security, p Probes) Report {
	var r Report
	checkSudo(&r, sec, p)
	checkBinaries(&r, sec, p)
	checkCredEnv(&r, sec, p)
	return r
}

// checkSudo verifies the no-passwordless-sudo floor when asserted. A
// successful `sudo -n true` is a Fail.
func checkSudo(r *Report, sec repocfg.Security, p Probes) {
	if !sec.Sudo.ForbidPasswordless {
		return
	}
	succeeded, stderr := p.Sudo()
	switch {
	case succeeded:
		r.add("sudo", Fail, "passwordless sudo is available to this user; the agent can escalate without the human's password. Lock it down (no broad NOPASSWD for the agent user).")
	case sudo.PasswordRequired(stderr):
		r.add("sudo", Pass, "sudo requires a password; the agent cannot escalate unattended.")
	default:
		r.add("sudo", Warn, "could not confirm sudo posture (sudo -n failed for another reason: %s). Treat as unverified.", oneLine(stderr))
	}
}

// checkBinaries verifies each protected binary's real install is not
// agent-executable, so an absolute-path call cannot bypass the shim.
func checkBinaries(r *Report, sec repocfg.Security, p Probes) {
	for _, pb := range sec.ProtectedBinaries {
		check := "binary:" + pb.Name
		if len(pb.ExpectedRealPaths) == 0 {
			r.add(check, Warn, "no expected_real_paths declared; cannot verify the binary is locked against absolute-path invocation.")
			continue
		}
		for _, path := range pb.ExpectedRealPaths {
			info, err := p.Stat(path)
			if err != nil {
				r.add(check, Warn, "real binary not found at %s (%v); cannot verify floor.", path, err)
				continue
			}
			canExec, known := p.CanExec(info)
			switch {
			case !known:
				r.add(check, Warn, "cannot determine exec posture of %s on this platform.", path)
			case canExec:
				r.add(check, Fail, "real binary at %s is executable by the agent user; an absolute-path call bypasses the PATH shim. Make it root-owned and not user-executable.", path)
			default:
				r.add(check, Pass, "real binary at %s is not executable by the agent user.", path)
			}
		}
	}
}

// checkCredEnv warns when a binary's credential env vars are present in the
// session: the agent holds the credentials regardless of the lock.
func checkCredEnv(r *Report, sec repocfg.Security, p Probes) {
	for _, pb := range sec.ProtectedBinaries {
		for _, key := range pb.CredentialEnv {
			if _, present := p.LookupEnv(key); present {
				r.add("credenv:"+pb.Name, Warn, "%s is set in this session; the agent holds %s credentials regardless of the binary lock.", key, pb.Name)
			}
		}
	}
}

// DefaultProbes wires Check to the real host: sudo -n true, os.Stat,
// os.LookupEnv, and a platform-specific exec-for-current-user check.
func DefaultProbes() Probes {
	return Probes{
		Sudo:      defaultSudo,
		Stat:      os.Stat,
		CanExec:   defaultCanExec,
		LookupEnv: os.LookupEnv,
	}
}

// defaultSudo runs `sudo -n true` with fixed literal args and captures
// stderr for the password-required sentinel.
func defaultSudo() (bool, string) {
	cmd := exec.Command("sudo", "-n", "true")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	err := cmd.Run()
	return err == nil, errb.String()
}

// oneLine collapses every whitespace run in s to a single space.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
