// Package ghcache caches GitHub REST `GET` responses keyed by method,
// path, body, and token fingerprint, with method-aware write-through
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
	// TTLVolatile covers fast-moving issue/PR/CI surfaces. Sized to absorb a
	// `watch -n 2` loop across ~10 repos (~10 calls/min, well under the
	TTLVolatile = 1 * time.Minute

	// TTLBranchHead covers head-of-branch reads, ref/tree/content lookups,
	// and search. Slower turnover than issues; long enough to collapse
	TTLBranchHead = 10 * time.Minute

	// TTLRepoMeta covers repo-level metadata: the repo body, labels,
	// milestones, releases, tags, topics, languages, contributors,
	TTLRepoMeta = 1 * time.Hour

	// TTLIdentity covers the identity surface: user, org, teams. Picked
	// at 25 (not 24) hours so a daily cron always sees a miss across
	TTLIdentity = 25 * time.Hour
)

// get returns a ttlcache rooted under the gh-api-cache subdir. The
// directory is shared across tiers; per-entry TTLs encode the freshness
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
func GetJSON(path string, fetch func() ([]byte, error)) ([]byte, error) {
	ttl := classify(path)
	if ttl == 0 {
		// Path doesn't match any tier - skip the cache entirely rather
		// than guess a TTL that might be too long for a write-sensitive
		return fetch()
	}
	return get().GetOrSet(cacheKey("GET", path, nil), ttl, fetch)
}

// MaybeServe is the lookup-only half of the cache. Returns (cached
// bytes, true) on a fresh hit, (nil, false) on miss or unclassified
func MaybeServe(path string) ([]byte, bool) {
	if classify(path) == 0 {
		return nil, false
	}
	return get().Get(cacheKey("GET", path, nil))
}

// MaybeServeMaxAge is the per-call-freshness variant of MaybeServe.
// Returns (cached bytes, true) only when a cached entry exists AND its
func MaybeServeMaxAge(path string, maxAge time.Duration) ([]byte, bool) {
	if classify(path) == 0 {
		return nil, false
	}
	return get().GetMaxAge(cacheKey("GET", path, nil), maxAge)
}

// Store puts data under path using the TTL derived from the path's
// tier. Returns false if the path is unclassified (caller should be
func Store(path string, data []byte) bool {
	ttl := classify(path)
	if ttl == 0 {
		return false
	}
	_ = get().Set(cacheKey("GET", path, nil), data, ttl)
	return true
}

// Invalidate drops cache entries that a `method <path>` write would
// affect. Idempotent and safe to call on paths that have no cached
func Invalidate(method, path string) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || method == "GET" {
		return
	}
	c := get()
	drop := func(p string) {
		_ = c.Invalidate(cacheKey("GET", p, nil))
	}
	drop(path)
	for _, parent := range affectedReadKeys(path) {
		drop(parent)
	}
}

// affectedReadKeys returns the read endpoints a write to `path`
// invalidates beyond the direct path. The list captures the realistic
func affectedReadKeys(path string) []string {
	p := normalizePath(path)
	var out []string
	if m := reCommentEndpoint.FindStringSubmatch(p); m != nil {
		// /repos/O/R/{issues,pulls}/N/comments -> parent body
		out = append(out, m[1])
		return out
	}
	if m := rePRReviewsEndpoint.FindStringSubmatch(p); m != nil {
		// /repos/O/R/pulls/N/reviews -> parent PR body
		out = append(out, m[1])
		return out
	}
	if m := reIssueOrPRBody.FindStringSubmatch(p); m != nil {
		// /repos/O/R/{issues,pulls}/N -> the list endpoint
		out = append(out, "/repos/"+m[1]+"/"+m[2]+"/"+m[3])
		return out
	}
	if m := reLabelNamed.FindStringSubmatch(p); m != nil {
		// /repos/O/R/labels/{name} -> the labels list
		out = append(out, "/repos/"+m[1]+"/"+m[2]+"/labels")
		return out
	}
	if m := reReleaseEntry.FindStringSubmatch(p); m != nil {
		// /repos/O/R/releases/{id|latest|tags/...} -> list + latest
		base := "/repos/" + m[1] + "/" + m[2] + "/releases"
		out = append(out, base, base+"/latest")
		return out
	}
	return out
}

// classify returns the TTL for a path or 0 if no tier matches. Order
// matters: more specific patterns are tested before the catch-alls
func classify(path string) time.Duration {
	p, _ := splitQuery(normalizePath(path))
	switch {
	// Tier 1 - 1 minute.
	case reT1IssuesList.MatchString(p),
		reT1IssueBody.MatchString(p),
		reT1IssueSub.MatchString(p),
		reT1PullsList.MatchString(p),
		reT1PRBody.MatchString(p),
		reT1PRSub.MatchString(p),
		reT1CommitChecks.MatchString(p),
		reT1ActionsRunsList.MatchString(p),
		reT1ActionsRun.MatchString(p),
		reT1ActionsRunJobs.MatchString(p):
		return TTLVolatile

	// Tier 2 - 10 minutes.
	case reT2CommitsList.MatchString(p),
		reT2Commit.MatchString(p),
		reT2Compare.MatchString(p),
		reT2BranchesList.MatchString(p),
		reT2Branch.MatchString(p),
		reT2GitRefs.MatchString(p),
		reT2GitTrees.MatchString(p),
		reT2Contents.MatchString(p),
		reT2SearchNonUsers.MatchString(p):
		return TTLBranchHead

	// Tier 3 - 1 hour.
	case reT3RepoBase.MatchString(p),
		reT3RepoLists.MatchString(p),
		reT3LabelNamed.MatchString(p),
		reT3ReleaseEntry.MatchString(p):
		return TTLRepoMeta

	// Tier 4 - 25 hours.
	case reT4User.MatchString(p),
		reT4UsersNamed.MatchString(p),
		reT4OrgBase.MatchString(p),
		reT4OrgLists.MatchString(p),
		reT4Team.MatchString(p),
		reT4SearchUsers.MatchString(p):
		return TTLIdentity
	}
	return 0
}

