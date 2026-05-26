package stscache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/coilysiren/cli-guard/stscache"
)

// resetCacheDir points stscache at a fresh tempdir and clears the AWS
// profile env vars so subtests do not alias each other through the
func resetCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("COILY_CACHE_DIR", dir)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	return dir
}

func TestCallerIdentity_FetchOnMiss(t *testing.T) {
	resetCacheDir(t)
	_ = stscache.Invalidate()
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{"Account":"111111111111"}`), nil
	}
	v, err := stscache.CallerIdentity(fetch)
	if err != nil {
		t.Fatalf("CallerIdentity: %v", err)
	}
	if string(v) != `{"Account":"111111111111"}` {
		t.Errorf("CallerIdentity = %q, want the fetched JSON", v)
	}
	v, err = stscache.CallerIdentity(fetch)
	if err != nil {
		t.Fatalf("CallerIdentity (cached): %v", err)
	}
	if string(v) != `{"Account":"111111111111"}` {
		t.Errorf("CallerIdentity (cached) = %q, want the fetched JSON", v)
	}
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 (second call should hit cache)", calls)
	}
}

func TestCallerIdentity_PerProfileKey(t *testing.T) {
	resetCacheDir(t)
	_ = stscache.Invalidate()

	t.Setenv("AWS_PROFILE", "alpha")
	_ = stscache.Invalidate()
	if _, err := stscache.CallerIdentity(func() ([]byte, error) {
		return []byte(`{"Account":"alpha-acct"}`), nil
	}); err != nil {
		t.Fatalf("alpha fetch: %v", err)
	}

	t.Setenv("AWS_PROFILE", "beta")
	_ = stscache.Invalidate()
	betaCalls := 0
	got, err := stscache.CallerIdentity(func() ([]byte, error) {
		betaCalls++
		return []byte(`{"Account":"beta-acct"}`), nil
	})
	if err != nil {
		t.Fatalf("beta fetch: %v", err)
	}
	if string(got) != `{"Account":"beta-acct"}` {
		t.Errorf("beta got = %q, want beta-acct (profile-swap leaked alpha's entry)", got)
	}
	if betaCalls != 1 {
		t.Errorf("beta fetch called %d times, want 1", betaCalls)
	}

	t.Setenv("AWS_PROFILE", "alpha")
	alphaCalls := 0
	got, err = stscache.CallerIdentity(func() ([]byte, error) {
		alphaCalls++
		return []byte(`{"Account":"alpha-refetch"}`), nil
	})
	if err != nil {
		t.Fatalf("alpha re-read: %v", err)
	}
	if string(got) != `{"Account":"alpha-acct"}` {
		t.Errorf("alpha re-read = %q, want the original alpha entry (cache should still be warm)", got)
	}
	if alphaCalls != 0 {
		t.Errorf("alpha fetch called %d times on second visit, want 0 (cache hit expected)", alphaCalls)
	}
}

func TestCallerIdentity_DefaultProfileFallback(t *testing.T) {
	dir := resetCacheDir(t)
	_ = stscache.Invalidate()
	t.Setenv("AWS_DEFAULT_PROFILE", "from-default")
	if _, err := stscache.CallerIdentity(func() ([]byte, error) {
		return []byte(`{"Account":"default-acct"}`), nil
	}); err != nil {
		t.Fatalf("CallerIdentity: %v", err)
	}
	// Confirm an entry landed under aws-sts-identity/.
	matches, err := filepath.Glob(filepath.Join(dir, "aws-sts-identity", "*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("Glob: matches=%v err=%v (want exactly one cache file)", matches, err)
	}
}

func TestCallerIdentity_FetchErrorPropagates(t *testing.T) {
	resetCacheDir(t)
	_ = stscache.Invalidate()
	wantErr := errors.New("sts unreachable")
	_, err := stscache.CallerIdentity(func() ([]byte, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("CallerIdentity error = %v, want errors.Is(_, wantErr)", err)
	}
}

func TestCallerIdentity_InvalidateForcesRefetch(t *testing.T) {
	resetCacheDir(t)
	_ = stscache.Invalidate()
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte(`{"Account":"x"}`), nil
	}
	if _, err := stscache.CallerIdentity(fetch); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := stscache.Invalidate(); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := stscache.CallerIdentity(fetch); err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls != 2 {
		t.Errorf("fetch called %d times, want 2 (Invalidate should force refetch)", calls)
	}
}

// TestCallerIdentity_HomeFallback exercises the $HOME-based default
// cache dir by unsetting COILY_CACHE_DIR and pointing HOME at a tempdir.
func TestCallerIdentity_HomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COILY_CACHE_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("AWS_PROFILE", "home-fallback-profile")
	_ = stscache.Invalidate()
	if _, err := stscache.CallerIdentity(func() ([]byte, error) {
		return []byte(`{"ok":true}`), nil
	}); err != nil {
		t.Fatalf("CallerIdentity: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".coily", "cache", "aws-sts-identity")); err != nil {
		t.Fatalf("home-fallback cache dir not created: %v", err)
	}
}
