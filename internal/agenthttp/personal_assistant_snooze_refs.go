package agenthttp

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	todayViewTTL = 15 * time.Minute
	snoozeTTL    = 5 * time.Minute
	todayMaxLive = 64
)

// A live view and its review share the same owner. A view is issued only
// after a stored Today turn. Neither it nor a review lives in the transcript.
type snoozeOwner struct {
	user, assistant, hq, conversation, page string
	version                                 int64
}

type snoozeViewRecord struct {
	id        string
	expires   time.Time
	choices   []assistantTodayRow
	truncated bool
	sequence  uint64
	review    *snoozeReviewRecord
}

type snoozeReviewRecord struct {
	id, followUpID, title, owner, href, label, selection string
	revision, until, expires                             time.Time
	suggested                                            bool
}

// This is deliberately not durable. Restart or expiration requires a new
// review; it never means a missing receipt is safe to retry.
type todayReviewCache struct {
	mu    sync.Mutex
	now   func() time.Time
	views map[snoozeOwner]*snoozeViewRecord
}

func newTodayReviewCache() *todayReviewCache {
	return &todayReviewCache{now: time.Now, views: make(map[snoozeOwner]*snoozeViewRecord)}
}

func (c *todayReviewCache) issue(owner snoozeOwner, choices []assistantTodayRow, truncated bool) string {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var oldestOwner snoozeOwner
	var oldest time.Time
	for key, view := range c.views {
		if !view.expires.After(now) {
			delete(c.views, key)
		} else if oldest.IsZero() || view.expires.Before(oldest) {
			oldestOwner, oldest = key, view.expires
		}
	}
	if len(c.views) >= todayMaxLive && c.views[owner] == nil {
		delete(c.views, oldestOwner)
	}
	id := uuid.NewString()
	c.views[owner] = &snoozeViewRecord{id: id, expires: now.Add(todayViewTTL), choices: choices, truncated: truncated}
	return id
}

// beginReview retires the previous preview before resolving a new target or
// time. A slow response cannot supersede a newer review request.
func (c *todayReviewCache) beginReview(owner snoozeOwner, id string) (snoozeViewRecord, bool) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	view := c.views[owner]
	if id == "" || view == nil || view.id != id || !view.expires.After(now) {
		return snoozeViewRecord{}, false
	}
	view.sequence++
	view.review = nil
	return *view, true
}

func (c *todayReviewCache) finishReview(owner snoozeOwner, previous snoozeViewRecord, review snoozeReviewRecord) (snoozeReviewRecord, bool) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	view := c.views[owner]
	if view == nil || view.id != previous.id || view.sequence != previous.sequence || !view.expires.After(now) || !review.until.After(now) {
		return snoozeReviewRecord{}, false
	}
	review.id = uuid.NewString()
	review.expires = now.Add(snoozeTTL)
	if view.expires.Before(review.expires) {
		review.expires = view.expires
	}
	view.review = &review
	return review, true
}

// consume is one way. A duplicate request, a late cancel or an uncertain
// storage result cannot submit the same review a second time.
func (c *todayReviewCache) consume(owner snoozeOwner, id string) (snoozeReviewRecord, bool) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	view := c.views[owner]
	if id == "" || view == nil || view.review == nil || view.review.id != id || !view.review.expires.After(now) {
		return snoozeReviewRecord{}, false
	}
	review := *view.review
	view.review = nil
	return review, true
}

func (c *todayReviewCache) forget(owner snoozeOwner) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.views, owner)
}
