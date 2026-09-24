package personalassistant

import (
	"errors"
	"time"
)

const (
	KnowledgeSchemaVersion = 1
	knowledgeFileName      = "personal-assistant-knowledge.json"
	knowledgeLockName      = "personal-assistant-knowledge.lock"
	knowledgeMaxBytes      = 256 * 1024
	knowledgeMaxItems      = 128
	knowledgeMaxTombstone  = 1024
	knowledgeMaxReceipts   = 128
	knowledgeMaxRevisions  = 12
	knowledgeMaxEvidence   = 16
)

var (
	ErrKnowledgeCorrupt = errors.New("personal assistant: knowledge metadata unavailable")
	ErrKnowledgeLimit   = errors.New("personal assistant: knowledge metadata limit reached")
)

type KnowledgeState string

const (
	KnowledgeCandidate   KnowledgeState = "candidate"
	KnowledgeApproved    KnowledgeState = "approved"
	KnowledgeNeedsReview KnowledgeState = "needs_review"
	KnowledgeRejected    KnowledgeState = "rejected"
	KnowledgeForgotten   KnowledgeState = "forgotten"
)

type KnowledgeOwner struct {
	UserID               string `json:"user_id"`
	AssistantID          string `json:"assistant_id"`
	HQWorkspaceID        string `json:"hq_workspace_id"`
	HQFolderSlug         string `json:"hq_folder_slug"`
	EntryAgentInstanceID string `json:"entry_agent_instance_id"`
}

func (binding KnowledgeBinding) owner() KnowledgeOwner {
	return KnowledgeOwner{
		UserID: binding.UserID, AssistantID: binding.AssistantID,
		HQWorkspaceID: binding.HQWorkspaceID, HQFolderSlug: binding.HQFolderSlug,
		EntryAgentInstanceID: binding.EntryAgentInstanceID,
	}
}

