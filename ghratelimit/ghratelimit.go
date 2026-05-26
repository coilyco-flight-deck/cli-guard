// Package ghratelimit retries gh-CLI calls that fail with a GitHub
// rate-limit error. v1 of cli-guard#66: pattern-match the failure mode
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
var backoffSchedule = []time.Duration{
	2 * time.Second,
	8 * time.Second,
	32 * time.Second,
}

// Retry runs fetch up to MaxRetries+1 times, backing off between
// attempts on recognized rate-limit failures. fetch's first return is
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
