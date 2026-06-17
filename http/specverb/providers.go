package specverb

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Provider resolves the value at address for one named value source. Registered
// by the consumer so the engine imports no store SDK. See specverb-policy.md.
type Provider func(ctx context.Context, address string) (string, error)

// builtinProviders are the store-agnostic resolvers cli-guard ships itself: no SDK,
// no wiring. A consumer Config.Providers entry of the same name overrides one.
func builtinProviders() map[string]Provider {
	return map[string]Provider{
		"env": func(_ context.Context, name string) (string, error) {
			v, ok := os.LookupEnv(name)
			if !ok {
				return "", fmt.Errorf("env var %q is not set", name)
			}
			return v, nil
		},
		"file": func(_ context.Context, path string) (string, error) {
			b, err := os.ReadFile(path) //nolint:gosec // path is author-supplied trusted policy
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(b)), nil
		},
		"literal": func(_ context.Context, v string) (string, error) {
			return v, nil
		},
	}
}

// mergeProviders layers the consumer's registry over the built-ins (consumer wins
// on a clash), always non-nil. A missing provider fails closed in resolveValue.
func mergeProviders(consumer map[string]Provider) map[string]Provider {
	out := builtinProviders()
	for name, p := range consumer {
		if p != nil {
			out[name] = p
		}
	}
	return out
}
