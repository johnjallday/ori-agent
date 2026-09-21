package blueprintintake

import "time"

// LedgerEntry is the portable audit record for one applied proposal item. It
// records the stable item key, created record, and exact reviewed values used.
type LedgerEntry struct {
	Key         string     `json:"key"`
	Kind        string     `json:"kind"`
	RecordID    string     `json:"record_id"`
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	Text        string     `json:"text,omitempty"`
	MemoryType  string     `json:"memory_type,omitempty"`
	Body        string     `json:"body,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	Start       *time.Time `json:"start,omitempty"`
	End         *time.Time `json:"end,omitempty"`
	AllDay      bool       `json:"all_day,omitempty"`
	Location    string     `json:"location,omitempty"`
	AppliedAt   time.Time  `json:"applied_at"`
}
