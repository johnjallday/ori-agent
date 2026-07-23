// Package meetingprep persists the durable link between a Calendar Ops event
// and the Calendar Ops note prepared for it (PRD FR42-48, task 6.1). A Link
// row identifies the meeting by workspace + binding + calendar + event (the
// same raw event id can otherwise collide across different calendars or
// bindings) and stores only the linked note id, the last normalized event
// fingerprint, and run status -- never the event body, which is always a
// live gateway read, never a cache.
package meetingprep

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Status is the lifecycle state of a meeting-prep run.
type Status string

const (
	StatusPending Status = "pending"
	StatusReady   Status = "ready"
	StatusFailed  Status = "failed"
)

// Key identifies a meeting uniquely for linking purposes.
type Key struct {
	WorkspaceID string
	BindingID   string
	CalendarID  string
	EventID     string
}

// Link is one durable event-to-note link row.
type Link struct {
	ID               string
	Key              Key
	NoteID           string
	EventFingerprint string
	Status           Status
	TaskID           string
	Error            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// FingerprintInput is the normalized event data that determines whether a
// meeting has materially changed since it was last prepared. Deliberately
// narrow: only the fields a prep brief actually grounds itself in.
type FingerprintInput struct {
	Title       string
	StartTime   string
	EndTime     string
	Location    string
	Description string
}

// Fingerprint deterministically hashes the normalized event fields this
// package is allowed to remember (never the full event body -- see the
// package doc). Comparing a freshly computed fingerprint against
// Link.EventFingerprint tells the caller whether the linked note may be
// stale relative to the live event.
func Fingerprint(in FingerprintInput) string {
	canonical := []string{
		strings.TrimSpace(in.Title),
		strings.TrimSpace(in.StartTime),
		strings.TrimSpace(in.EndTime),
		strings.TrimSpace(in.Location),
		strings.TrimSpace(in.Description),
	}
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
