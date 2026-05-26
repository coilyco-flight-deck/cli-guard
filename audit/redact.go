package audit

import (
	"regexp"
	"strings"
)

// Tier values for ProfileDecision.Coordinate.DataSecurity. Mirrored
// here so the redactor does not import the profile package and can
const (
	DataSecurityLow    = "low"
	DataSecurityMedium = "medium"
	DataSecurityHigh   = "high"
	DataSecurityMax    = "max"
)

// RedactPolicy carries the patterns the consumer wants applied. cli-guard
// supplies the mechanism; the consumer (today: coily) supplies the patterns.
type RedactPolicy struct {
	// SecretFlagPatterns is a list of flag-name prefixes (with leading
	// dashes). Matching is "argv token starts with this prefix" so
	SecretFlagPatterns []string

	// IdentifierPatterns is a list of compiled regexes. Any match in
	// Error/StderrTail/Reason fields gets replaced with [REDACTED].
	IdentifierPatterns []*regexp.Regexp
}

// RedactedValue is the literal replacement token used everywhere
// redaction applies. Exported so consumers can grep for it in tests.
const RedactedValue = "[REDACTED]"

// hasSecretFlagPrefix returns the matching prefix and a bool. The
// prefix includes the leading dashes. Argv tokens are checked for
func (p RedactPolicy) hasSecretFlagPrefix(tok string) (string, bool) {
	for _, pre := range p.SecretFlagPatterns {
		if tok == pre || strings.HasPrefix(tok, pre+"=") {
			return pre, true
		}
	}
	return "", false
}

// RedactArgv applies the data-security tier to argv. Returns the
// argv to persist. At "low" the argv is returned unchanged. At
func RedactArgv(argv []string, tier string, p RedactPolicy) []string {
	if len(argv) == 0 || tier == "" || tier == DataSecurityLow {
		return argv
	}
	if len(p.SecretFlagPatterns) == 0 {
		return argv
	}
	matched := false
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		pre, ok := p.hasSecretFlagPrefix(tok)
		if !ok {
			out = append(out, tok)
			continue
		}
		matched = true
		if strings.HasPrefix(tok, pre+"=") {
			out = append(out, pre+"="+RedactedValue)
			continue
		}
		// Bare flag form: redact this token's value (next argv element).
		out = append(out, tok)
		if i+1 < len(argv) {
			out = append(out, RedactedValue)
			i++
		}
	}
	if tier == DataSecurityMax && matched {
		return nil
	}
	if !matched {
		return argv
	}
	return out
}

// RedactIdentifiersInString replaces every IdentifierPatterns match
// with [REDACTED]. Runs at "medium" and stricter. Empty input or
func RedactIdentifiersInString(s string, tier string, p RedactPolicy) string {
	if s == "" || tier == "" || tier == DataSecurityLow {
		return s
	}
	if len(p.IdentifierPatterns) == 0 {
		return s
	}
	for _, re := range p.IdentifierPatterns {
		s = re.ReplaceAllString(s, RedactedValue)
	}
	return s
}

// RedactEgressRows applies the data_security tier to egress rows.
// At "high" Host is stripped to the eTLD+1 best-effort (strip leading
func RedactEgressRows(rows []EgressRow, tier string) []EgressRow {
	if len(rows) == 0 || tier == "" || tier == DataSecurityLow || tier == DataSecurityMedium {
		return rows
	}
	out := make([]EgressRow, len(rows))
	for i, r := range rows {
		switch tier {
		case DataSecurityHigh:
			r.Host = stripLeadingSubdomain(r.Host)
		case DataSecurityMax:
			r.Host = RedactedValue
			r.DurationMS = 0
		}
		out[i] = r
	}
	return out
}

// stripLeadingSubdomain returns the host with its leading subdomain
// removed when the host has 3+ labels. Naive eTLD+1 approximation:
func stripLeadingSubdomain(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[1:], ".")
}

// applyRedaction mutates r in place with the tier and policy. Called
// from Append before the JSON encode step. No-op when r.ProfileDecision
func (w *Writer) applyRedaction(r *Record) {
	if r.ProfileDecision == nil {
		return
	}
	tier := r.ProfileDecision.Coordinate.DataSecurity
	if tier == "" || tier == DataSecurityLow {
		return
	}
	w.mu.Lock()
	p := w.redact
	w.mu.Unlock()
	r.Argv = RedactArgv(r.Argv, tier, p)
	r.RemoteArgv = RedactArgv(r.RemoteArgv, tier, p)
	r.Error = RedactIdentifiersInString(r.Error, tier, p)
	r.StderrTail = RedactIdentifiersInString(r.StderrTail, tier, p)
	if r.ProfileDecision != nil {
		r.ProfileDecision.Reason = RedactIdentifiersInString(r.ProfileDecision.Reason, tier, p)
	}
	r.Egress = RedactEgressRows(r.Egress, tier)
}

// SetRedactPolicy installs the consumer-supplied pattern list. Safe
// to call once at Runner construction. Calls during a hot pipeline
func (w *Writer) SetRedactPolicy(p RedactPolicy) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.redact = p
}
