package blueprintintake

import "time"

// LedgerEntry is the portable audit record for one applied proposal item. It
// records the stable item key, created record, and exact reviewed values used.
type LedgerEntry struct {
	Key       string     `json:"key"`
	Kind      string     `json:"kind"`
	RecordID  string     `json:"record_id"`
	Title     string     `json:"title"`
	DueAt     *time.Time `json:"due_at,omitempty"`
	AppliedAt time.Time  `json:"applied_at"`
}
