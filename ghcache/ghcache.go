// Package ghcache caches GitHub REST `GET` responses keyed by method,
// path, body, and token fingerprint, with method-aware write-through
// invalidation. Drives the high-volume coily dispatch path: a burst of
// `coily dispatch coilysiren/X#N` against the same issue collapses to one
// `gh api /repos/X/issues/N` call plus cache hits, instead of one
// subprocess per dispatch.
//
// Tiered TTLs reflect how stable each surface is. Issue/PR bodies move on
// human timescales (minutes); labels and topics move on weeks; lists and
// search results turn over with every comment, so they get the shortest
// TTL. Tier classification lives in this package so the choice is
// declarative and reviewable.
//
// Write-through invalidation: when a caller issues a `POST`, `PATCH`, or
// `DELETE` to a path, the matching `GET` entry is dropped before the next
// read. Comment writes also drop the parent issue/PR entry, since the
// `comments` count on the issue body changes. The cache is not
// load-bearing for correctness - on a missed invalidation, the next read
// returns slightly stale data for at most the tier's TTL window.
//
// Failure mode matches ttlcache: cache miss or corrupt entry falls
// through to the underlying fetch. See cli-guard#56 for the rollout.
package ghcache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/coilysiren/cli-guard/ttlcache"
)

// Default TTLs by tier. Exported so callers and tests can reference the
// same constants the path-classifier uses internally.
const (
	TTLIssueOrPR  = 120 * time.Second // Tier-3: issue/PR body
	TTLRepoMeta   = 15 * time.Minute  // Tier-2: repo description, topics
	TTLRepoLabels = 1 * time.Hour     // Tier-2: label list
	TTLListSearch = 60 * time.Second  // Tier-4: issues list, search
)

// get returns a ttlcache rooted under the gh-api-cache subdir. The
// directory is shared across tiers; per-entry TTLs encode the freshness
// window so a single directory can hold mixed-tier entries.
func get() *ttlcache.Cache {
	dir := os.Getenv("COILY_CACHE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = os.TempDir()
		}
		dir = filepath.Join(home, ".coily", "cache")
	}
	return ttlcache.New(filepath.Join(dir, "gh-api-cache"))
}

// GetJSON fetches the response body for a `GET <path>` GitHub REST call,
// served from cache if a fresh entry exists. On miss it invokes fetch
// and stores the result under a TTL derived from the path tier.
//
// The path is the gh-api-style request path - either `/repos/O/R/issues/N`
// or `repos/O/R/issues/N` (leading slash is normalized). Query strings
// are included for cache-key purposes so paginated reads of the same
// endpoint produce distinct entries.
func GetJSON(path string, fetch func() ([]byte, error)) ([]byte, error) {
	ttl := classify(path)
	if ttl == 0 {
		// Path doesn't match any tier - skip the cache entirely rather
		// than guess a TTL that might be too long for a write-sensitive
		// endpoint.
		return fetch()
	}
	return get().GetOrSet(cacheKey("GET", path, nil), ttl, fetch)
}

// Invalidate drops cache entries that a `method <path>` write would
// affect. Idempotent and safe to call on paths that have no cached
// entry. Comment writes also invalidate the parent issue/PR so the
// `comments` count stays consistent.
//
// Method is normalized to upper-case. method == "GET" is a no-op (reads
// don't invalidate anything).
func Invalidate(method, path string) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || method == "GET" {
		return
	}
	c := get()
	// Drop the direct GET entry for the path being written.
	_ = c.Invalidate(cacheKey("GET", path, nil))
	// Comment writes (POST /repos/O/R/issues/N/comments) also bump the
	// parent issue's `comments` field. Invalidate that too.
	if parent := commentParent(path); parent != "" {
		_ = c.Invalidate(cacheKey("GET", parent, nil))
	}
}

// classify returns the TTL for a path or 0 if no tier matches. The
// regexes are intentionally narrow - paths the caller might mutate
// (e.g. /repos/O/R/issues/N/comments) deliberately fall through to
// uncached so a write race doesn't get masked by a stale read.
func classify(path string) time.Duration {
	p, _ := splitQuery(normalizePath(path))
	switch {
	case reIssueOrPR.MatchString(p):
		return TTLIssueOrPR
	case reRepoLabels.MatchString(p):
		return TTLRepoLabels
	case reRepoBase.MatchString(p):
		return TTLRepoMeta
	case reIssuesList.MatchString(p), reSearch.MatchString(p):
		return TTLListSearch
	}
	return 0
}

// commentParent returns the parent issue/PR path that a comment write
// affects, or "" if path is not a comment endpoint.
//
//	POST /repos/O/R/issues/N/comments  -> /repos/O/R/issues/N
//	POST /repos/O/R/pulls/N/comments   -> /repos/O/R/pulls/N
func commentParent(path string) string {
	p := normalizePath(path)
	m := reCommentEndpoint.FindStringSubmatch(p)
	if m == nil {
		return ""
	}
	return m[1]
}

// cacheKey produces the on-disk key. Layout: method:path:bodyhash:host:tokenfp.
// Sorting query params (when present) so callers that build URLs in
// different orders still alias. body is nil for GET, the raw request body
// otherwise (POST/PATCH bodies are not cached today but the signature
// reserves the slot for symmetry with cli-guard#56's spec).
func cacheKey(method, path string, body []byte) string {
	method = strings.ToUpper(method)
	p, q := splitQuery(normalizePath(path))
	bh := "no-body"
	if len(body) > 0 {
		h := sha256.Sum256(body)
		bh = hex.EncodeToString(h[:8])
	}
	host := os.Getenv("GH_HOST")
	if host == "" {
		host = "github.com"
	}
	tok := os.Getenv("GH_TOKEN")
	if tok == "" {
		tok = os.Getenv("GITHUB_TOKEN")
	}
	return strings.Join([]string{
		method,
		p,
		sortedQuery(q),
		bh,
		host,
		tokenFingerprint(tok),
	}, ":")
}

func normalizePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func splitQuery(p string) (path, query string) {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

// sortedQuery returns the query string with key=value pairs sorted by
// key so URLs that differ only in param order produce the same key.
func sortedQuery(q string) string {
	if q == "" {
		return ""
	}
	parts := strings.Split(q, "&")
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

// tokenFingerprint is the same shape as ghidcache's: short sha256 of the
// token, or a stable "no-env-token" marker for keyring-auth runs. Keeps
// different tokens from aliasing in the cache without writing the token
// itself to disk.
func tokenFingerprint(tok string) string {
	if tok == "" {
		return "no-env-token"
	}
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:8])
}

var (
	// reIssueOrPR matches /repos/{O}/{R}/issues/{N} or /pulls/{N} - the
	// Tier-3 hot path for `coily dispatch`. Excludes /comments and any
	// other sub-resource so writes against those decline caching.
	reIssueOrPR = regexp.MustCompile(`^/repos/[^/]+/[^/]+/(issues|pulls)/\d+$`)

	// reCommentEndpoint matches a comment write endpoint and captures
	// the parent issue/PR path for write-through invalidation.
	reCommentEndpoint = regexp.MustCompile(`^(/repos/[^/]+/[^/]+/(?:issues|pulls)/\d+)/comments$`)

	reRepoBase   = regexp.MustCompile(`^/repos/[^/]+/[^/]+$`)
	reRepoLabels = regexp.MustCompile(`^/repos/[^/]+/[^/]+/labels$`)
	reIssuesList = regexp.MustCompile(`^/repos/[^/]+/[^/]+/(issues|pulls)$`)
	reSearch     = regexp.MustCompile(`^/search/`)
)
