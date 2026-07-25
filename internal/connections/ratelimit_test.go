package connections

import (
	"net/http"
	"testing"
	"time"
)

func TestIsRateLimited(t *testing.T) {
	if !IsRateLimited(http.StatusTooManyRequests) {
		t.Fatal("429 should be rate limited")
	}
	if IsRateLimited(http.StatusOK) || IsRateLimited(http.StatusForbidden) {
		t.Fatal("non-429 should not be rate limited")
	}
}

func TestBackoff_Exponential(t *testing.T) {
	p := BackoffPolicy{Base: time.Second, Max: time.Minute, Factor: 2}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{100, time.Minute}, // capped at Max
	}
	for _, tc := range cases {
		if got := p.Delay(tc.attempt, 0); got != tc.want {
			t.Fatalf("Delay(%d,0) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestBackoff_RetryAfterWins(t *testing.T) {
	p := DefaultBackoff()
	if got := p.Delay(0, 30*time.Second); got != 30*time.Second {
		t.Fatalf("Retry-After should win over base: %v", got)
	}
}

func TestBackoff_RetryAfterCeiling(t *testing.T) {
	p := DefaultBackoff()
	if got := p.Delay(0, 2*time.Hour); got != time.Hour {
		t.Fatalf("Retry-After should be capped at 1h ceiling: %v", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Now()

	if d, ok := ParseRetryAfter("120", now); !ok || d != 2*time.Minute {
		t.Fatalf("seconds form: d=%v ok=%v", d, ok)
	}
	if d, ok := ParseRetryAfter(now.Add(30*time.Second).UTC().Format(http.TimeFormat), now); !ok || d < 25*time.Second || d > 30*time.Second {
		t.Fatalf("http-date form: d=%v ok=%v", d, ok)
	}
	for _, bad := range []string{"", "   ", "soon", "-5"} {
		if _, ok := ParseRetryAfter(bad, now); ok {
			t.Fatalf("%q should not parse", bad)
		}
	}
}
