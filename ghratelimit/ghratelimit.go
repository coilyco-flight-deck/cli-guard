// Package ghratelimit retries gh-CLI calls that fail with a GitHub
// rate-limit error. v1 of cli-guard#66: pattern-match the failure mode
// in gh's exit error (stderr text and HTTP status), back off, retry.
// Header-aware budget tracking is deferred to a follow-up since `gh api`
// does not surface response headers without `--include`, which then
// poisons JSON parsing for every caller.
//
// Retry policy:
//
//   - Primary rate limit (403 with "API rate limit exceeded"): exponential
//     backoff 2s -> 8s -> 32s, capped at 60s, up to 3 retries.
//   - Secondary / abuse-detection limit (403 with "secondary rate limit"
//     or "abuse detection"): same backoff. These are usually short.
//   - HTTP 429: same backoff.
//   - Any other error: returned immediately, no retry.
//
// Failure mode: if all retries fail, the original error is returned with
// retry context wrapped around it. The caller can pair this with ghcache
// to fall through to a stale entry rather than hard-fail.
//
// Sleep is via the package-level Sleeper var so tests can swap it for a
// no-op. Real callers do not touch it.
package ghratelimit

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Sleeper is the function used between retries. Swappable in tests so
// the retry loop runs at wall-clock zero. Real callers leave this alone.
var Sleeper = time.Sleep

// MaxRetries is the cap on retry attempts after the initial call. Three
// retries plus the initial call = up to four total attempts.
const MaxRetries = 3

// backoffSchedule is the per-attempt sleep before the *next* call. Index
// 0 is the wait before retry #1 (i.e. after the initial call failed),
// index 1 before retry #2, etc.
var backoffSchedule = []time.Duration{
	2 * time.Second,
	8 * time.Second,
	32 * time.Second,
}

// Retry runs fetch up to MaxRetries+1 times, backing off between
// attempts on recognized rate-limit failures. fetch's first return is
// passed through on success.
//
// On final failure, Retry returns an error that wraps the most recent
// fetch error and contains the attempt count in its message. The wrapped
// error is reachable via errors.Unwrap so callers can still inspect it.
func Retry(fetch func() ([]byte, error)) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		v, err := fetch()
		if err == nil {
			recordSuccess()
			return v, nil
		}
		lastErr = err
		if !isRateLimit(err) {
			return nil, err
		}
		recordFailure(err)
		if attempt == MaxRetries {
			break
		}
		Sleeper(backoffFor(attempt))
	}
	return nil, fmt.Errorf("ghratelimit: gave up after %d retries: %w", MaxRetries, lastErr)
}

// backoffFor returns the sleep before retry attempt+1. Cap matches the
// issue spec (60s) so a single secondary-limit cooldown can't stall the
// caller for minutes.
func backoffFor(attempt int) time.Duration {
	const maxBackoff = 60 * time.Second
	if attempt < 0 || attempt >= len(backoffSchedule) {
		return maxBackoff
	}
	d := backoffSchedule[attempt]
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// isRateLimit returns true when err looks like a GitHub rate-limit
// failure. Pattern-matches both the gh CLI's exec-style errors (stderr
// text bubbled up via cli-guard's stderr-tail capture) and a few common
// HTTP-status fragments.
//
// The set is deliberately narrow. A false positive here means retrying a
// non-retryable error and burning a few seconds; a false negative means
// surfacing a rate-limit error to the user without retrying. We bias
// toward false negative since the retry loop also costs latency on the
// non-rate-limit path.
func isRateLimit(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"api rate limit exceeded",
		"secondary rate limit",
		"abuse detection",
		"http 429",
		"status: 429",
		// gh CLI emits this exact phrase on primary limits.
		"you have exceeded a secondary rate limit",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// failureState is the package's tiny in-process record of the most
// recent rate-limit failure. ghcache reads LastFailure() to decide
// whether to return a stale entry instead of propagating a hard error.
// In-process is enough today (a single coily invocation), but the type
// is exported so a future on-disk variant can swap in without API
// churn.
type failureState struct {
	mu      sync.Mutex
	lastErr error
	lastAt  time.Time
}

var state failureState

func recordFailure(err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.lastErr = err
	state.lastAt = time.Now()
}

func recordSuccess() {
	state.mu.Lock()
	defer state.mu.Unlock()
	// Don't clear lastErr - a fresh success doesn't mean past failures
	// didn't happen. LastFailure callers gate on the timestamp.
}

// LastFailure returns the most recent rate-limit error observed in this
// process and the time it occurred. (time-zero, nil) when no failure has
// been recorded. Exposed for ghcache so it can fall through to a stale
// entry on the next call after a rate-limit hit.
func LastFailure() (time.Time, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.lastAt, state.lastErr
}

// Reset clears the failure state. Test-only; real callers leave the
// state alone so LastFailure carries across calls within a process.
func Reset() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.lastErr = nil
	state.lastAt = time.Time{}
}

// ErrGaveUp is returned (wrapped) when retries are exhausted. Exposed
// so callers can errors.Is against it without string-matching.
var ErrGaveUp = errors.New("ghratelimit: retries exhausted")
