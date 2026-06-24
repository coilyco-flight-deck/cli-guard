// Package awsgate denies read-only aws invocations touching sensitive
// resources, pre-send. Extracted from the original read gate; see docs/execverb.md.
package awsgate

import (
	"strings"
)

// DefaultSensitivePatterns is the baked-in default-deny set: secrets,
// terraform state, backups, admin/root ARNs. Meaningful-name globs only.
var DefaultSensitivePatterns = []string{
	"*secret*",
	"*credential*",
	"*-private*",
	"*private-*",
	"*tfstate*",
	"*terraform-state*",
	"*-backup*",
	"*-backups*",
	"arn:aws:iam::*:role/*admin*",
	"arn:aws:iam::*:role/*root*",
}

// Gate is one configured sensitive-read policy.
type Gate struct {
	// Patterns are the sensitive globs; empty means DefaultSensitivePatterns.
	Patterns []string

	// AllowPatterns are globs naming tokens explicitly allowed through.
	AllowPatterns []string
}

// Check returns a denial (token, pattern, denied=true) when argv is a
// read-only aws invocation touching a sensitive token no allow glob clears.
func (g Gate) Check(argv []string) (token, pattern string, denied bool) {
	if !IsReadOnly(argv) {
		return "", "", false
	}
	patterns := g.Patterns
	if len(patterns) == 0 {
		patterns = DefaultSensitivePatterns
	}
	token, pattern, hit := matchSensitive(argv, patterns)
	if !hit || g.allowed(token) {
		return "", "", false
	}
	return token, pattern, true
}

// allowed reports whether token escapes the gate via an allow glob.
func (g Gate) allowed(token string) bool {
	lower := strings.ToLower(token)
	for _, pat := range g.AllowPatterns {
		if GlobMatch(strings.ToLower(pat), lower) {
			return true
		}
	}
	return false
}

// IsReadOnly reports whether argv is a read-only aws invocation: a service
// plus an operation named by the CLI's read convention (describe-*, ls, ...).
func IsReadOnly(argv []string) bool {
	positionals := Positionals(argv)
	if len(positionals) < 2 {
		return false
	}
	return isReadOp(positionals[1])
}

// readPrefixes are the operation-verb prefixes that denote a read.
var readPrefixes = []string{
	"describe-", "list-", "get-", "head-", "lookup-",
	"search-", "select-", "batch-get-",
}

// readExact are read operations that are not prefix-shaped.
var readExact = map[string]bool{"ls": true, "scan": true, "query": true}

// isReadOp reports whether op is a read by the CLI's naming convention.
func isReadOp(op string) bool {
	if readExact[op] {
		return true
	}
	for _, p := range readPrefixes {
		if strings.HasPrefix(op, p) {
			return true
		}
	}
	return false
}

// valueFlags are the aws CLI's value-taking global flags, enumerated so
// `--region us-east-1` does not leave its value masquerading as a positional.
var valueFlags = map[string]bool{
	"--region": true, "--profile": true, "--output": true,
	"--endpoint-url": true, "--cli-read-timeout": true,
	"--cli-connect-timeout": true, "--color": true, "--ca-bundle": true,
	"--query": true,
}

// Positionals returns argv with flags (and the values of known value-taking
// global flags) removed, preserving order.
func Positionals(argv []string) []string {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if strings.HasPrefix(tok, "--") {
			if strings.IndexByte(tok, '=') >= 0 {
				continue // --flag=value, self-contained
			}
			if valueFlags[tok] && i+1 < len(argv) {
				i++ // consume the value
			}
			continue
		}
		if strings.HasPrefix(tok, "-") && tok != "-" {
			continue // short flag
		}
		out = append(out, tok)
	}
	return out
}

// matchSensitive scans argv's positional tokens for the first matching a
// sensitive pattern, case-insensitively.
func matchSensitive(argv, patterns []string) (token, pattern string, ok bool) {
	for _, tok := range Positionals(argv) {
		lower := strings.ToLower(tok)
		for _, pat := range patterns {
			if GlobMatch(strings.ToLower(pat), lower) {
				return tok, pat, true
			}
		}
	}
	return "", "", false
}

// GlobMatch reports whether s matches pattern, where `*` matches any run of
// characters (crossing `/` and `:`, unlike filepath.Match), the rest literal.
func GlobMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s // no wildcard: exact match
	}
	if parts[0] != "" {
		if !strings.HasPrefix(s, parts[0]) {
			return false
		}
		s = s[len(parts[0]):]
	}
	for _, mid := range parts[1 : len(parts)-1] {
		if mid == "" {
			continue
		}
		idx := strings.Index(s, mid)
		if idx < 0 {
			return false
		}
		s = s[idx+len(mid):]
	}
	last := parts[len(parts)-1]
	if last != "" {
		return strings.HasSuffix(s, last)
	}
	return true
}
