// Package assistantsetup coordinates the user-approved File Janitor setup
// journey across existing domain owners. It owns sequencing and bounded
// receipts only; workspace, agent, folder, monitoring, and scan state remain
// authoritative in their existing packages.
package assistantsetup

import (
	"errors"
	"time"
)

const (
	SchemaVersion = 1
	CapabilityID  = "file-janitor"
	BlueprintID   = "file-janitor"
)

var (
	ErrNotFound             = errors.New("assistant setup: not found")
	ErrUnavailable          = errors.New("assistant setup: unavailable")
	ErrInvalid              = errors.New("assistant setup: invalid")
	ErrConflict             = errors.New("assistant setup: conflict")
	ErrStaleProposal        = errors.New("assistant setup: stale proposal")
	ErrStaleRun             = errors.New("assistant setup: stale run")
	ErrAssistantNotReady    = errors.New("assistant setup: assistant not ready")
	ErrAssistantRepair      = errors.New("assistant setup: assistant repair required")
	ErrAmbiguousTarget      = errors.New("assistant setup: ambiguous target")
	ErrUnsupportedTarget    = errors.New("assistant setup: unsupported target")
	ErrTeamConflict         = errors.New("assistant setup: team conflict")
	ErrReconcileRequired    = errors.New("assistant setup: reconciliation required")
	ErrNoLongerAvailable    = errors.New("assistant setup: no longer available")
	ErrAgentRootUnavailable = errors.New("assistant setup: agent root unavailable")
)

type TargetMode string

const (
	TargetCreate TargetMode = "create"
	TargetAdopt  TargetMode = "adopt"
	TargetChoose TargetMode = "choose"
)

type RunStatus string

const (
	RunActive            RunStatus = "active"
	RunDeferred          RunStatus = "deferred"
	RunFirstResult       RunStatus = "first_result"
	RunReconcileRequired RunStatus = "reconcile_required"
	RunInvalidated       RunStatus = "invalidated"
)

type Step string

const (
	StepWorkspace   Step = "workspace"
	StepFolder      Step = "folder"
	StepMonitoring  Step = "monitoring"
	StepInitialScan Step = "initial_scan"
	StepResult      Step = "result"
)

type OperationKind string

const (
	OperationWorkspace   OperationKind = "workspace"
	OperationFolderGrant OperationKind = "folder_grant"
	OperationMonitoring  OperationKind = "monitoring"
	OperationInitialScan OperationKind = "initial_scan"
	OperationReset       OperationKind = "reset"
)

type OperationStatus string

const (
	OperationAwaitingUser OperationStatus = "awaiting_user"
	OperationClaimed      OperationStatus = "claimed"
	OperationRunning      OperationStatus = "running"
	OperationSucceeded    OperationStatus = "succeeded"
	OperationFailed       OperationStatus = "failed"
	OperationUnresolved   OperationStatus = "unresolved"
)

type ResourceOwnership string

const (
	OwnershipAdopted ResourceOwnership = "adopted"
	OwnershipCreated ResourceOwnership = "created"
	OwnershipUpdated ResourceOwnership = "updated"
)

const (
	ResourceWorkspace     = "workspace"
	ResourceAgentInstance = "agent_instance"
	ResourceAgentProfile  = "agent_profile"
	ResourceDirectory     = "directory_reference"
	ResourceRoot          = "root_generation"
	ResourceWatcher       = "watcher"
	ResourceSchedule      = "schedule"
	ResourceScanBatch     = "scan_batch"
	ResourceScanOutcome   = "scan_outcome"
)

// TeamRole is the one normalized File Janitor role shown in the reviewed
// proposal. It deliberately excludes prompts and arbitrary template JSON.
type TeamRole struct {
	RoleID          string `json:"role_id"`
	Name            string `json:"name"`
	Action          string `json:"action"` // create or reuse
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	ModelConfigured bool   `json:"model_configured"`
	ConfigDigest    string `json:"config_digest"`
	Warning         string `json:"warning,omitempty"`
}

// TeamPlan is produced only by the reviewed session creator from the current
// effective built-in template.
type TeamPlan struct {
	BlueprintID      string     `json:"blueprint_id"`
	BlueprintVersion int        `json:"blueprint_version"`
	BlueprintDigest  string     `json:"blueprint_digest"`
	PlanRevision     string     `json:"plan_revision"`
	Roles            []TeamRole `json:"roles"`
}

type Target struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Route       string `json:"route"`
	Supported   bool   `json:"supported"`
	Reason      string `json:"reason,omitempty"`
}

