// Package decision is the per-call profile-aware evaluator: takes a
// session profile name, resolves it through the profiles registry, and
package decision

import (
	"regexp"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/profile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/profiles"
)

// RedactPolicy returns the default redactor: a secret-flag pattern list
// and an identifier regex list suitable for any consumer wrapping aws,
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
