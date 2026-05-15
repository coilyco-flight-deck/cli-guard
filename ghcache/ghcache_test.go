package ghcache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	// /user is a real gh-api path but not in any tier - the package
	// declines to cache rather than guess a TTL.
	_, _ = ghcache.GetJSON("/user", fetch)
	_, _ = ghcache.GetJSON("/user", fetch)
	if calls != 2 {
		t.Errorf("unclassified path should bypass cache, got calls=%d", calls)
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
