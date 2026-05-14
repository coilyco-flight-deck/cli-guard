// Package decision is the per-call profile-aware evaluator: takes a
// session profile name, resolves it through the profiles registry, and
// returns an audit.ProfileDecision suitable for attaching to an audit
// row. Today every call returns Allowed=true with the resolved
// Coordinate attached, so the audit log gathers a soak signal before
// any axis flips to enforcing mode.
//
// Plug in via verb.Spec.OnEvaluate when audit.profile_aware is true.
package decision

import (
	"regexp"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/profile"
	"github.com/coilysiren/cli-guard/profiles"
)

// RedactPolicy returns the default redactor: a secret-flag pattern list
// and an identifier regex list suitable for any consumer wrapping aws,
// gh, and the like. Install once at Writer construction via
// audit.Writer.SetRedactPolicy.
func RedactPolicy() audit.RedactPolicy {
	return audit.RedactPolicy{
		SecretFlagPatterns: []string{
			"--secret", "--secrets",
			"--password", "--passwd",
			"--token", "--api-key", "--api_key",
			"--auth", "--auth-token",
			"--credential", "--credentials",
			"--private-key",
			"--key-data",
			"--value", // covers `aws ssm put-parameter --value <secret>` for SecureString
		},
		IdentifierPatterns: []*regexp.Regexp{
			// AWS account id: 12 consecutive digits at word boundaries.
			regexp.MustCompile(`\b\d{12}\b`),
			// Email address. Tight enough for audit-row context.
			regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`),
		},
	}
}

// Evaluate resolves the named session profile via the profiles package
// and returns an attached audit.ProfileDecision. Allowed is always true
// today (soak mode). The error return path is reserved for loader
// failures (malformed ~/.coily/coily.yaml); a missing override or an
// unknown profile name fall through to a Strictest Coordinate with the
// matching Source value rather than an error.
func Evaluate(profileName string) (*audit.ProfileDecision, error) {
	res, err := profiles.Resolve(profileName)
	if err != nil {
		return nil, err
	}
	return &audit.ProfileDecision{
		Allowed:    true,
		Profile:    profileName,
		Source:     string(res.Source),
		Coordinate: snapshot(res.Coord),
		Reason:     res.Note,
	}, nil
}

// CoordinatePtr returns a non-nil *profile.Coordinate from the
// resolver result, suitable for lockdown.Driver.Coordinate. Callers
// that want the Strictest-fallback behavior on missing override can
// pass the empty profile name; the resolver hands them Strictest.
func CoordinatePtr(profileName string) (*profile.Coordinate, error) {
	res, err := profiles.Resolve(profileName)
	if err != nil {
		return nil, err
	}
	c := res.Coord
	return &c, nil
}

func snapshot(c profile.Coordinate) audit.Coordinate {
	return audit.Coordinate{
		DataSecurity:    string(c.DataSecurity),
		BlastRadius:     string(c.BlastRadius),
		NetworkEgress:   string(c.NetworkEgress),
		FilesystemReach: string(c.FilesystemReach),
	}
}
