// Package stscache caches `aws sts get-caller-identity` JSON for callers
// that re-resolve the active AWS identity on every invocation. STS
package stscache

import (
	"os"
	"path/filepath"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/config"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/ttlcache"
)

// TTL is the freshness window for cached identity responses. Chosen at
// the high end of "stable for hours" - role assumption sessions typically
const TTL = 1 * time.Hour

// get resolves the cache directory on every call rather than caching it
// behind sync.Once. Resolution is cheap (one getenv plus a join) and
func get() *ttlcache.Cache {
	return ttlcache.New(filepath.Join(config.CacheDir(), "aws-sts-identity"))
}

// CallerIdentity returns the JSON body of `aws sts get-caller-identity
// --output json`, served from cache if a fresh entry exists for the
func CallerIdentity(fetch func() ([]byte, error)) ([]byte, error) {
	return get().GetOrSet(profileKey(), TTL, fetch)
}

// Invalidate drops the cached entry for the active profile. Callers that
// know they just rotated credentials or assumed a different role can use
func Invalidate() error {
	return get().Invalidate(profileKey())
}

// profileKey reflects the AWS SDK's profile-selection precedence:
// AWS_PROFILE wins, then AWS_DEFAULT_PROFILE, then the literal
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
