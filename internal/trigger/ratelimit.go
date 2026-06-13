package trigger

import (
	"sync"
	"time"
)

// DefaultWebhookRatePerMin is the per-trigger ceiling on accepted webhook
// requests per minute. Combined with coalescing this bounds worst-case LLM
// spend per trigger (PRD #25).
const DefaultWebhookRatePerMin = 60

// rateLimiter is a per-key token bucket. Keys are trigger IDs. The bucket
// refills continuously at ratePerMin/60 tokens per second up to a burst of
// ratePerMin, so a caller may spend a full minute's allowance at once but no
// more than ratePerMin within any rolling minute.
type rateLimiter struct {
	ratePerMin float64

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(ratePerMin int) *rateLimiter {
	if ratePerMin <= 0 {
		ratePerMin = DefaultWebhookRatePerMin
	}
	return &rateLimiter{
		ratePerMin: float64(ratePerMin),
		buckets:    make(map[string]*bucket),
	}
}

// allow consumes one token for key, returning false when the bucket is empty.
func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	b := r.buckets[key]
	if b == nil {
		// New bucket starts full so the first request always passes.
		r.buckets[key] = &bucket{tokens: r.ratePerMin - 1, last: now}
		return true
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * (r.ratePerMin / 60.0)
	if b.tokens > r.ratePerMin {
		b.tokens = r.ratePerMin
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// forget drops a key's bucket (called when a trigger is deleted) so the map
// doesn't grow without bound.
func (r *rateLimiter) forget(key string) {
	r.mu.Lock()
	delete(r.buckets, key)
	r.mu.Unlock()
}
