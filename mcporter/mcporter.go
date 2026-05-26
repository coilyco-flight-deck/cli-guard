// Package mcporter wraps the mcporter tool with a pre-exec preflight that
// resolves `${VAR}` references in `~/.mcporter/mcporter.json` against a
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
type SecretResolver interface {
	Resolve(name string) (string, error)
}

// ResolverFunc adapts a plain function to the SecretResolver interface.
// Useful for tests and for one-off in-process resolvers that don't need
type ResolverFunc func(name string) (string, error)

// Resolve calls the underlying function.
func (f ResolverFunc) Resolve(name string) (string, error) { return f(name) }

// envVarRe matches `${NAME}` references in mcporter.json string values.
// The capture group is the bare variable name. Names are conventionally
var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ConfigPath returns the resolved mcporter config path. Honors the
// `MCPORTER_CONFIG` env override; otherwise falls back to the well-known
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