// cacheKey produces the on-disk key. Layout: method:path:bodyhash:host:tokenfp.
func cacheKey(method, path string, body []byte) string { //nolint:unparam // method always "GET" today; slot reserved for future write-cache.
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
func tokenFingerprint(tok string) string {
	if tok == "" {
		return "no-env-token"
	}
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:8])
}

// Path classifiers and invalidation helpers. Tested as a unit in
// ghcache_test.go.
var (
	// Tier 1 - 1 minute. Fast-moving issue/PR/CI surfaces.
	reT1IssuesList      = regexp.MustCompile(`^/repos/[^/]+/[^/]+/issues$`)
	reT1IssueBody       = regexp.MustCompile(`^/repos/[^/]+/[^/]+/issues/\d+$`)
	reT1IssueSub        = regexp.MustCompile(`^/repos/[^/]+/[^/]+/issues/\d+/(comments|events)$`)
	reT1PullsList       = regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls$`)
	reT1PRBody          = regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls/\d+$`)
	reT1PRSub           = regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls/\d+/(reviews|comments|files)$`)
	reT1CommitChecks    = regexp.MustCompile(`^/repos/[^/]+/[^/]+/commits/[^/]+/(status|check-runs)$`)
	reT1ActionsRunsList = regexp.MustCompile(`^/repos/[^/]+/[^/]+/actions/runs$`)
	reT1ActionsRun      = regexp.MustCompile(`^/repos/[^/]+/[^/]+/actions/runs/\d+$`)
	reT1ActionsRunJobs  = regexp.MustCompile(`^/repos/[^/]+/[^/]+/actions/runs/\d+/jobs$`)

	// Tier 2 - 10 minutes. Branch/ref/content reads, search.
	reT2CommitsList    = regexp.MustCompile(`^/repos/[^/]+/[^/]+/commits$`)
	reT2Commit         = regexp.MustCompile(`^/repos/[^/]+/[^/]+/commits/[^/]+$`)
	reT2Compare        = regexp.MustCompile(`^/repos/[^/]+/[^/]+/compare/.+$`)
	reT2BranchesList   = regexp.MustCompile(`^/repos/[^/]+/[^/]+/branches$`)
	reT2Branch         = regexp.MustCompile(`^/repos/[^/]+/[^/]+/branches/[^/]+$`)
	reT2GitRefs        = regexp.MustCompile(`^/repos/[^/]+/[^/]+/git/refs(/.+)?$`)
	reT2GitTrees       = regexp.MustCompile(`^/repos/[^/]+/[^/]+/git/trees/.+$`)
	reT2Contents       = regexp.MustCompile(`^/repos/[^/]+/[^/]+/contents/.*$`)
	reT2SearchNonUsers = regexp.MustCompile(`^/search/(issues|code|repositories)$`)

	// Tier 3 - 1 hour. Repo metadata.
	reT3RepoBase     = regexp.MustCompile(`^/repos/[^/]+/[^/]+$`)
	reT3RepoLists    = regexp.MustCompile(`^/repos/[^/]+/[^/]+/(labels|milestones|topics|languages|contributors|collaborators|tags|releases)$`)
	reT3LabelNamed   = regexp.MustCompile(`^/repos/[^/]+/[^/]+/labels/[^/]+$`)
	reT3ReleaseEntry = regexp.MustCompile(`^/repos/[^/]+/[^/]+/releases/(\d+|latest|tags/.+)$`)

	// Tier 4 - 25 hours. Identity surface.
	reT4User        = regexp.MustCompile(`^/user$`)
	reT4UsersNamed  = regexp.MustCompile(`^/users/[^/]+$`)
	reT4OrgBase     = regexp.MustCompile(`^/orgs/[^/]+$`)
	reT4OrgLists    = regexp.MustCompile(`^/orgs/[^/]+/(members|teams)$`)
	reT4Team        = regexp.MustCompile(`^/orgs/[^/]+/teams/[^/]+(/members)?$`)
	reT4SearchUsers = regexp.MustCompile(`^/search/users$`)

	// Invalidation helpers. Captures are used to build parent read keys
	// (see affectedReadKeys).
	reCommentEndpoint   = regexp.MustCompile(`^(/repos/[^/]+/[^/]+/(?:issues|pulls)/\d+)/comments$`)
	rePRReviewsEndpoint = regexp.MustCompile(`^(/repos/[^/]+/[^/]+/pulls/\d+)/reviews$`)
	reIssueOrPRBody     = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/(issues|pulls)/\d+$`)
	reLabelNamed        = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/labels/[^/]+$`)
	reReleaseEntry      = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/releases/(?:\d+|latest|tags/.+)$`)
)
