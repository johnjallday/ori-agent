package resourcecatalog

import "time"

// DocumentSchemaVersion is the schema of the catalog files and the grants.
const DocumentSchemaVersion = 1

// Bounds of one inspection and of the catalog (contract §7.2).
const (
	PageLimit                = 256
	MaxPagesPerSource        = 80
	MaxEntriesPerSource      = 20000
	MaxEntriesPerCatalog     = 40000
	InspectionDeadline       = 10 * time.Minute
	ReviewTTL                = 10 * time.Minute
	maxUnannotatedTombstones = 5000
	maxOperationsKept        = 20
)

// Source outcomes recorded for the last attempt at reading one source.
const (
	OutcomeComplete    = "complete"
	OutcomePartial     = "partial"
	OutcomeFailed      = "failed"
	OutcomeCancelled   = "cancelled"
	OutcomeAbsent      = "absent"
	OutcomeDenied      = "denied"
	OutcomeUnsupported = "unsupported"
	OutcomeUnavailable = "unavailable"
	OutcomeWithdrawn   = "withdrawn"
)

// Entry states stored in the observations file. Access withdrawn and
// "another setup" are never stored: they are derived from the current grant
// every time something is read.
const (
	EntryPresent       = "present"
	EntryNoLongerFound = "no_longer_found"
)

// CatalogRef names the provider catalog a Studio's files were written under.
type CatalogRef struct {
	PluginID string `json:"plugin_id"`
	ID       string `json:"id"`
	Version  int    `json:"version"`
}

// Observations is the Studio's canonical record of what was seen. Only a
// publication writes it, and every publication raises Revision by one.
type Observations struct {
	SchemaVersion int                  `json:"schema_version"`
	Catalog       CatalogRef           `json:"catalog"`
	DeclaredBy    *Declaration         `json:"declared_by,omitempty"`
	Revision      int64                `json:"revision"`
	PublishedAt   *time.Time           `json:"published_at,omitempty"`
	Installations []InstallationRecord `json:"installations"`
	Sources       []SourceRecord       `json:"sources"`
	Entries       []EntryRecord        `json:"entries"`
}

// InstallationRecord is one application resource folder the Studio has seen.
type InstallationRecord struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Layout      string    `json:"layout"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// SourceRecord is one source's coverage. It never holds a location: those
// stay in this computer's grants.
type SourceRecord struct {
	InstallationID string     `json:"installation_id"`
	SourceID       string     `json:"source_id"`
	Category       string     `json:"category"`
	Label          string     `json:"label"`
	Access         string     `json:"access"`
	LastAttempt    *Attempt   `json:"last_attempt,omitempty"`
	LastCompleteAt *time.Time `json:"last_complete_at,omitempty"`
	// Cursor and ChainStartedAt carry an unfinished read to its next
	// continuation. A source is complete only when a chain reaches its end.
	Cursor         string     `json:"cursor,omitempty"`
	ChainStartedAt *time.Time `json:"chain_started_at,omitempty"`
}

// Attempt is how the last read of one source ended.
type Attempt struct {
	OperationID string    `json:"operation_id"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	Outcome     string    `json:"outcome"`
	Reason      string    `json:"reason,omitempty"`
	Skipped     []Skip    `json:"skipped,omitempty"`
	EntriesRead int       `json:"entries_read"`
}

// EntryRecord is one observed resource. Its ID binds installation, source
// and the plugin's native identity (EntryID).
type EntryRecord struct {
	EntryID          string       `json:"entry_id"`
	InstallationID   string       `json:"installation_id"`
	SourceID         string       `json:"source_id"`
	Category         string       `json:"category"`
	Kind             string       `json:"kind"`
	NativeID         string       `json:"native_id"`
	Name             string       `json:"name"`
	Facts            []Fact       `json:"facts,omitempty"`
	Description      *Description `json:"description,omitempty"`
	ModifiedAt       *time.Time   `json:"modified_at,omitempty"`
	FirstObservedAt  time.Time    `json:"first_observed_at"`
	LastObservedAt   time.Time    `json:"last_observed_at"`
	ObservedRevision int64        `json:"observed_revision"`
	State            string       `json:"state"`
	NoLongerFoundAt  *time.Time   `json:"no_longer_found_at,omitempty"`
}

