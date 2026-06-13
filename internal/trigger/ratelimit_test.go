package trigger

import "testing"

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	rl := newRateLimiter(60)
	allowed := 0
	for i := 0; i < 100; i++ {
		if rl.allow("trg-1") {
			allowed++
		}
	}
	// A fresh bucket holds a full minute's worth (60); the rest are blocked
	// until the bucket refills.
	if allowed != 60 {
		t.Errorf("allowed = %d, want 60 (full burst)", allowed)
	}
}

func TestRateLimiterIsolatesKeys(t *testing.T) {
	rl := newRateLimiter(2)
	if !rl.allow("a") || !rl.allow("a") {
		t.Fatal("first two for key a should pass")
	}
	if rl.allow("a") {
		t.Error("third for key a should block")
	}
	if !rl.allow("b") {
		t.Error("key b has its own bucket and should pass")
	}
}

func TestRateLimiterForget(t *testing.T) {
	rl := newRateLimiter(1)
	if !rl.allow("a") {
		t.Fatal("first should pass")
	}
	if rl.allow("a") {
		t.Fatal("second should block")
	}
	rl.forget("a")
	if !rl.allow("a") {
		t.Error("after forget, bucket resets and should pass")
	}
}

func TestStatusForIngest(t *testing.T) {
	cases := map[IngestResult]int{
		IngestAccepted:     202,
		IngestNotFound:     404,
		IngestUnauthorized: 401,
		IngestRateLimited:  429,
		IngestWrongType:    415,
	}
	for res, want := range cases {
		if got := StatusForIngest(res); got != want {
			t.Errorf("StatusForIngest(%d) = %d, want %d", res, got, want)
		}
	}
}

func TestAcceptedContentType(t *testing.T) {
	ok := []string{"application/json", "application/json; charset=utf-8", "text/plain", "application/x-www-form-urlencoded", ""}
	for _, ct := range ok {
		if !AcceptedContentType(ct) {
			t.Errorf("AcceptedContentType(%q) = false, want true", ct)
		}
	}
	bad := []string{"application/octet-stream", "image/png", "application/pdf"}
	for _, ct := range bad {
		if AcceptedContentType(ct) {
			t.Errorf("AcceptedContentType(%q) = true, want false", ct)
		}
	}
}

func TestWebhookEventTruncates(t *testing.T) {
	big := make([]byte, MaxPayloadBytes+100)
	ev := WebhookEventFromRequest("application/json", "1.2.3.4", string(big))
	if !ev.Truncated || len(ev.Body) != MaxPayloadBytes {
		t.Errorf("oversized body not truncated: truncated=%v len=%d", ev.Truncated, len(ev.Body))
	}
}
