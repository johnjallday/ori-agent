// Package assistantcontext contains data-only historical/display projections.
// A reference in this package is never a source grant or CLI execution scope.
package assistantcontext

import "time"

const (
	Version        = 1
	OverviewLimit  = 12000
	PreviewLimit   = 5
	EvidenceLimit  = 64000
	FileChunkLimit = 40000
)

type Availability string

const (
	Available   Availability = "available"
	Empty       Availability = "empty"
	Unavailable Availability = "unavailable"
	Denied      Availability = "denied"
	Unsupported Availability = "unsupported"
	Partial     Availability = "partial"
	Historical  Availability = "historical"
)

type SourceStatus struct {
	Status      Availability `json:"status"`
	Reason      string       `json:"reason,omitempty"`
	Count       int          `json:"count,omitempty"`
	ContentRead bool         `json:"content_read"`
}

type WorkspaceRef struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug,omitempty"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskPreview struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Status    string    `json:"status"`
	Assignee  string    `json:"assignee,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NotePreview is a note's identity only. A title is not the note's content.
type NotePreview struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentPreview struct {
	Name   string `json:"name"`
	Role   string `json:"role,omitempty"`
	Status string `json:"status"`
}

// ProgramLink is an exact reciprocal record, distinct from physical grouping.
// It describes membership only, not inherited source/runtime permissions.
type ProgramLink struct {
	Home      WorkspaceRef `json:"home"`
	LinkID    string       `json:"link_id"`
	Revision  int64        `json:"revision"`
	ProgramID string       `json:"program_id"`
}

type Overview struct {
	Workspace    WorkspaceRef            `json:"workspace"`
	Description  string                  `json:"description,omitempty"`
	Goal         string                  `json:"goal,omitempty"`
	Parent       *WorkspaceRef           `json:"parent,omitempty"`
	ProgramLink  *ProgramLink            `json:"program_link,omitempty"`
	Children     []WorkspaceRef          `json:"children,omitempty"`
	Agents       []AgentPreview          `json:"agents,omitempty"`
	Tasks        []TaskPreview           `json:"tasks,omitempty"`
	Notes        []NotePreview           `json:"notes,omitempty"`
	SelectedTask *TaskPreview            `json:"selected_task,omitempty"`
	Sources      map[string]SourceStatus `json:"sources"`
	Truncated    bool                    `json:"truncated"`
}

// Turn separates location from subject. Conversation owner and review action
// destination are resolved by their own canonical stores, not inferred here.
// No body, private path, credential, agent principal or executable grant belongs
// in this projection. Old/imported copies remain historical data.
type Turn struct {
	Version         int            `json:"version"`
	Status          Availability   `json:"status"`
	Reason          string         `json:"reason,omitempty"`
	Location        *WorkspaceRef  `json:"location,omitempty"`
	Subject         *WorkspaceRef  `json:"subject,omitempty"`
	SubjectExplicit bool           `json:"subject_explicit"`
	Overview        *Overview      `json:"overview,omitempty"`
	ReadAt          time.Time      `json:"read_at"`
	Choices         []WorkspaceRef `json:"choices,omitempty"`
	Discovery       SourceStatus   `json:"discovery"`
	ProjectCount    int            `json:"project_count"`
	GroupCount      int            `json:"group_count"`
	Projects        []WorkspaceRef `json:"projects,omitempty"`
	Groups          []WorkspaceRef `json:"groups,omitempty"`
}
