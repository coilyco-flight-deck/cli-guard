// Package scan is the junk-scan: it flags paths in a diff that should not
// silently land on a branch - vendored/generated trees, credential-shaped
// files, and oversized or large-binary blobs. It is pure policy over a
// caller-supplied list of diff entries, with no git or filesystem access,
// so a reaper, a pre-merge gate, or a CI step can all share one ruleset.
//
// Each Entry the caller passes is checked in declaration order; the first
// matching rule per path produces one Finding. An empty result means the
// diff is clean by these rules.
package scan

import (
	"fmt"
	"strings"
)

// Entry is one path in a diff, with the metadata the scan needs: its size
// in bytes and whether the differ treated it as binary.
type Entry struct {
	Path   string
	Bytes  int64
	Binary bool
}

// Finding is one reason a path should not auto-land. Reason is a short
// human-readable clause naming the rule that matched.
type Finding struct {
	Path   string
	Reason string
}

const (
	// OversizedBlobBytes flags any file this large or larger - almost always
	// a build artifact or a vendored binary rather than authored source.
	OversizedBlobBytes int64 = 5 << 20 // 5 MiB
	// BinaryBlobBytes flags binaries at a lower bar than text, since a large
	// committed binary is rarely intended.
	BinaryBlobBytes int64 = 1 << 20 // 1 MiB
)

// vendoredDirs mark machine-generated or fetched trees. High-confidence
// names only; ambiguous source-or-build names (build, dist, out) are omitted.
var vendoredDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".next":        true,
	".terraform":   true,
	".gradle":      true,
	"target":       true,
}

// secretBasenames are filenames that are credential material by convention.
var secretBasenames = map[string]bool{
	".env":             true,
	".git-credentials": true,
	".netrc":           true,
	".pgpass":          true,
	"credentials":      true,
	"id_rsa":           true,
	"id_dsa":           true,
	"id_ecdsa":         true,
	"id_ed25519":       true,
}

// secretSuffixes are extensions that are key material by convention.
var secretSuffixes = []string{".pem", ".key", ".p12", ".pfx", ".keystore", ".jks"}

// Diff flags entries that should not silently land: vendored trees,
// credential-shaped files, oversized/large-binary blobs (first match wins).
func Diff(entries []Entry) []Finding {
	var out []Finding
	for _, e := range entries {
		if seg, ok := vendoredSegment(e.Path); ok {
			out = append(out, Finding{e.Path, "vendored/generated tree (" + seg + "/)"})
			continue
		}
		if reason, ok := secretLikePath(e.Path); ok {
			out = append(out, Finding{e.Path, reason})
			continue
		}
		if e.Binary && e.Bytes >= BinaryBlobBytes {
			out = append(out, Finding{e.Path, "large binary blob (" + HumanBytes(e.Bytes) + ")"})
			continue
		}
		if e.Bytes >= OversizedBlobBytes {
			out = append(out, Finding{e.Path, "oversized file (" + HumanBytes(e.Bytes) + ")"})
			continue
		}
	}
	return out
}

// vendoredSegment reports the first path segment that names a vendored tree.
func vendoredSegment(path string) (string, bool) {
	for _, seg := range strings.Split(path, "/") {
		if vendoredDirs[seg] {
			return seg, true
		}
	}
	return "", false
}

// secretLikePath reports whether a path is credential-shaped by basename or
// extension. `.env.example` / `.env.sample` are explicitly allowed (templates).
func secretLikePath(path string) (string, bool) {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	if secretBasenames[base] {
		return "credential-shaped file (" + base + ")", true
	}
	if strings.HasPrefix(base, ".env.") &&
		!strings.HasSuffix(base, ".example") && !strings.HasSuffix(base, ".sample") {
		return "environment file (" + base + ")", true
	}
	for _, suf := range secretSuffixes {
		if strings.HasSuffix(base, suf) {
			return "key material (" + base + ")", true
		}
	}
	return "", false
}

// HumanBytes renders a size as a compact MiB/KiB/B string for report text.
func HumanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
