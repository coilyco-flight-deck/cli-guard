package ghratelimit_test

import (
	"errors"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/ghratelimit"
)

// swallow replaces the real Sleeper with a no-op for the duration of a
// test. Without this the retry loop would burn ~42s of real time.
func swallow(t *testing.T) {
	t.Helper()
	orig := ghratelimit.Sleeper
	ghratelimit.Sleeper = func(time.Duration) {}
	ghratelimit.Reset()
	t.Cleanup(func() {
		ghratelimit.Sleeper = orig
		ghratelimit.Reset()
	})
}

func TestRetry_SuccessOnFirstCall(t *testing.T) {
	swallow(t)
	calls := 0
	v, err := ghratelimit.Retry(func() ([]byte, error) {
		calls++
		return []byte("ok"), nil
	})
	if err != nil || string(v) != "ok" || calls != 1 {
		t.Errorf("first-call success: v=%q err=%v calls=%d", v, err, calls)
	}
}

func TestRetry_NonRateLimitErrorReturnsImmediately(t *testing.T) {
	swallow(t)
	wantErr := errors.New("disk full")
	calls := 0
	_, err := ghratelimit.Retry(func() ([]byte, error) {
		calls++
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("want wrapped wantErr, got %v", err)
	}
	if calls != 1 {
		t.Errorf("non-rate-limit should not retry: calls=%d", calls)
	}
}

func TestRetry_PrimaryRateLimitRetriesAndSucceeds(t *testing.T) {
	swallow(t)
	calls := 0
	v, err := ghratelimit.Retry(func() ([]byte, error) {
		calls++
		if calls < 2 {
			return nil, errors.New("HTTP 403: API rate limit exceeded for user ID 1")
		}
		return []byte("recovered"), nil
	})
	if err != nil || string(v) != "recovered" {
		t.Errorf("recover: v=%q err=%v", v, err)
	}
	if calls != 2 {
		t.Errorf("calls=%d, want 2", calls)
	}
}

func TestRetry_SecondaryRateLimitRetries(t *testing.T) {
	swallow(t)
	calls := 0
	_, err := ghratelimit.Retry(func() ([]byte, error) {
		calls++
		return nil, errors.New("you have exceeded a secondary rate limit, please wait a few minutes")
	})
	if err == nil {
		t.Fatal("want exhausted-retries error, got nil")
	}
	if calls != ghratelimit.MaxRetries+1 {
		t.Errorf("calls=%d, want %d (MaxRetries+1)", calls, ghratelimit.MaxRetries+1)
	}
}

func TestRetry_Http429TriggersRetry(t *testing.T) {
	swallow(t)
	calls := 0
	v, err := ghratelimit.Retry(func() ([]byte, error) {
		calls++
		if calls < 2 {
			return nil, errors.New("status: 429 Too Many Requests")
		}
		return []byte("ok"), nil
	})
	if err != nil || string(v) != "ok" || calls != 2 {
		t.Errorf("429 retry: v=%q err=%v calls=%d", v, err, calls)
	}
}

func TestRetry_ExhaustedRetriesWrapsLastErr(t *testing.T) {
	swallow(t)
	final := errors.New("API rate limit exceeded; reset at 1234")
	_, err := ghratelimit.Retry(func() ([]byte, error) {
		return nil, final
	})
	if !errors.Is(err, final) {
		t.Errorf("err = %v, want wrapped final", err)
	}
}

func TestLastFailure_RecordedOnRateLimit(t *testing.T) {
	swallow(t)
	rateLimitErr := errors.New("API rate limit exceeded")
	_, _ = ghratelimit.Retry(func() ([]byte, error) {
		return nil, rateLimitErr
	})
	at, got := ghratelimit.LastFailure()
	if got == nil {
		t.Fatal("LastFailure: nil, want recorded rate-limit error")
	}
	if at.IsZero() {
		t.Error("LastFailure timestamp is zero")
	}
}

func TestLastFailure_NotRecordedOnNonRateLimit(t *testing.T) {
	swallow(t)
	_, _ = ghratelimit.Retry(func() ([]byte, error) {
		return nil, errors.New("ENOENT")
	})
	_, got := ghratelimit.LastFailure()
	if got != nil {
		t.Errorf("LastFailure = %v on non-rate-limit error, want nil", got)
	}
}

func TestLastFailure_NotClearedOnLaterSuccess(t *testing.T) {
	// A success after a rate-limit doesn't mean the limit is gone -
	// the failure window matters to ghcache's stale-read logic.
	swallow(t)
	rateLimitErr := errors.New("secondary rate limit")
	_, _ = ghratelimit.Retry(func() ([]byte, error) {
		return nil, rateLimitErr
	})
	_, _ = ghratelimit.Retry(func() ([]byte, error) {
		return []byte("ok"), nil
	})
	_, got := ghratelimit.LastFailure()
	if got == nil {
		t.Error("LastFailure cleared after success, want preserved")
	}
}
