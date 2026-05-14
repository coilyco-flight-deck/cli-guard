// Package stscache caches `aws sts get-caller-identity` JSON for callers
// that re-resolve the active AWS identity on every coily invocation. STS
// identity is stable for hours and the rate-limit budget on STS is
// generous, so the value here is purely latency: skip a ~200ms subprocess
// when the answer is unchanged.
//
// Cache key is the active AWS profile so swapping AWS_PROFILE between
// runs never returns the wrong account. TTL is 1 hour, the high end of
// the stable-identity range. Failure mode matches ttlcache: a cache miss
// or corrupt entry falls through to the underlying fetch.
//
// See cli-guard#54 for the rollout that motivated this package.
package stscache

import (
	"os"
	"path/filepath"
	"time"

	"github.com/coilysiren/cli-guard/ttlcache"
)

// TTL is the freshness window for cached identity responses. Chosen at
// the high end of "stable for hours" - role assumption sessions typically
// last 1h, so anything longer risks returning an identity past its
// credential lifetime.
const TTL = 1 * time.Hour

// get resolves the cache directory on every call rather than caching it
// behind sync.Once. Resolution is cheap (one getenv plus a join) and
// tests can flip $COILY_CACHE_DIR / $HOME between calls without
// fighting package-level state.
func get() *ttlcache.Cache {
	dir := os.Getenv("COILY_CACHE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = os.TempDir()
		}
		dir = filepath.Join(home, ".coily", "cache")
	}
	return ttlcache.New(filepath.Join(dir, "aws-sts-identity"))
}

// CallerIdentity returns the JSON body of `aws sts get-caller-identity
// --output json`, served from cache if a fresh entry exists for the
// active AWS profile. On miss it invokes fetch and stores the result.
//
// fetch is the caller's runner-bound closure; this package does not
// import os/exec or any runner abstraction so cli-guard stays
// dependency-light.
func CallerIdentity(fetch func() ([]byte, error)) ([]byte, error) {
	return get().GetOrSet(profileKey(), TTL, fetch)
}

// Invalidate drops the cached entry for the active profile. Callers that
// know they just rotated credentials or assumed a different role can use
// this to force a refetch.
func Invalidate() error {
	return get().Invalidate(profileKey())
}

// profileKey reflects the AWS SDK's profile-selection precedence:
// AWS_PROFILE wins, then AWS_DEFAULT_PROFILE, then the literal
// "default". This matches what `aws sts get-caller-identity` would
// actually authenticate as, so cache entries never alias across
// identities.
func profileKey() string {
	p := os.Getenv("AWS_PROFILE")
	if p == "" {
		p = os.Getenv("AWS_DEFAULT_PROFILE")
	}
	if p == "" {
		p = "default"
	}
	return "sts:get-caller-identity:" + p
}