// Annotations is the owner's own metadata, kept apart from observations so a
// refresh can never rewrite, drop or reassign it.
type Annotations struct {
	SchemaVersion int                   `json:"schema_version"`
	Revision      int64                 `json:"revision"`
	Annotations   map[string]Annotation `json:"annotations"`
}

// Annotation is one entry's favorite, tags and note.
type Annotation struct {
	Favorite  bool      `json:"favorite"`
	Tags      []string  `json:"tags"`
	Note      string    `json:"note"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Grants is this computer's approval to read a Studio's sources. It lives in
// Ori's data directory and never travels with the workspace, so a Studio
// opened on another computer has history but no access there.
type Grants struct {
	SchemaVersion     int           `json:"schema_version"`
	StudioWorkspaceID string        `json:"studio_workspace_id"`
	LinkID            string        `json:"link_id"`
	OwnerUserID       string        `json:"owner_user_id"`
	Provider          CatalogRef    `json:"provider"`
	InstallationID    string        `json:"installation_id"`
	ApprovalRevision  int64         `json:"approval_revision"`
	Sources           []GrantSource `json:"sources"`
}

// GrantSource is one approved source as it was disclosed for review.
type GrantSource struct {
	SourceID         string     `json:"source_id"`
	Category         string     `json:"category"`
	Label            string     `json:"label"`
	Location         string     `json:"location"`
	Access           string     `json:"access"`
	ApprovedAt       time.Time  `json:"approved_at"`
	ApprovedRevision int64      `json:"approved_revision"`
	WithdrawnAt      *time.Time `json:"withdrawn_at,omitempty"`
}

// Active reports whether the source may be read and shared now.
func (g GrantSource) Active() bool { return g.WithdrawnAt == nil }

// Operation modes and states.
const (
	ModeInspect  = "inspect"
	ModeRefresh  = "refresh"
	ModeContinue = "continue"

	OperationRunning     = "running"
	OperationPublished   = "published"
	OperationCancelled   = "cancelled"
	OperationFailed      = "failed"
	OperationSuperseded  = "superseded"
	OperationInterrupted = "interrupted"
)

// Operation is one inspection: which sources it reads and how each went.
type Operation struct {
	ID                string            `json:"id"`
	IdempotencyKey    string            `json:"idempotency_key"`
	Mode              string            `json:"mode"`
	State             string            `json:"state"`
	StartedAt         time.Time         `json:"started_at"`
	FinishedAt        *time.Time        `json:"finished_at,omitempty"`
	InstallationID    string            `json:"installation_id"`
	BaseRevision      int64             `json:"base_revision"`
	ApprovalRevision  int64             `json:"approval_revision"`
	PublishedRevision int64             `json:"published_revision,omitempty"`
	Sources           []OperationSource `json:"sources"`
}

// OperationSource is one source's progress inside an operation.
type OperationSource struct {
	SourceID  string `json:"source_id"`
	Category  string `json:"category"`
	State     string `json:"state"`
	Pages     int    `json:"pages"`
	Entries   int    `json:"entries"`
	Reason    string `json:"reason,omitempty"`
	Continued bool   `json:"continued,omitempty"`
	// ApprovedRevision is the source's approval this read relies on. A
	// withdrawal or re-approval changes it, and the read's results are dropped.
	ApprovedRevision int64 `json:"approved_revision"`
}

// Per-source progress states inside an operation; terminal ones are outcomes.
const (
	ProgressPending = "pending"
	ProgressReading = "reading"
)
