package ghcache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coilysiren/cli-guard/ghcache"
)

func reset(t *testing.T) {
	t.Helper()
	t.Setenv("COILY_CACHE_DIR", t.TempDir())
	t.Setenv("GH_HOST", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
}

func TestGetJSON_IssueCachesAt120s(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{"number":1}`), nil
	}
	for i := 0; i < 5; i++ {
		v, err := ghcache.GetJSON("/repos/o/r/issues/1", fetch)
		if err != nil || string(v) != `{"number":1}` {
			t.Fatalf("GetJSON: v=%q err=%v", v, err)
		}
	}
	if calls != 1 {
		t.Errorf("fetch called %d times across 5 GETs, want 1 (Tier-3 should cache)", calls)
	}
}

func TestGetJSON_PRPathCaches(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/pulls/42", fetch)
	_, _ = ghcache.GetJSON("/repos/o/r/pulls/42", fetch)
	if calls != 1 {
		t.Errorf("pulls path: fetch called %d times, want 1", calls)
	}
}

func TestGetJSON_LeadingSlashNormalized(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	_, _ = ghcache.GetJSON("repos/o/r/issues/1", fetch)
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", fetch)
	if calls != 1 {
		t.Errorf("with/without leading slash should alias, calls=%d", calls)
	}
}

func TestGetJSON_UnclassifiedPathBypassesCache(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	// /notifications is a real gh-api path but not in any tier - the
	// package declines to cache rather than guess a TTL on a path
	// whose write semantics are unclear.
	_, _ = ghcache.GetJSON("/notifications", fetch)
	_, _ = ghcache.GetJSON("/notifications", fetch)
	if calls != 2 {
		t.Errorf("unclassified path should bypass cache, got calls=%d", calls)
	}
}

func TestGetJSON_RateLimitNotCached(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	// /rate_limit is deliberately unclassified - callers hit it to get
	// ground truth, so caching it would defeat the purpose.
	_, _ = ghcache.GetJSON("/rate_limit", fetch)
	_, _ = ghcache.GetJSON("/rate_limit", fetch)
	if calls != 2 {
		t.Errorf("/rate_limit should bypass cache, got calls=%d", calls)
	}
}

func TestClassify_Tiers(t *testing.T) {
	// One representative path per regex per tier. The exported TTL
	// constants are the source of truth.
	cases := []struct {
		path string
		ttl  time.Duration
	}{
		// Tier 1 - 1 minute.
		{"/repos/o/r/issues", ghcache.TTL1Min},
		{"/repos/o/r/issues/1", ghcache.TTL1Min},
		{"/repos/o/r/issues/1/comments", ghcache.TTL1Min},
		{"/repos/o/r/issues/1/events", ghcache.TTL1Min},
		{"/repos/o/r/pulls", ghcache.TTL1Min},
		{"/repos/o/r/pulls/2", ghcache.TTL1Min},
		{"/repos/o/r/pulls/2/reviews", ghcache.TTL1Min},
		{"/repos/o/r/pulls/2/comments", ghcache.TTL1Min},
		{"/repos/o/r/pulls/2/files", ghcache.TTL1Min},
		{"/repos/o/r/commits/abc123/status", ghcache.TTL1Min},
		{"/repos/o/r/commits/abc123/check-runs", ghcache.TTL1Min},
		{"/repos/o/r/actions/runs", ghcache.TTL1Min},
		{"/repos/o/r/actions/runs/77", ghcache.TTL1Min},
		{"/repos/o/r/actions/runs/77/jobs", ghcache.TTL1Min},

		// Tier 2 - 10 minutes.
		{"/repos/o/r/commits", ghcache.TTL10Min},
		{"/repos/o/r/commits/abc123", ghcache.TTL10Min},
		{"/repos/o/r/compare/main...feature", ghcache.TTL10Min},
		{"/repos/o/r/branches", ghcache.TTL10Min},
		{"/repos/o/r/branches/main", ghcache.TTL10Min},
		{"/repos/o/r/git/refs", ghcache.TTL10Min},
		{"/repos/o/r/git/refs/heads/main", ghcache.TTL10Min},
		{"/repos/o/r/git/trees/abc123", ghcache.TTL10Min},
		{"/repos/o/r/contents/README.md", ghcache.TTL10Min},
		{"/search/issues", ghcache.TTL10Min},
		{"/search/code", ghcache.TTL10Min},
		{"/search/repositories", ghcache.TTL10Min},

		// Tier 3 - 1 hour.
		{"/repos/o/r", ghcache.TTL1Hour},
		{"/repos/o/r/labels", ghcache.TTL1Hour},
		{"/repos/o/r/labels/bug", ghcache.TTL1Hour},
		{"/repos/o/r/milestones", ghcache.TTL1Hour},
		{"/repos/o/r/topics", ghcache.TTL1Hour},
		{"/repos/o/r/languages", ghcache.TTL1Hour},
		{"/repos/o/r/contributors", ghcache.TTL1Hour},
		{"/repos/o/r/collaborators", ghcache.TTL1Hour},
		{"/repos/o/r/tags", ghcache.TTL1Hour},
		{"/repos/o/r/releases", ghcache.TTL1Hour},
		{"/repos/o/r/releases/42", ghcache.TTL1Hour},
		{"/repos/o/r/releases/latest", ghcache.TTL1Hour},
		{"/repos/o/r/releases/tags/v1.2.3", ghcache.TTL1Hour},

		// Tier 4 - 25 hours.
		{"/user", ghcache.TTL25Hour},
		{"/users/coilysiren", ghcache.TTL25Hour},
		{"/orgs/coilysiren", ghcache.TTL25Hour},
		{"/orgs/coilysiren/members", ghcache.TTL25Hour},
		{"/orgs/coilysiren/teams", ghcache.TTL25Hour},
		{"/orgs/coilysiren/teams/eng", ghcache.TTL25Hour},
		{"/orgs/coilysiren/teams/eng/members", ghcache.TTL25Hour},
		{"/search/users", ghcache.TTL25Hour},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			reset(t)
			calls := 0
			fetch := func() ([]byte, error) {
				calls++
				return []byte(`{}`), nil
			}
			// Two consecutive GETs of a classified path must collapse
			// to one fetch.
			_, _ = ghcache.GetJSON(tc.path, fetch)
			_, _ = ghcache.GetJSON(tc.path, fetch)
			if calls != 1 {
				t.Errorf("%s: expected cache hit (tier=%s), got calls=%d", tc.path, tc.ttl, calls)
			}
		})
	}
}

func TestInvalidate_IssueBodyDropsList(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`[]`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/issues", fetch)
	// PATCHing an individual issue changes its updated_at, which
	// reorders the list. Drop the list key.
	ghcache.Invalidate("PATCH", "/repos/o/r/issues/1")
	_, _ = ghcache.GetJSON("/repos/o/r/issues", fetch)
	if calls != 2 {
		t.Errorf("issue PATCH should drop list: calls=%d, want 2", calls)
	}
}

func TestInvalidate_PRReviewDropsParent(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/pulls/2", fetch)
	// POSTing a review changes the PR's review-state aggregate.
	ghcache.Invalidate("POST", "/repos/o/r/pulls/2/reviews")
	_, _ = ghcache.GetJSON("/repos/o/r/pulls/2", fetch)
	if calls != 2 {
		t.Errorf("PR review write should drop parent PR: calls=%d, want 2", calls)
	}
}

func TestInvalidate_LabelEditDropsLabelsList(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`[]`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/labels", fetch)
	ghcache.Invalidate("PATCH", "/repos/o/r/labels/bug")
	_, _ = ghcache.GetJSON("/repos/o/r/labels", fetch)
	if calls != 2 {
		t.Errorf("label edit should drop labels list: calls=%d, want 2", calls)
	}
}

func TestInvalidate_ReleaseEditDropsListAndLatest(t *testing.T) {
	reset(t)
	listCalls := 0
	latestCalls := 0
	_, _ = ghcache.GetJSON("/repos/o/r/releases", func() ([]byte, error) {
		listCalls++
		return []byte(`[]`), nil
	})
	_, _ = ghcache.GetJSON("/repos/o/r/releases/latest", func() ([]byte, error) {
		latestCalls++
		return []byte(`{}`), nil
	})
	ghcache.Invalidate("PATCH", "/repos/o/r/releases/42")
	_, _ = ghcache.GetJSON("/repos/o/r/releases", func() ([]byte, error) {
		listCalls++
		return []byte(`[]`), nil
	})
	_, _ = ghcache.GetJSON("/repos/o/r/releases/latest", func() ([]byte, error) {
		latestCalls++
		return []byte(`{}`), nil
	})
	if listCalls != 2 || latestCalls != 2 {
		t.Errorf("release edit should drop list + latest: list=%d latest=%d", listCalls, latestCalls)
	}
}

func TestGetJSON_DifferentTokensDontAlias(t *testing.T) {
	reset(t)

	t.Setenv("GH_TOKEN", "alpha")
	if _, err := ghcache.GetJSON("/repos/o/r/issues/1", func() ([]byte, error) {
		return []byte(`{"who":"alpha"}`), nil
	}); err != nil {
		t.Fatalf("alpha: %v", err)
	}

	t.Setenv("GH_TOKEN", "beta")
	betaCalls := 0
	got, err := ghcache.GetJSON("/repos/o/r/issues/1", func() ([]byte, error) {
		betaCalls++
		return []byte(`{"who":"beta"}`), nil
	})
	if err != nil {
		t.Fatalf("beta: %v", err)
	}
	if string(got) != `{"who":"beta"}` || betaCalls != 1 {
		t.Errorf("token-swap leak: got=%q calls=%d", got, betaCalls)
	}
}

func TestGetJSON_QueryParamOrderAliases(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`[]`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/issues?state=open&labels=bug", fetch)
	_, _ = ghcache.GetJSON("/repos/o/r/issues?labels=bug&state=open", fetch)
	if calls != 1 {
		t.Errorf("query-order alias: calls=%d, want 1", calls)
	}
}

func TestGetJSON_DifferentQueriesDontAlias(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`[]`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/issues?state=open", fetch)
	_, _ = ghcache.GetJSON("/repos/o/r/issues?state=closed", fetch)
	if calls != 2 {
		t.Errorf("distinct queries should not alias: calls=%d", calls)
	}
}

func TestInvalidate_DropsExactPath(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", fetch)
	ghcache.Invalidate("PATCH", "/repos/o/r/issues/1")
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", fetch)
	if calls != 2 {
		t.Errorf("Invalidate should force refetch: calls=%d, want 2", calls)
	}
}

func TestInvalidate_CommentInvalidatesParent(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", fetch)
	// A POST to the comments endpoint should drop the parent issue's
	// cached body (the comment count changed).
	ghcache.Invalidate("POST", "/repos/o/r/issues/1/comments")
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", fetch)
	if calls != 2 {
		t.Errorf("comment write should drop parent: calls=%d, want 2", calls)
	}
}

func TestInvalidate_GETIsNoop(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", fetch)
	ghcache.Invalidate("GET", "/repos/o/r/issues/1")
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", fetch)
	if calls != 1 {
		t.Errorf("GET Invalidate should be a no-op: calls=%d, want 1", calls)
	}
}

func TestGetJSON_FetchErrorNotCached(t *testing.T) {
	reset(t)
	wantErr := errors.New("rate limit")
	bad := func() ([]byte, error) { return nil, wantErr }
	_, err := ghcache.GetJSON("/repos/o/r/issues/1", bad)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wantErr", err)
	}
	// Next call must still call the fetcher - error is not cached.
	calls := 0
	_, err = ghcache.GetJSON("/repos/o/r/issues/1", func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	})
	if err != nil || calls != 1 {
		t.Errorf("after error: calls=%d err=%v, want 1 / nil", calls, err)
	}
}

func TestGetJSON_BurstCollapsesToOneFetch(t *testing.T) {
	// Tier-3 acceptance criterion from cli-guard#56: 100 repeat dispatch
	// calls against the same issue should produce 1 fetch + 99 hits.
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{"number":99}`), nil
	}
	for i := 0; i < 100; i++ {
		v, err := ghcache.GetJSON("/repos/o/r/issues/99", fetch)
		if err != nil || string(v) != `{"number":99}` {
			t.Fatalf("burst[%d]: v=%q err=%v", i, v, err)
		}
	}
	if calls != 1 {
		t.Errorf("burst: fetch called %d times, want 1", calls)
	}
}

func TestGetJSON_HomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COILY_CACHE_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("GH_TOKEN", "")
	_, _ = ghcache.GetJSON("/repos/o/r/issues/1", func() ([]byte, error) {
		return []byte(`{}`), nil
	})
	if _, err := os.Stat(filepath.Join(home, ".coily", "cache", "gh-api-cache")); err != nil {
		t.Errorf("home-fallback cache dir not created: %v", err)
	}
}

func TestGetJSON_RepoMetaCached(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{}`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r", fetch)
	_, _ = ghcache.GetJSON("/repos/o/r", fetch)
	if calls != 1 {
		t.Errorf("Tier-2 repo meta should cache: calls=%d", calls)
	}
}

func TestGetJSON_LabelsCached(t *testing.T) {
	reset(t)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`[]`), nil
	}
	_, _ = ghcache.GetJSON("/repos/o/r/labels", fetch)
	_, _ = ghcache.GetJSON("/repos/o/r/labels", fetch)
	if calls != 1 {
		t.Errorf("labels should cache: calls=%d", calls)
	}
}
