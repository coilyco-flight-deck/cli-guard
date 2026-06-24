// App-dir namespacing. The consumer-supplied directory name (e.g. ward
// ".ward") roots every per-user path; cli-guard hardcodes no consumer.
package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// FallbackAppDir is the app dir used when the consumer never calls SetAppDir.
// It is the framework's own name, so an unconfigured deployment owns nobody.
const FallbackAppDir = ".cli-guard"

// CacheSubdir is the subdirectory under the app dir holding the ttl caches.
const CacheSubdir = "cache"

// appDir is the consumer-supplied directory name, guarded because cache
// helpers resolve it lazily and may race a consumer setting it during init.
var (
	appDirMu sync.RWMutex
	appDir   string
)

// envSanitizer collapses runs of non-[A-Z0-9] into a single underscore so a
// base name maps to a legal shell env var token.
var envSanitizer = regexp.MustCompile(`[^A-Z0-9]+`)

// SetAppDir registers the directory name rooting every per-user path. The
// consumer calls it once at startup - e.g. ward ".ward".
func SetAppDir(name string) {
	appDirMu.Lock()
	defer appDirMu.Unlock()
	appDir = name
}

// AppDir returns the value the consumer set, or FallbackAppDir when unset.
// Always begins with a dot so it lands hidden under $HOME and the repo root.
func AppDir() string {
	appDirMu.RLock()
	defer appDirMu.RUnlock()
	if appDir == "" {
		return FallbackAppDir
	}
	return appDir
}

// BaseName returns the app dir with leading dots stripped, e.g. ".ward" ->
// "ward", for the non-hidden cache env var and dispatch queue dir.
func BaseName() string {
	return strings.TrimLeft(AppDir(), ".")
}

// EnvName derives a consumer-scoped env var from the app dir base (".ward" +
// "_CACHE_DIR" -> "WARD_CACHE_DIR"); unset falls back to the "CLI_GUARD" token.
func EnvName(suffix string) string {
	base := envSanitizer.ReplaceAllString(strings.ToUpper(BaseName()), "_")
	base = strings.Trim(base, "_")
	if base == "" {
		base = "CLI_GUARD"
	}
	return base + suffix
}

// CacheDirEnv derives the cache-dir override env var from the app dir so no
// consumer name is baked in: ".ward" -> "WARD_CACHE_DIR".
func CacheDirEnv() string {
	return EnvName("_CACHE_DIR")
}

// CacheDir resolves the ttl-cache root: the <BASE>_CACHE_DIR env override,
// else ~/<app-dir>/cache, else $TMPDIR/<app-dir>/cache when $HOME is unset.
func CacheDir() string {
	if dir := os.Getenv(CacheDirEnv()); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, AppDir(), CacheSubdir)
}
