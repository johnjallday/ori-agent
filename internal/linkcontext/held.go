package linkcontext

import (
	"strings"
	"sync"
	"time"
)

// MaxHeldPerUser bounds how many links one user's composer can hold at once.
// Holding another past it lets the least recently used one go.
const MaxHeldPerUser = 20

// HeldLinks keeps the links the assistant's composer attached, by an id the
// browser sends back with a turn, so a turn's link is the observation the
// server made rather than anything the browser claims. It lives in memory: a
// held link survives no restart, and one unused for HeldTTL is let go. Using
// one (Resolve) keeps it for another HeldTTL.
type HeldLinks struct {
	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	items map[string]heldLink
}

type heldLink struct {
	user        string
	observation Observation
	expires     time.Time
	used        time.Time
}

// NewHeldLinks returns an empty store with the package TTL.
func NewHeldLinks() *HeldLinks {
	return &HeldLinks{ttl: HeldTTL, now: time.Now, items: map[string]heldLink{}}
}

// WithClock fixes the store's clock for tests.
func (h *HeldLinks) WithClock(now func() time.Time) *HeldLinks {
	if now != nil {
		h.now = now
	}
	return h
}

// Hold keeps an observation for a user and returns the id to send back: the
// observation's own id.
func (h *HeldLinks) Hold(user string, observation Observation) string {
	user = strings.TrimSpace(user)
	if h == nil || user == "" || strings.TrimSpace(observation.ID) == "" {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	h.sweepLocked(now)
	held := 0
	oldestID, oldest := "", time.Time{}
	for id, item := range h.items {
		if item.user != user {
			continue
		}
		held++
		if oldestID == "" || item.used.Before(oldest) {
			oldestID, oldest = id, item.used
		}
	}
	if held >= MaxHeldPerUser && oldestID != "" {
		delete(h.items, oldestID)
	}
	h.items[observation.ID] = heldLink{user: user, observation: observation, expires: now.Add(h.ttl), used: now}
	return observation.ID
}

// Resolve returns the user's held observation for id and keeps it for another
// TTL. Another user's id, an expired one, or an unknown one is not found.
func (h *HeldLinks) Resolve(user, id string) (Observation, bool) {
	if h == nil {
		return Observation{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	item, ok := h.items[strings.TrimSpace(id)]
	if !ok || item.user != strings.TrimSpace(user) {
		return Observation{}, false
	}
	if !now.Before(item.expires) {
		delete(h.items, id)
		return Observation{}, false
	}
	item.expires, item.used = now.Add(h.ttl), now
	h.items[id] = item
	return item.observation, true
}

// Release lets a user's held link go. Releasing twice, or another user's id,
// does nothing.
func (h *HeldLinks) Release(user, id string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id = strings.TrimSpace(id)
	if item, ok := h.items[id]; ok && item.user == strings.TrimSpace(user) {
		delete(h.items, id)
	}
}

func (h *HeldLinks) sweepLocked(now time.Time) {
	for id, item := range h.items {
		if !now.Before(item.expires) {
			delete(h.items, id)
		}
	}
}
