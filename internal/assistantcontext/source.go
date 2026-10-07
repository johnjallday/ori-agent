package assistantcontext

import "time"

// Source kinds a turn can read through Ori's brokered readers.
const (
	SourceNote = "note"
	SourceTask = "task"
)

// Coverage says how much of a source reached the model. A listing or a title
// is never coverage: only delivered content is recorded as a source.
const (
	CoverageFull    = "full"
	CoveragePartial = "partial"
)

// SourceLimit bounds the references kept with one turn. They are references
// and versions only; the body that was read is never stored with them.
const SourceLimit = 12

// SourceRef is one source whose content Ori delivered to the model in a turn.
// The key is issued by the server for that turn. Saved with a turn it is
// history: what that reply read then, never permission to read it again.
type SourceRef struct {
	Key         string    `json:"key"`
	Kind        string    `json:"kind"`
	WorkspaceID string    `json:"workspace_id"`
	Workspace   string    `json:"workspace"`
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Version     string    `json:"version,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
	ReadAt      time.Time `json:"read_at"`
	Coverage    string    `json:"coverage"`
	// Start, End and Total are character positions of the part that was read.
	Start int `json:"start,omitempty"`
	End   int `json:"end,omitempty"`
	Total int `json:"total,omitempty"`
	// Href is a server-authored in-app page for the source, never a model URL
	// or a filesystem path.
	Href string `json:"href,omitempty"`
	// Cited reports that the reply referred to this source by its key.
	Cited bool `json:"cited,omitempty"`
}
