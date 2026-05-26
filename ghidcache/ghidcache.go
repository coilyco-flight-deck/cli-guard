// Package ghidcache caches GitHub identity reads - `gh auth status` and
// `gh api user` - that re-resolve on every coily invocation but are stable
package ghidcache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/coilysiren/cli-guard/ttlcache"
)

// TTL is the freshness window for cached identity responses. 1h matches
// stscache and sits at the high end of "stable for hours" - a token
const TTL = 1 * time.Hour

// get resolves the cache directory on every call rather than memoizing
// behind sync.Once. Resolution is cheap and tests can flip
func get(subdir string) *ttlcache.Cache {
	dir := os.Getenv("COILY_CACHE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = os.TempDir()
		}
		dir = filepath.Join(home, ".coily", "cache")
	}
	return ttlcache.New(filepath.Join(dir, subdir))
}

// APIUser returns the JSON body of `gh api user`, served from cache if a
// fresh entry exists for the active GH_HOST + token. On miss it invokes
func APIUser(fetch func() ([]byte, error)) ([]byte, error) {
	return get("gh-api-user").GetOrSet(identityKey("gh:api:user"), TTL, fetch)
}

// AuthStatus returns the captured output of `gh auth status` (stderr,
// since gh writes the human-readable host/scope readout there).
func AuthStatus(fetch func() ([]byte, error)) ([]byte, error) {
	return get("gh-auth-status").GetOrSet(identityKey("gh:auth:status"), TTL, fetch)
}

// Invalidate drops every cached identity entry under both subdirs.
// Callers that just rotated a token or switched GH_HOST can use this to
func Invalidate() error {
	_ = get("gh-api-user").Invalidate(identityKey("gh:api:user"))
	return get("gh-auth-status").Invalidate(identityKey("gh:auth:status"))
}

// identityKey blends the call type, the active GH_HOST, and a token
// fingerprint. Token precedence matches gh CLI: GH_TOKEN wins over
func identityKey(prefix string) string {
	host := os.Getenv("GH_HOST")
	if host == "" {
		host = "github.com"
	}
	tok := os.Getenv("GH_TOKEN")
	if tok == "" {
		tok = os.Getenv("GITHUB_TOKEN")
	}
	return prefix + ":" + host + ":" + tokenFingerprint(tok)
}

// tokenFingerprint produces a short stable identifier for a token without
// retaining the token itself. 16 hex chars of sha256 is ~64 bits of
func tokenFingerprint(tok string) string {
	if tok == "" {
		return "no-env-token"
	}
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:8])
}
