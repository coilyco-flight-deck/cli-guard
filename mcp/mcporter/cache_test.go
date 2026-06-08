package mcporter_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/mcp/mcporter"
)

func TestTTLCache_HitWithinTTL(t *testing.T) {
	var calls int32
	inner := mcporter.ResolverFunc(func(name string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "v:" + name, nil
	})
	path := filepath.Join(t.TempDir(), "ssm.json")
	c := mcporter.WithTTLCache(inner, time.Minute, path)

	for i := 0; i < 3; i++ {
		v, err := c.Resolve("A")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if v != "v:A" {
			t.Errorf("Resolve = %q, want v:A", v)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("inner calls = %d, want 1 (cache hit)", got)
	}
}

func TestTTLCache_MissAfterTTL(t *testing.T) {
	var calls int32
	inner := mcporter.ResolverFunc(func(name string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "v:" + name, nil
	})
	path := filepath.Join(t.TempDir(), "ssm.json")
	c := mcporter.WithTTLCache(inner, time.Nanosecond, path)

	if _, err := c.Resolve("A"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	time.Sleep(2 * time.Second) // entries are unix-second granularity
	if _, err := c.Resolve("A"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("inner calls = %d, want 2 (refetch after TTL)", got)
	}
}

func TestTTLCache_PersistsAcrossInstances(t *testing.T) {
	var calls int32
	inner := mcporter.ResolverFunc(func(name string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "v:" + name, nil
	})
	path := filepath.Join(t.TempDir(), "ssm.json")
	c1 := mcporter.WithTTLCache(inner, time.Minute, path)
	if _, err := c1.Resolve("A"); err != nil {
		t.Fatalf("Resolve c1: %v", err)
	}

	// Second instance, same persistPath. Should hit cache without
	// calling inner again.
	c2 := mcporter.WithTTLCache(inner, time.Minute, path)
	if _, err := c2.Resolve("A"); err != nil {
		t.Fatalf("Resolve c2: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("inner calls = %d, want 1 (persisted across instances)", got)
	}
}

func TestTTLCache_ResolverErrorNotCached(t *testing.T) {
	boom := errors.New("transient")
	var calls int32
	inner := mcporter.ResolverFunc(func(name string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return "", boom
		}
		return "v:" + name, nil
	})
	path := filepath.Join(t.TempDir(), "ssm.json")
	c := mcporter.WithTTLCache(inner, time.Minute, path)

	if _, err := c.Resolve("A"); !errors.Is(err, boom) {
		t.Fatalf("first Resolve err = %v, want %v", err, boom)
	}
	v, err := c.Resolve("A")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if v != "v:A" {
		t.Errorf("second Resolve = %q, want v:A", v)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("inner calls = %d, want 2 (error not cached)", got)
	}
}

func TestTTLCache_CorruptFileFallsThrough(t *testing.T) {
	var calls int32
	inner := mcporter.ResolverFunc(func(name string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "v:" + name, nil
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "ssm.json")
	if err := os.WriteFile(path, []byte("not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c := mcporter.WithTTLCache(inner, time.Minute, path)
	v, err := c.Resolve("A")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v != "v:A" {
		t.Errorf("Resolve = %q, want v:A", v)
	}
}
