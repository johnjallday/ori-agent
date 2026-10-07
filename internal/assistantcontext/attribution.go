package assistantcontext

import (
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

const AttributionLimit = 8000

// SaveOwner supplies expected canonical identity for an atomic save CAS. It
// is constructed only by the server and is neither serialized nor a grant.
type SaveOwner struct {
	UserID              string
	WorkspaceID         string
	AgentName           string
	StateVersion        int64
	ContextWorkspaceIDs []string
}

// Attribution retains references/versions only, never an overview or source
// body. Its historical representation is display data, not a read/action grant.
type Attribution struct {
	Version  int           `json:"version"`
	Status   Availability  `json:"status"`
	Reason   string        `json:"reason,omitempty"`
	Location *WorkspaceRef `json:"location,omitempty"`
	Subject  *WorkspaceRef `json:"subject,omitempty"`
	// SubjectExplicit records that the user named the subject in that turn; it
	// did not merely follow the page. Like every field here it is history.
	SubjectExplicit bool          `json:"subject_explicit,omitempty"`
	Parent          *WorkspaceRef `json:"parent,omitempty"`
	SelectedTaskID  string        `json:"selected_task_id,omitempty"`
	ReadAt          time.Time     `json:"read_at"`
	Historical      bool          `json:"historical,omitempty"`
}

func (t Turn) Attribution() *Attribution {
	out := &Attribution{Version: t.Version, Status: t.Status, Reason: t.Reason, Location: t.Location, Subject: t.Subject, SubjectExplicit: t.SubjectExplicit && t.Subject != nil, ReadAt: t.ReadAt}
	if t.Overview != nil {
		out.Parent = t.Overview.Parent
		if t.Overview.SelectedTask != nil {
			out.SelectedTaskID = t.Overview.SelectedTask.ID
		}
	}
	return out
}
func EncodeAttribution(value *Attribution) (string, error) {
	if value == nil {
		return "", nil
	}
	if value.Version != Version {
		return "", errors.New("invalid turn attribution version")
	}
	data, err := json.Marshal(value)
	if err != nil || utf8.RuneCount(data) > AttributionLimit {
		return "", errors.New("invalid turn attribution size")
	}
	return string(data), nil
}
func DecodeAttribution(data string) *Attribution {
	if data == "" || utf8.RuneCountInString(data) > AttributionLimit {
		return nil
	}
	var value Attribution
	if json.Unmarshal([]byte(data), &value) != nil || value.Version != Version {
		return nil
	}
	value.Historical = true
	return &value
}