type Relationship struct {
	AssistantID string `json:"assistant_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	State       string `json:"state"`
	Eligible    bool   `json:"eligible"`
	Proactive   bool   `json:"proactive"`
}

type Proposal struct {
	Revision             string     `json:"revision"`
	Mode                 TargetMode `json:"mode"`
	BlueprintID          string     `json:"blueprint_id,omitempty"`
	BlueprintVersion     int        `json:"blueprint_version,omitempty"`
	BlueprintDigest      string     `json:"blueprint_digest,omitempty"`
	TeamPlanRevision     string     `json:"team_plan_revision,omitempty"`
	Team                 []TeamRole `json:"team"`
	Targets              []Target   `json:"targets"`
	SelectedWorkspaceID  string     `json:"selected_workspace_id,omitempty"`
	MetadataDisclosure   string     `json:"metadata_disclosure"`
	FolderDisclosure     string     `json:"folder_disclosure"`
	MonitoringDisclosure string     `json:"monitoring_disclosure"`
	FileReviewDisclosure string     `json:"file_review_disclosure"`
	DefaultSchedule      string     `json:"default_schedule"`
	Timezone             string     `json:"timezone"`
	NoAutomaticTasks     bool       `json:"no_automatic_tasks"`
	NoFileActions        bool       `json:"no_file_actions"`
}

type Run struct {
	ID                string     `json:"id"`
	OwnerUserID       string     `json:"-"`
	AssistantID       string     `json:"-"`
	CapabilityID      string     `json:"capability_id"`
	Status            RunStatus  `json:"lifecycle"`
	CurrentStep       Step       `json:"current_step"`
	Revision          int64      `json:"revision"`
	ProposalRevision  string     `json:"proposal_revision"`
	BlueprintID       string     `json:"blueprint_id"`
	BlueprintVersion  int        `json:"blueprint_version"`
	BlueprintDigest   string     `json:"blueprint_digest"`
	TeamPlanRevision  string     `json:"team_plan_revision"`
	TeamRole          TeamRole   `json:"team_role"`
	TargetMode        TargetMode `json:"target_mode"`
	TargetWorkspaceID string     `json:"target_workspace_id"`
	LastErrorCode     string     `json:"last_error_code,omitempty"`
	FailedStep        Step       `json:"failed_step,omitempty"`
	DeferredAt        *time.Time `json:"deferred_at,omitempty"`
	RetryAfter        *time.Time `json:"retry_after,omitempty"`
	ReconcileAfter    *time.Time `json:"reconcile_after,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type Operation struct {
	ID                  string          `json:"id"`
	RunID               string          `json:"run_id"`
	OwnerUserID         string          `json:"-"`
	Kind                OperationKind   `json:"kind"`
	Status              OperationStatus `json:"status"`
	AttemptCount        int             `json:"attempt_count"`
	IdempotencyDigest   string          `json:"-"`
	ReviewDigest        string          `json:"-"`
	ExpectedRunRevision int64           `json:"-"`
	WorkspaceID         string          `json:"workspace_id,omitempty"`
	ProfileProvenanceID string          `json:"-"`
	SafeOutcomeCode     string          `json:"outcome_code,omitempty"`
	SafeErrorCode       string          `json:"error_code,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	StartedAt           *time.Time      `json:"started_at,omitempty"`
	CompletedAt         *time.Time      `json:"completed_at,omitempty"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type Resource struct {
	OperationID   string            `json:"operation_id"`
	RunID         string            `json:"run_id"`
	WorkspaceID   string            `json:"workspace_id"`
	Kind          string            `json:"kind"`
	ResourceID    string            `json:"resource_id"`
	Ownership     ResourceOwnership `json:"ownership"`
	StoreOrigin   string            `json:"store_origin,omitempty"`
	VersionDigest string            `json:"version_digest,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

type Action struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	Route   string `json:"route,omitempty"`
}

type Projection struct {
	SchemaVersion int          `json:"schema_version"`
	CapabilityID  string       `json:"capability_id"`
	ViewState     string       `json:"view_state"`
	Stale         bool         `json:"stale"`
	StatusMessage string       `json:"status_message"`
	Relationship  Relationship `json:"relationship"`
	Proposal      *Proposal    `json:"proposal,omitempty"`
	Run           *Run         `json:"run,omitempty"`
	Operations    []Operation  `json:"operations,omitempty"`
	Target        *Target      `json:"target,omitempty"`
	Actions       []Action     `json:"actions"`
}

type Acceptance struct {
	OwnerUserID          string
	AssistantID          string
	ProposalRevision     string
	BlueprintID          string
	BlueprintVersion     int
	BlueprintDigest      string
	TeamPlanRevision     string
	TeamRole             TeamRole
	TargetMode           TargetMode
	TargetWorkspaceID    string
	WorkspaceOperationID string
	ProfileProvenanceID  string
	ReviewDigest         string
}

type WorkspaceResult struct {
	WorkspaceID         string
	AgentInstanceID     string
	ProfileProvenanceID string
	ProfileStoreOrigin  string
	ProfileCreated      bool
	ConfigurationDigest string
}
