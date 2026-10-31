package session

import (
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// MarshalJSON adds the read-only shape projection to both list and detail
// responses. No SQLite column or client-supplied shape participates in writes.
func (w Workspace) MarshalJSON() ([]byte, error) {
	type payload Workspace
	return json.Marshal(struct {
		payload
		Shape string `json:"shape"`
	}{payload: payload(w), Shape: w.Shape()})
}

// Shape uses the bounded SQLite list projection only when this is a summary
// without SharedData. Hydrated records always derive it from canonical data.
func (w Workspace) Shape() string {
	if w.SharedData == nil && w.SummaryShape != "" {
		return w.SummaryShape
	}
	return workspace.ShapeFromSharedData(w.SharedData)
}
