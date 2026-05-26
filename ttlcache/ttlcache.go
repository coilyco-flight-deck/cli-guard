// Package ttlcache is a small on-disk key/value cache with per-entry TTLs.
// Built for coily after the lockdown inversion: every aws / kubectl / gh
package ttlcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Cache is a directory of TTL'd JSON entries on disk. One file per key,
// named by sha256(key) so disk layout stays bounded and predictable. The
type Cache struct {
	Dir string
}

// New returns a Cache rooted at dir. The directory is created lazily on
// first Set, with mode 0o700 so other local users cannot read cached
func New(dir string) *Cache {
	return &Cache{Dir: dir}
}

// entry is the on-disk shape. Keeping the TTL in the file (rather than in
// the cache instance) means a single cache directory can hold entries
type entry struct {
	Value      []byte    `json:"value"`
	StoredAt   time.Time `json:"stored_at"`
	TTLSeconds int       `json:"ttl_seconds"`
}

func (c *Cache) keyToPath(key string) string {
	h := sha256.Sum256([]byte(key))
	return filepath.Join(c.Dir, hex.EncodeToString(h[:])+".json")
}

// Get returns the cached value for key, or (nil, false) if there is no
// fresh entry. Any read / unmarshal / TTL failure is reported as a miss
func (c *Cache) Get(key string) ([]byte, bool) {
	data, err := os.ReadFile(c.keyToPath(key))
	if err != nil {
		return nil, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, false
	}
	if time.Since(e.StoredAt) > time.Duration(e.TTLSeconds)*time.Second {
		return nil, false
	}
	return e.Value, true
}

// GetMaxAge returns the cached value for key only if a fresh entry
// exists and its age is within max. Age is measured against the entry's
func (c *Cache) GetMaxAge(key string, maxAge time.Duration) ([]byte, bool) {
	if maxAge == 0 {
		return nil, false
	}
	data, err := os.ReadFile(c.keyToPath(key))
	if err != nil {
		return nil, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, false
	}
	age := time.Since(e.StoredAt)
	if age > time.Duration(e.TTLSeconds)*time.Second {
		return nil, false
	}
	if maxAge > 0 && age > maxAge {
		return nil, false
	}
	return e.Value, true
}

// Set writes value under key with the given TTL. Returns the underlying
// filesystem error if the write fails; callers can ignore it (the next
func (c *Cache) Set(key string, value []byte, ttl time.Duration) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(entry{
		Value:      value,
		StoredAt:   time.Now(),
		TTLSeconds: int(ttl.Seconds()),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(c.keyToPath(key), data, 0o600)
}

// GetOrSet returns the cached value for key, or calls fetch() and stores
// the result if there is no fresh entry. fetch is called at most once.
func (c *Cache) GetOrSet(key string, ttl time.Duration, fetch func() ([]byte, error)) ([]byte, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	v, err := fetch()
	if err != nil {
		return nil, err
	}
	_ = c.Set(key, v, ttl) // perf hint, not load-bearing
	return v, nil
}

// Invalidate removes the entry for key. Returns nil if the entry did not
// exist (idempotent), the filesystem error otherwise. Used by callers
func (c *Cache) Invalidate(key string) error {
	err := os.Remove(c.keyToPath(key))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