// KnowledgeEvidence contains inert bounded source references, never raw source
// payloads, filesystem paths, or a grant to reread a source.
type KnowledgeEvidence struct {
	SourceKind string    `json:"source_kind"`
	SourceID   string    `json:"source_id"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	Summary    string    `json:"summary,omitempty"`
}

type KnowledgeRevision struct {
	ID         string              `json:"id"`
	Text       string              `json:"text,omitempty"`
	ValueHash  string              `json:"value_hash,omitempty"`
	CreatedAt  time.Time           `json:"created_at,omitempty"`
	EditedAt   *time.Time          `json:"edited_at,omitempty"`
	ApprovedAt *time.Time          `json:"approved_at,omitempty"`
	Evidence   []KnowledgeEvidence `json:"evidence,omitempty"`
}

// KnowledgeTarget points to a canonical value. Its hash is a compare-and-swap
// guard, never a substitute for reading the current value from the canonical
// MEMORY.md or profile store.
type KnowledgeTarget struct {
	Kind          string `json:"kind"` // memory or profile
	Marker        string `json:"marker,omitempty"`
	Field         string `json:"field,omitempty"`
	CanonicalHash string `json:"canonical_hash"`
	Version       string `json:"version,omitempty"`
}

// KnowledgeOperation is a prepared cross-store operation. The canonical
// mutation and journal recovery that use it are implemented in task 1.5/1.6.
type KnowledgeOperation struct {
	RequestID  string    `json:"request_id"`
	Kind       string    `json:"kind"`
	OldHash    string    `json:"old_hash"`
	NewHash    string    `json:"new_hash"`
	RevisionID string    `json:"revision_id,omitempty"`
	PreparedAt time.Time `json:"prepared_at,omitempty"`
}

type KnowledgeItem struct {
	ID                string              `json:"id"`
	Version           int64               `json:"version"`
	State             KnowledgeState      `json:"state"`
	SemanticKey       string              `json:"semantic_key"`
	Aliases           []string            `json:"aliases,omitempty"`
	Category          string              `json:"category"`
	SourceKind        string              `json:"source_kind,omitempty"`
	CurrentRevisionID string              `json:"current_revision_id,omitempty"`
	Revisions         []KnowledgeRevision `json:"revisions,omitempty"`
	Target            *KnowledgeTarget    `json:"target,omitempty"`
	Prepared          *KnowledgeOperation `json:"prepared,omitempty"`
	CreatedAt         time.Time           `json:"created_at,omitempty"`
	UpdatedAt         time.Time           `json:"updated_at,omitempty"`
}

type KnowledgeTombstone struct {
	SemanticKey string    `json:"semantic_key"`
	ItemID      string    `json:"item_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type KnowledgeReceipt struct {
	RequestID  string    `json:"request_id"`
	ItemID     string    `json:"item_id"`
	Result     string    `json:"result"`
	RevisionID string    `json:"revision_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type KnowledgeInterviewStatus string

const (
	KnowledgeInterviewOffered   KnowledgeInterviewStatus = "offered"
	KnowledgeInterviewDeferred  KnowledgeInterviewStatus = "deferred"
	KnowledgeInterviewCompleted KnowledgeInterviewStatus = "completed"
)

// Interview metadata never contains answer drafts. Durable per-row receipts
// let a later explicit reviewed save report partial outcomes without replaying
// or silently reverting a previously committed canonical value.
type KnowledgeInterviewRowReceipt struct {
	RequestID         string    `json:"request_id"`
	RowID             string    `json:"row_id"`
	TextHash          string    `json:"text_hash"`
	CanonicalRef      string    `json:"canonical_ref,omitempty"`
	ProfileRevision   int64     `json:"profile_revision,omitempty"` // per-field SQL generation, never a copied value
	ProfileExpectedAt time.Time `json:"-"`                          // transient CAS proof for an already-identical canonical value
	Status            string    `json:"status"`
	SavedAt           time.Time `json:"saved_at,omitempty"`
}

// KnowledgeInterviewProfileWrite is an inert per-row cross-store prepare
// record. It contains hashes and versions only; profile SQL owns the value.
// The exact planned SQL version lets a restarted retry distinguish its prior
// write from a coincidentally matching outside edit.
type KnowledgeInterviewProfileWrite struct {
	RequestID       string    `json:"request_id"`
	RowID           string    `json:"row_id"`
	Field           string    `json:"field"`
	BeforeHash      string    `json:"before_hash"`
	AfterHash       string    `json:"after_hash"`
	BeforeUpdatedAt time.Time `json:"before_updated_at"`
	BeforeRevision  int64     `json:"before_revision"`
	WrittenAt       time.Time `json:"written_at"`
}

type KnowledgeInterview struct {
	Status              KnowledgeInterviewStatus        `json:"status"`
	OfferedAt           time.Time                       `json:"offered_at,omitempty"`
	UpdatedAt           time.Time                       `json:"updated_at,omitempty"`
	CompletedAt         time.Time                       `json:"completed_at,omitempty"`
	CompletionRequestID string                          `json:"completion_request_id,omitempty"`
	SelectionHash       string                          `json:"selection_hash,omitempty"`
	SaveRequestID       string                          `json:"save_request_id,omitempty"`
	ReviewHash          string                          `json:"review_hash,omitempty"`
	RowReceipts         []KnowledgeInterviewRowReceipt  `json:"row_receipts,omitempty"`
	ProfileWrite        *KnowledgeInterviewProfileWrite `json:"profile_write,omitempty"`
}

type KnowledgeDocument struct {
	SchemaVersion int                  `json:"schema_version"`
	Version       int64                `json:"version"`
	Owner         KnowledgeOwner       `json:"owner"`
	Items         []KnowledgeItem      `json:"items,omitempty"`
	Tombstones    []KnowledgeTombstone `json:"tombstones,omitempty"`
	Receipts      []KnowledgeReceipt   `json:"receipts,omitempty"`
	Admissions    []time.Time          `json:"admissions,omitempty"`
	Interview     *KnowledgeInterview  `json:"interview,omitempty"`
	// Present distinguishes a missing file from a valid, empty document.
	// A context reader must never treat missing metadata as approval.
	Present bool `json:"-"`
}
