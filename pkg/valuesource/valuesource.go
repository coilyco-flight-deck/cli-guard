// Package valuesource is cli-guard's shared value-resolution layer: a Provider
// reads a named address at request time, so neither engine imports a store SDK.
package valuesource

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Provider resolves the value at address for one named value source. A consumer
// registers store-backed resolvers; the same func type drives both engines.
type Provider func(ctx context.Context, address string) (string, error)

// Builtins are the store-agnostic resolvers cli-guard ships itself: no SDK, no
// wiring. A consumer entry of the same name overrides one via Merge.
func Builtins() map[string]Provider {
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
		"literal": func(_ context.Context, v string) (string, error) { return v, nil },
	}
}

// Merge layers the consumer's registry over the built-ins (consumer wins on a
// clash), always non-nil. A missing provider fails closed in Resolve.
func Merge(consumer map[string]Provider) map[string]Provider {
	out := Builtins()
	for name, p := range consumer {
		if p != nil {
			out[name] = p
		}
	}
	return out
}

// Resolve reads address through the named provider. A missing provider or a
// resolver error is returned for the caller to wrap; the value is never logged.
func Resolve(ctx context.Context, providers map[string]Provider, provider, address string) (string, error) {
	p := providers[provider]
	if p == nil {
		return "", fmt.Errorf("no provider registered for %q", provider)
	}
	return p(ctx, address)
}

// Source is one (provider, address) pair: where a value is read and the address
// the provider interprets. An ordered slice of these is a fallback chain.
type Source struct {
	Provider string
	Address  string
}

// ResolveFirst returns the first source resolving to a non-empty value with no
// error, else a combined error naming each tried source - never a value.
func ResolveFirst(ctx context.Context, providers map[string]Provider, sources []Source) (string, error) {
	if len(sources) == 0 {
		return "", fmt.Errorf("value chain names no source")
	}
	reasons := make([]string, 0, len(sources))
	for _, s := range sources {
		v, err := Resolve(ctx, providers, s.Provider, s.Address)
		switch {
		case err != nil:
			reasons = append(reasons, fmt.Sprintf("%s %s: %v", s.Provider, s.Address, err))
		case v == "":
			reasons = append(reasons, fmt.Sprintf("%s %s: resolved empty", s.Provider, s.Address))
		default:
			return v, nil
		}
	}
	return "", fmt.Errorf("no value source resolved: %s", strings.Join(reasons, "; "))
}
