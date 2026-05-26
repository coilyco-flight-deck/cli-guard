package ghidcache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/coilysiren/cli-guard/ghidcache"
)

// resetCacheDir points ghidcache at a fresh tempdir and clears the gh
// identity env vars so subtests do not alias each other through the
func resetCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("COILY_CACHE_DIR", dir)
	t.Setenv("GH_HOST", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	return dir
}

func TestAPIUser_FetchOnMiss(t *testing.T) {
	resetCacheDir(t)
	_ = ghidcache.Invalidate()
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{"login":"kai"}`), nil
	}
	v, err := ghidcache.APIUser(fetch)
	if err != nil {
		t.Fatalf("APIUser: %v", err)
	}
	if string(v) != `{"login":"kai"}` {
		t.Errorf("APIUser = %q, want fetched JSON", v)
	}
	if _, err := ghidcache.APIUser(fetch); err != nil {
		t.Fatalf("APIUser (cached): %v", err)
	}
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1", calls)
	}
}

func TestAPIUser_PerTokenKey(t *testing.T) {
	resetCacheDir(t)
	_ = ghidcache.Invalidate()

	t.Setenv("GH_TOKEN", "alpha-token")
	if _, err := ghidcache.APIUser(func() ([]byte, error) {
		return []byte(`{"login":"alpha"}`), nil
	}); err != nil {
		t.Fatalf("alpha fetch: %v", err)
	}

	// Swap the token. The alpha entry must not leak into the beta
	// identity even though both runs see the same GH_HOST.
	t.Setenv("GH_TOKEN", "beta-token")
	betaCalls := 0
	got, err := ghidcache.APIUser(func() ([]byte, error) {
		betaCalls++
		return []byte(`{"login":"beta"}`), nil
	})
	if err != nil {
		t.Fatalf("beta fetch: %v", err)
	}
	if string(got) != `{"login":"beta"}` {
		t.Errorf("beta = %q, want beta (token-swap leaked alpha's entry)", got)
	}
	if betaCalls != 1 {
		t.Errorf("beta fetch called %d times, want 1", betaCalls)
	}

	// Switch back to alpha. The alpha entry should still be warm.
	t.Setenv("GH_TOKEN", "alpha-token")
	alphaCalls := 0
	got, err = ghidcache.APIUser(func() ([]byte, error) {
		alphaCalls++
		return []byte(`{"login":"alpha-refetched"}`), nil
	})
	if err != nil {
		t.Fatalf("alpha re-read: %v", err)
	}
	if string(got) != `{"login":"alpha"}` {
		t.Errorf("alpha re-read = %q, want the original alpha entry", got)
	}
	if alphaCalls != 0 {
		t.Errorf("alpha fetch called %d times, want 0", alphaCalls)
	}
}

func TestAPIUser_GitHubTokenFallback(t *testing.T) {
	resetCacheDir(t)
	_ = ghidcache.Invalidate()
	// GH_TOKEN wins over GITHUB_TOKEN. Confirm GITHUB_TOKEN gets
	// picked up when GH_TOKEN is empty.
	t.Setenv("GITHUB_TOKEN", "from-github-env")
	if _, err := ghidcache.APIUser(func() ([]byte, error) {
		return []byte(`{"login":"gh-env"}`), nil
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Setting GH_TOKEN to a different value should miss, since the
	// fingerprint changes.
	t.Setenv("GH_TOKEN", "different")
	calls := 0
	if _, err := ghidcache.APIUser(func() ([]byte, error) {
		calls++
		return []byte(`{"login":"different"}`), nil
	}); err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 (GH_TOKEN should outrank GITHUB_TOKEN)", calls)
	}
}

func TestAuthStatus_SeparateFromAPIUser(t *testing.T) {
	dir := resetCacheDir(t)
	_ = ghidcache.Invalidate()
	if _, err := ghidcache.APIUser(func() ([]byte, error) {
		return []byte(`{"login":"x"}`), nil
	}); err != nil {
		t.Fatalf("APIUser: %v", err)
	}
	if _, err := ghidcache.AuthStatus(func() ([]byte, error) {
		return []byte("Logged in to github.com"), nil
	}); err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	for _, sub := range []string{"gh-api-user", "gh-auth-status"} {
		matches, err := filepath.Glob(filepath.Join(dir, sub, "*.json"))
		if err != nil || len(matches) != 1 {
			t.Errorf("%s: matches=%v err=%v (want exactly one cache file)", sub, matches, err)
		}
	}
}

func TestAPIUser_FetchErrorPropagates(t *testing.T) {
	resetCacheDir(t)
	_ = ghidcache.Invalidate()
	wantErr := errors.New("gh unreachable")
	_, err := ghidcache.APIUser(func() ([]byte, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("APIUser error = %v, want errors.Is(_, wantErr)", err)
	}
}

func TestInvalidate_DropsBothSubdirs(t *testing.T) {
	resetCacheDir(t)
	_ = ghidcache.Invalidate()
	apiCalls, authCalls := 0, 0
	apiFetch := func() ([]byte, error) {
		apiCalls++
		return []byte(`{"login":"x"}`), nil
	}
	authFetch := func() ([]byte, error) {
		authCalls++
		return []byte("status"), nil
	}
	if _, err := ghidcache.APIUser(apiFetch); err != nil {
		t.Fatalf("APIUser: %v", err)
	}
	if _, err := ghidcache.AuthStatus(authFetch); err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if err := ghidcache.Invalidate(); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := ghidcache.APIUser(apiFetch); err != nil {
		t.Fatalf("APIUser refetch: %v", err)
	}
	if _, err := ghidcache.AuthStatus(authFetch); err != nil {
		t.Fatalf("AuthStatus refetch: %v", err)
	}
	if apiCalls != 2 || authCalls != 2 {
		t.Errorf("after Invalidate: apiCalls=%d authCalls=%d, want 2 each", apiCalls, authCalls)
	}
}

func TestAPIUser_HomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COILY_CACHE_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("GH_TOKEN", "home-fallback")
	_ = ghidcache.Invalidate()
	if _, err := ghidcache.APIUser(func() ([]byte, error) {
		return []byte(`{"login":"x"}`), nil
	}); err != nil {
		t.Fatalf("APIUser: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".coily", "cache", "gh-api-user")); err != nil {
		t.Fatalf("home-fallback cache dir not created: %v", err)
	}
}

func TestAPIUser_HostScopesKey(t *testing.T) {
	resetCacheDir(t)
	_ = ghidcache.Invalidate()
	t.Setenv("GH_TOKEN", "same-token")

	t.Setenv("GH_HOST", "github.com")
	if _, err := ghidcache.APIUser(func() ([]byte, error) {
		return []byte(`{"login":"public"}`), nil
	}); err != nil {
		t.Fatalf("public: %v", err)
	}

	// Same token, different host. Must not alias.
	t.Setenv("GH_HOST", "ghe.example.com")
	calls := 0
	got, err := ghidcache.APIUser(func() ([]byte, error) {
		calls++
		return []byte(`{"login":"ghe"}`), nil
	})
	if err != nil {
		t.Fatalf("ghe: %v", err)
	}
	if string(got) != `{"login":"ghe"}` || calls != 1 {
		t.Errorf("host-scoping leak: got=%q calls=%d, want ghe / 1", got, calls)
	}
}
