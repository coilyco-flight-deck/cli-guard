// Package mcporter wraps the mcporter tool with a pre-exec preflight that
// resolves `${VAR}` references in `~/.mcporter/mcporter.json` against a
// pluggable SecretResolver, injecting the resolved values as env vars on
// the child mcporter process only. The parent shell's env is never touched.
//
// cli-guard stays secret-backend-agnostic. This package defines the
// SecretResolver interface and the scanning + caching mechanics; the
// concrete resolver (SSM, Vault, GCP Secret Manager, anything) lives in
// the consumer. coily wires in an SSM-backed resolver via
// coilysiren/coily#269.
//
// Failure model: a resolver error short-circuits with a clean error
// naming the failed variable. No partial-env exec. A missing config
// file scans to an empty name list and returns no error (no-op
// preflight). A parse error in mcporter.json surfaces the file path and
// position.
package mcporter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// SecretResolver resolves one env-var name (e.g. "COILYSIREN_TAILNET_DOMAIN")
// to its plaintext value. Implementations decide how to map the name to a
// backing store; cli-guard never interprets the name beyond passing it
// through. A resolver error is propagated verbatim by the passthrough so
// the user sees the underlying cause (expired SSO, missing param, etc.).
type SecretResolver interface {
	Resolve(name string) (string, error)
}

// ResolverFunc adapts a plain function to the SecretResolver interface.
// Useful for tests and for one-off in-process resolvers that don't need
// their own struct.
type ResolverFunc func(name string) (string, error)

// Resolve calls the underlying function.
func (f ResolverFunc) Resolve(name string) (string, error) { return f(name) }

// envVarRe matches `${NAME}` references in mcporter.json string values.
// The capture group is the bare variable name. Names are conventionally
// uppercase + underscores; we accept any non-empty `[A-Za-z0-9_]+` run
// to stay forward-compatible with mcporter's own grammar.
var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ConfigPath returns the resolved mcporter config path. Honors the
// `MCPORTER_CONFIG` env override; otherwise falls back to the well-known
// `~/.mcporter/mcporter.json`. Returns an error if the home directory
// cannot be resolved and no override is set.
func ConfigPath() (string, error) {
	if p := os.Getenv("MCPORTER_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("mcporter: resolve home: %w", err)
	}
	return filepath.Join(home, ".mcporter", "mcporter.json"), nil
}

// ScanConfig reads the JSON file at path and returns every `${NAME}`
// reference found in string values, deduplicated and sorted. A missing
// file returns an empty slice and no error (treated as "no preflight
// needed"). A parse error surfaces the file path and the underlying
// json error's offset.
func ScanConfig(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mcporter: read %s: %w", path, err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("mcporter: parse %s: %w", path, err)
	}
	seen := map[string]struct{}{}
	walkStrings(doc, func(s string) {
		for _, m := range envVarRe.FindAllStringSubmatch(s, -1) {
			seen[m[1]] = struct{}{}
		}
	})
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// ResolveAll fans out one Resolve call per name and collects results
// into a name->value map. Returns the first error encountered, naming
// the variable that failed.
func ResolveAll(r SecretResolver, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, n := range names {
		v, err := r.Resolve(n)
		if err != nil {
			return nil, fmt.Errorf("mcporter: resolve %s: %w", n, err)
		}
		out[n] = v
	}
	return out, nil
}

// walkStrings invokes fn on every string value reachable from doc
// (objects, arrays, nested). Non-string scalars are ignored. The walk
// is depth-first; order isn't load-bearing for callers.
func walkStrings(doc any, fn func(string)) {
	switch v := doc.(type) {
	case string:
		fn(v)
	case []any:
		for _, item := range v {
			walkStrings(item, fn)
		}
	case map[string]any:
		for _, item := range v {
			walkStrings(item, fn)
		}
	}
}
