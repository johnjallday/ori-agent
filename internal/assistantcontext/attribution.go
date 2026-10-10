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
	UserID                       string
	WorkspaceID                  string
	AgentName                    string
	StateVersion                 int64
	ContextWorkspaceIDs          []string
	ExpectedConversationRevision string
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
	// Sources are the sources whose content that turn delivered to the model.
	Sources  []SourceRef   `json:"sources,omitempty"`
	Research []ResearchRef `json:"research,omitempty"`
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
	// Scope always fits. Source references are dropped from the end, never cut
	// mid-record, so what is saved stays valid and says less rather than more.
	bounded := *value
	if len(bounded.Sources) > SourceLimit {
		bounded.Sources = bounded.Sources[:SourceLimit]
	}
	bounded.Research = nil
	for _, ref := range value.Research {
		if validResearchRef(ref) && len(bounded.Sources)+len(bounded.Research) < SourceLimit {
			bounded.Research = append(bounded.Research, ref)
		}
	}
	for {
		data, err := json.Marshal(bounded)
		if err != nil {
			return "", errors.New("invalid turn attribution")
		}
		if utf8.RuneCount(data) <= AttributionLimit {
			return string(data), nil
		}
		if len(bounded.Research) > 0 {
			bounded.Research = bounded.Research[:len(bounded.Research)-1]
		} else if len(bounded.Sources) > 0 {
			bounded.Sources = bounded.Sources[:len(bounded.Sources)-1]
		} else {
			return "", errors.New("invalid turn attribution size")
		}
	}
}

// WithoutSources is the scope alone, for places that restate where an earlier
// turn was asked but must not restate what it read.
func (a *Attribution) WithoutSources() *Attribution {
	if a == nil || len(a.Sources)+len(a.Research) == 0 {
		return a
	}
	scope := *a
	scope.Sources, scope.Research = nil, nil
	return &scope
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
	refs := []ResearchRef{}
	for _, ref := range value.Research {
		if validResearchRef(ref) && len(refs) < SourceLimit {
			refs = append(refs, ref)
		}
	}
	value.Research = refs
	return &value
}
