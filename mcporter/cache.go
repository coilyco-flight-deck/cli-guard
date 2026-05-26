package mcporter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WithTTLCache wraps inner with an on-disk TTL cache persisted to a
// single JSON file at persistPath. Cache shape:
func WithTTLCache(inner SecretResolver, ttl time.Duration, persistPath string) SecretResolver {
	return &ttlCache{inner: inner, ttl: ttl, path: persistPath}
}

type ttlCache struct {
	inner SecretResolver
	ttl   time.Duration
	path  string
	mu    sync.Mutex
}

type ttlEntry struct {
	Value     string `json:"value"`
	FetchedAt int64  `json:"fetched_at"`
}

func (c *ttlCache) Resolve(name string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entries := c.load()
	if e, ok := entries[name]; ok {
		age := time.Since(time.Unix(e.FetchedAt, 0))
		if age < c.ttl {
			return e.Value, nil
		}
	}
	v, err := c.inner.Resolve(name)
	if err != nil {
		return "", err
	}
	entries[name] = ttlEntry{Value: v, FetchedAt: time.Now().Unix()}
	c.store(entries)
	return v, nil
}

// load returns the persisted cache map. A missing or corrupt file
// returns an empty map; both fall through to a fresh underlying resolve
func (c *ttlCache) load() map[string]ttlEntry {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return map[string]ttlEntry{}
	}
	out := map[string]ttlEntry{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]ttlEntry{}
	}
	return out
}

// store rewrites the full cache atomically (temp file + rename). I/O
// errors are swallowed; a failed store means the next call refetches,
func (c *ttlCache) store(entries map[string]ttlEntry) {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".ssm-*.json")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		_ = os.Remove(tmpName)
		return
	}
}
