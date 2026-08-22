package workspace

import (
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vaultref"
)

// WorkspaceStatus represents the current state of a workspace
type WorkspaceStatus string

const (
	StatusActive    WorkspaceStatus = "active"
	StatusCompleted WorkspaceStatus = "completed"
	StatusFailed    WorkspaceStatus = "failed"
	StatusCancelled WorkspaceStatus = "cancelled"
	StatusTrashed   WorkspaceStatus = "trashed"
	// StatusMissing marks a workspace whose folder disappeared from disk outside
	// this app. Hidden from listings and excluded from disk write-through so a
	// stale record cannot resurrect a deleted folder.
	StatusMissing WorkspaceStatus = "missing"
)

// MessageType represents the type of inter-agent message
type MessageType string

const (
	MessageTaskRequest MessageType = "task_request"
	MessageResult      MessageType = "result"
	MessageQuestion    MessageType = "question"
	MessageStatus      MessageType = "status"
)

// AutonomyPolicy controls how much agent-initiated action a mission run is allowed.
// Higher levels (Act-with-approval, Autopilot) are deferred to v1.5+.
type AutonomyPolicy string

const (
	// AutonomyWatch limits the run to read-classified tools only. No writes
	// anywhere — except workspace memory (see IsWorkspaceMemoryTool), so even
	// watch-only missions can learn across runs.
	AutonomyWatch AutonomyPolicy = "watch"
	// AutonomyPropose allows read + workspace-internal writes (draft artifacts, notes,
	// recommended-task drafts). External-effect tools remain denied.
	AutonomyPropose AutonomyPolicy = "propose"
)

// NotificationPolicy describes when a mission run's findings should reach the user
// (today via the Action Center; future channels are out of scope for v1).
type NotificationPolicy struct {
	// MinPriority filters which findings enter the Action Center. Below this, the
	// finding is still recorded against the run but suppressed from the global inbox.
	MinPriority string `json:"min_priority,omitempty"` // low | medium | high | critical
	// OnFindings controls whether the run notifies at all on a given cycle.
	OnFindings string `json:"on_findings,omitempty"` // always | if_any | never
}

// SideEffect classifies what a tool call does so the autonomy gate can allow or
// deny it. Bindings carry a DefaultSideEffect plus optional per-tool overrides.
type SideEffect string

const (
	SideEffectRead     SideEffect = "read"     // observation only; safe under all policies
	SideEffectWrite    SideEffect = "write"    // mutates workspace-internal state
	SideEffectExternal SideEffect = "external" // affects systems outside the workspace
)

// AgentInstance represents a specific instance of an agent with a stable identifier
type AgentInstance struct {
	ID             string `json:"id"`              // Stable UUID for this agent instance
	Name           string `json:"name"`            // Agent type name (e.g., "default", "writer")
	InstanceNumber int    `json:"instance_number"` // Instance number for display (e.g., 1, 2, 3)
	NodeID         string `json:"node_id"`         // Stable node ID (e.g., "default-node-1")
	Role           string `json:"role,omitempty"`  // Workspace-specific responsibility label (e.g., "Project Manager")
	Description    string `json:"description,omitempty"`
	// CustomInstructions is the workspace owner's per-attachment refinement of a
	// shared agent definition, layered onto the shared base prompt for this
	// workspace only (never mutates the global definition). PRD FR16/FR17.
	CustomInstructions string    `json:"custom_instructions,omitempty"`
	EntryPoint         bool      `json:"entry_point,omitempty"` // Marks the default entry node for workspace-level requests
	CreatedAt          time.Time `json:"created_at"`            // When this instance was added
}

// Folder represents a managed folder under the workspace files root.
type Folder struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Workspace stores shared context between collaborating agents
type Workspace struct {
	ID                   string                      `json:"id"`
	Name                 string                      `json:"name"`
	Kind                 string                      `json:"kind,omitempty"`
	Description          string                      `json:"description,omitempty"`
	FolderSlug           string                      `json:"folder_slug,omitempty"`     // Filesystem folder name (derived from Name via Slugify)
	ProjectPath          string                      `json:"project_path,omitempty"`    // Relative path to associated project code directory
	Tags                 []string                    `json:"tags,omitempty"`            // Normalized labels used for organization and filtering
	OwnerUserID          string                      `json:"owner_user_id,omitempty"`   // User profile owner for background task context
	ParentID             string                      `json:"parent_id,omitempty"`       // ID of parent workspace (empty for root-level); cached/diagnostic — physical folder location is authoritative
	OrderIndex           int                         `json:"order_index,omitempty"`     // Manual ordering within a parent workspace (portable; persisted to workspace.json)
	AgentInstances       []AgentInstance             `json:"agent_instances,omitempty"` // Stable agent instances with persistent IDs
	SharedData           map[string]any              `json:"shared_data"`
	Messages             []AgentMessage              `json:"messages"`
	Tasks                []Task                      `json:"tasks"`
	PlannerDecision      *types.PlannerDecision      `json:"planner_decision,omitempty"`
	PendingPlan          *types.PendingPlan          `json:"pending_plan,omitempty"`
	DynamicAgentRequests []types.DynamicAgentRequest `json:"dynamic_agent_requests,omitempty"`
	Attachments          []Attachment                `json:"attachments,omitempty"`
	Folders              []Folder                    `json:"folders,omitempty"`
	ScheduledTasks       []ScheduledTask             `json:"scheduled_tasks,omitempty"`
	StoreNodes           []StoreNode                 `json:"store_nodes,omitempty"`
	DirectoryReferences  []DirectoryReference        `json:"directory_references,omitempty"`
	MCPBindings          []MCPBinding                `json:"mcp_bindings,omitempty"`
	AgentMCPAccess       []AgentMCPAccess            `json:"agent_mcp_access,omitempty"`
	SkillBindings        []SkillBinding              `json:"skill_bindings,omitempty"`
	AgentSkillAccess     []AgentSkillAccess          `json:"agent_skill_access,omitempty"`
	// Toolboxes are the workspace's named, versioned capability recipes, and
	// ToolboxAssignments pin one version of one Toolbox to each stable agent
	// instance (see toolbox.go). Together they are the SOURCE OF TRUTH for
	// "what may this agent instance actually use" (FR-22).
	//
	// They live on the canonical workspace record rather than in a side store
	// so they inherit Version and lost-write protection, which is what makes a
	// Toolbox switch safe against a concurrent Workshop edit (FR-23).
	//
	// The older AgentSkillAccess / AgentMCPAccess entries above remain as a
	// compatibility surface during the migration window; after migration they
	// are derived from the assignment rather than consulted independently, so
	// they can never become a second, invisible answer (FR-36).
	Toolboxes          []ToolboxDefinition      `json:"toolboxes,omitempty"`
	ToolboxAssignments []AgentToolboxAssignment `json:"toolbox_assignments,omitempty"`
	// ToolboxMigration records that this workspace's legacy implicit capability
	// state has been made explicit, so the migration is idempotent across
	// restarts and cannot create a second `Workspace Default` (FR-34).
	ToolboxMigration *ToolboxMigrationState `json:"toolbox_migration,omitempty"`
	Workflows        map[string]Workflow    `json:"workflows,omitempty"`
	Layout           *CanvasLayout          `json:"layout,omitempty"` // Canvas layout (positions of tasks and agents)
	Status           WorkspaceStatus        `json:"status"`
	Version          int64                  `json:"version,omitempty"` // monotonic, bumped on every Save; used to detect lost writes
	// TicketMigrationVersion records that this workspace's legacy Task records
	// have been evolved in place into canonical Tickets (FR-106). It makes the
	// migration idempotent and restart-safe: a workspace already at the
	// current version is skipped, so a second run can never renumber.
	TicketMigrationVersion int `json:"ticket_migration_version,omitempty"`
	// TicketSequence is the high-water mark of workspace-local Ticket numbers
	// (FR-140). It only ever increases: allocation reserves the next value
	// inside the same store.Update transaction that persists the Ticket, so a
	// number is never reused even if the Ticket is later deleted, and a
	// concurrent create cannot hand out the same number twice. Group 7's
	// migration seeds it past the highest backfilled number.
	TicketSequence int64 `json:"ticket_sequence,omitempty"`
	// AllowNativeMCPCLI opts this workspace into letting CLI-provider agents
	// (Claude Code / Codex) run the workspace's MCP + built-in tools natively,
	// outside ori-agent's per-tool confirmation gate. Security-sensitive, so it
	// defaults OFF; an agent must also opt in (Settings.AllowNativeMCPTools).
	AllowNativeMCPCLI bool `json:"allow_native_mcp_cli,omitempty"`

	// Designation is a synced projection of the personalhq designation records
	// (internal/personalhq), not the source of truth itself. The per-user
	// designation record — "who has designated which workspace" — lives in the
	// personalhq store; this field mirrors that onto the workspace so backend
	// and UI code can branch on the workspace directly. Valid values: "" (none)
	// and "personal_hq". Written/cleared by personalhq.Service on
	// designate/replace/clear and healed on startup by the designation backfill.
	Designation string `json:"designation,omitempty"`

	// TemplateProvenance records the built-in template this workspace was created
	// from (portable metadata; see template_provenance.go). Nil for workspaces
	// created without a template or before provenance was recorded.
	TemplateProvenance *TemplateProvenance `json:"template_provenance,omitempty"`

	// InstalledCapabilities records which built-in Workspace Capabilities are
	// installed on this workspace (see installed_capabilities.go). It is the
	// authoritative answer to "does this workspace have File Janitor?" —
	// replacing inference from template provenance, workspace name, or
	// capability-specific state. Runtime health is NOT stored here; it is always
	// derived from the capability service (PRD FR-4, FR-6).
	//
	// Unrelated to the CapabilityMapping records on MCPBinding, which map
	// semantic connector operations onto MCP tools (FR-7).
	//
	// Nil means "no data" and empty means "known to have none"; every merge and
	// preservation site must guard with len()==0, never ==nil. Mirrored into
	// SQLite as installed_capabilities_json.
	InstalledCapabilities []InstalledCapability `json:"installed_capabilities,omitempty"`

	// SetupWizardProgress is the authoritative record of how far the workspace
	// has got through its blueprint's Setup Wizard (see setup_wizard.go). The
	// server owns it: the browser never reports completion, and closing the
	// dialog changes nothing here. Nil until the wizard is first opened, and
	// always nil for a workspace whose blueprint declares no wizard.
	SetupWizardProgress *SetupWizardProgress `json:"setup_wizard_progress,omitempty"`

	// RuntimeState records the user's operating-mode choice, durable runtime
	// configuration/verification history, and explicit capability grants. Live
	// connectivity is always evaluated afresh and is never persisted here.
	RuntimeState *WorkspaceRuntimeState `json:"runtime_state,omitempty"`

	// Mission fields — workspace-level proactive goal carried out by the entry
	// agent (Workspace Manager) on cadence. All fields are optional; a workspace
	// with MissionEnabled = false (the zero value) behaves exactly as before.
	Mission               string              `json:"mission,omitempty"`
	Cadence               *ScheduleConfig     `json:"cadence,omitempty"`
	AutonomyPolicy        AutonomyPolicy      `json:"autonomy_policy,omitempty"` // defaults to AutonomyPropose when MissionEnabled is true
	NotificationPolicy    *NotificationPolicy `json:"notification_policy,omitempty"`
	MissionEnabled        bool                `json:"mission_enabled,omitempty"`
	LastMissionRunAt      *time.Time          `json:"last_mission_run_at,omitempty"`
	NextMissionRunAt      *time.Time          `json:"next_mission_run_at,omitempty"`
	MissionExecutionCount int                 `json:"mission_execution_count,omitempty"`
	MissionFailureCount   int                 `json:"mission_failure_count,omitempty"`
	// MissionCadenceHeartbeat keeps the cadence timer fixed when event triggers
	// fire the mission: event-fired runs still count (LastMissionRunAt,
	// counters) but leave NextMissionRunAt untouched, so the cadence acts as a
	// hard heartbeat regardless of event activity. Default false = an
	// event-fired run pushes the next cadence run back, like any other run.
	MissionCadenceHeartbeat bool          `json:"mission_cadence_heartbeat,omitempty"`
	Opportunities           []Opportunity `json:"opportunities,omitempty"`
	// GoalBrief is the structured, user-accepted statement of what this
	// workspace's Goal needs — output, sources, semantic operations, autonomy
	// ceiling, mandatory capabilities (see toolbox_goal_brief.go). Nil until a
	// user accepts one, and an unaccepted brief never drives recommendations
	// (FR-92–FR-94).
	GoalBrief *GoalBrief `json:"goal_brief,omitempty"`
	// GoalToolboxPolicy records which Toolbox version the Goal runs with, and
	// whether that choice is pinned. Pinning is the default so a recurring goal
	// cannot silently change behavior when someone edits a toolbox (FR-103,
	// FR-104).
	GoalToolboxPolicy *GoalToolboxPolicy `json:"goal_toolbox_policy,omitempty"`

	// PinnedReaperScripts is the ordered list of REAPER script IDs (e.g.
	// "custom:filename.lua") this workspace has pinned as quick actions in
	// the console (see internal/workspace/reaper_pins.go). REAPER scripts
	// themselves are globally shared files (internal/reaper/library.go), so
	// per-workspace pin state has nowhere else portable to live and is kept
	// here instead — never written into the script file or its frontmatter.
	// A pin whose script no longer resolves is pruned at read time by
	// internal/reaperhttp, not here (FR from tasks-reaper-station-discoverability.md 1.3).
	PinnedReaperScripts []string `json:"pinned_reaper_scripts,omitempty"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	mu        sync.RWMutex   `json:"-"`
	taskIndex map[string]int `json:"-"` // Index for O(1) task lookups by ID

	// capabilitiesExplicit records that THIS in-memory value has had its
	// InstalledCapabilities deliberately edited, as opposed to merely arriving
	// without them.
	//
	// The two cases are otherwise indistinguishable — both leave the collection
	// empty — but they must produce opposite behavior on save: a record that
	// never loaded capabilities has to be refilled from the canonical
	// workspace.json (FR-144), while an uninstall has to be allowed to write the
	// collection away. See SyncStore.Save.
	//
	// Never persisted and never cloned: it is intent attached to one
	// mutate-then-save cycle (the window CanonicalUpdate holds open), and a copy
	// decoded from disk correctly carries no pending intent.
	capabilitiesExplicit bool

	// missionLoaded records that THIS in-memory value arrived with its Goal
	// configuration, as opposed to arriving without it.
	//
	// The two are otherwise indistinguishable — both leave Mission empty and
	// MissionEnabled false — but they must produce opposite behavior on save: a
	// record that predates the mission_state_json column has to be refilled
	// from the canonical workspace.json, while a Goal the user deliberately
	// cleared has to be allowed to write through. The primary store sets this
	// via MarkMissionLoaded when its envelope was present (SQL NULL means
	// absent), so a cleared Goal — a real, empty-valued envelope — is never
	// resurrected. See SyncStore.Save.
	//
	// Never persisted and never cloned: it describes how one value was loaded,
	// and a copy decoded from disk always carries its own Goal directly.
	missionLoaded bool

	// pinnedReaperScriptsExplicit records that THIS in-memory value has had its
	// PinnedReaperScripts deliberately edited (by ReaperPinService or template
	// starter-pack seeding), as opposed to merely arriving without them.
	//
	// The two cases are otherwise indistinguishable — both leave the slice
	// empty — but they must produce opposite behavior on save: a workspace
	// record that never loaded pins (e.g. the lean SQLite-primary projection
	// SyncStore reads before restoring folder-only state) has to be refilled
	// from the canonical workspace.json, while unpinning the last quick action
	// has to be allowed to write the empty collection away. Mirrors
	// capabilitiesExplicit above for the exact same reason — see SyncStore.Save.
	//
	// Never persisted and never cloned: it is intent attached to one
	// mutate-then-save cycle, and a copy decoded from disk correctly carries no
	// pending intent.
	pinnedReaperScriptsExplicit bool
}

// MarkPinnedReaperScriptsExplicit records that this value's PinnedReaperScripts
// was deliberately written, not merely absent. Called by ReaperPinService
// (internal/workspace/reaper_pins.go) and template starter-pack seeding after
// mutating the field; see the field comment for why the distinction matters.
func (w *Workspace) MarkPinnedReaperScriptsExplicit() {
	if w == nil {
		return
	}
	w.pinnedReaperScriptsExplicit = true
}

// PinnedReaperScriptsExplicit reports whether this in-memory workspace has had
// its PinnedReaperScripts deliberately written during this value's lifetime,
// as opposed to merely arriving without them. See the pinnedReaperScriptsExplicit
// field doc comment on Workspace.
func (w *Workspace) PinnedReaperScriptsExplicit() bool {
	if w == nil {
		return false
	}
	return w.pinnedReaperScriptsExplicit
}

// MarkMissionLoaded records that this value's Goal configuration came from a
// store that actually carried it. Called by the primary store's conversion; see
// the field comment for why the distinction matters.
func (w *Workspace) MarkMissionLoaded() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.missionLoaded = true
}

// MissionLoaded reports whether this value arrived with its Goal configuration.
func (w *Workspace) MissionLoaded() bool {
	if w == nil {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.missionLoaded
}

// CanvasLayout stores positions of tasks and agents on the canvas
type CanvasLayout struct {
	TaskPositions       map[string]Position `json:"task_positions,omitempty"`       // task ID -> position
	AgentPositions      map[string]Position `json:"agent_positions,omitempty"`      // agent node ID -> position (falls back to name for legacy layouts)
	AttachmentPositions map[string]Position `json:"attachment_positions,omitempty"` // attachment ID -> position
	SchedulerPositions  map[string]Position `json:"scheduler_positions,omitempty"`  // scheduler node ID -> position
	StorePositions      map[string]Position `json:"store_positions,omitempty"`      // store node ID -> position
	DirectoryPositions  map[string]Position `json:"directory_positions,omitempty"`  // directory reference ID -> position
	FolderPositions     map[string]Position `json:"folder_positions,omitempty"`     // managed workspace folder ID -> position
	// StationPositions holds HQ command-map station positions, keyed by HQ
	// station registry key (e.g. "email"). Unlike every other position map on
	// this struct, values are FRACTIONAL coordinates in [0,1] relative to the
	// command map's field, not canvas pixels — this keeps a saved position
	// meaningful across viewport sizes. Written only by the scoped
	// station-layout save path; the canvas layout save path must never touch
	// this field (see SaveLayoutHandler / SaveStationLayoutHandler).
	StationPositions    map[string]Position        `json:"station_positions,omitempty"`
	WorkflowConnections []WorkflowConnectionLayout `json:"workflow_connections,omitempty"` // connections between tasks/agents
	Scale               float64                    `json:"scale,omitempty"`                // zoom level
	OffsetX             float64                    `json:"offset_x,omitempty"`             // pan offset X
	OffsetY             float64                    `json:"offset_y,omitempty"`             // pan offset Y
}

// Position represents a 2D position on the canvas
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WorkflowConnectionLayout represents a connection between nodes (task/agent)
type WorkflowConnectionLayout struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	FromPort string `json:"fromPort"`
	To       string `json:"to"`
	ToPort   string `json:"toPort"`
	Color    string `json:"color,omitempty"`
	Animated bool   `json:"animated,omitempty"`
}

// AttachmentType represents the attachment content type
type AttachmentType string

const (
	AttachmentTypeDoc   AttachmentType = "doc"
	AttachmentTypeImage AttachmentType = "image"
	AttachmentTypeOther AttachmentType = "other"
)

// AttachmentFileMeta captures optional file information
type AttachmentFileMeta struct {
	Name         string `json:"name,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Mime         string `json:"mime,omitempty"`
	URL          string `json:"url,omitempty"`
	RelativePath string `json:"relative_path,omitempty"`
	OriginalPath string `json:"original_path,omitempty"`
	Status       string `json:"status,omitempty"`
	// Checksum is the SHA-256 (hex) of the on-disk file, used to re-identify a
	// workspace-owned file after it is renamed or moved outside the app. It is
	// cached against ChecksumModTime: the hash is only recomputed when the file's
	// mod time (or size) changes. Empty for legacy attachments until backfilled.
	Checksum        string    `json:"checksum,omitempty"`
	ChecksumModTime time.Time `json:"checksum_mod_time,omitempty"`
}

// Attachment represents a note/file/link pinned to the workspace canvas
type Attachment struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspace_id"`
	Title       string              `json:"title"`
	Body        string              `json:"body,omitempty"`
	Type        AttachmentType      `json:"type"`
	Color       string              `json:"color,omitempty"`
	LinkURL     string              `json:"link_url,omitempty"`
	File        *AttachmentFileMeta `json:"file_meta,omitempty"`
	VaultRef    *vaultref.Reference `json:"vault_reference,omitempty"`
	X           float64             `json:"x"`
	Y           float64             `json:"y"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	DeletedAt   *time.Time          `json:"deleted_at,omitempty"` // Soft-delete timestamp (nil = active, set = in trash)
}

// AgentMessage represents a message passed between agents
type AgentMessage struct {
	ID        string         `json:"id"`
	From      string         `json:"from"`
	To        string         `json:"to"` // empty = broadcast
	Type      MessageType    `json:"type"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// Task represents a delegated task within a workspace
type Task struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	From           string `json:"from"`
	To             string `json:"to"`
	AssignedNodeID string `json:"assigned_node_id,omitempty"` // Specific agent instance (node) when multiple share a name
	// Assignment provenance — who chose this task's assignee and why. See
	// task_assignment.go for the mode values and the coordinator resolver.
	AssignedBy       string               `json:"assigned_by,omitempty"`       // Coordinator agent name, or "manual" for a user override
	AssignmentMode   TaskAssignmentMode   `json:"assignment_mode,omitempty"`   // How the assignee was chosen
	AssignmentReason string               `json:"assignment_reason,omitempty"` // Short human-readable rationale
	Description      string               `json:"description"`
	Details          string               `json:"details,omitempty"`
	ReferenceURL     string               `json:"reference_url,omitempty"`
	Tags             []string             `json:"tags,omitempty"` // Normalized labels used for organization and filtering
	Priority         int                  `json:"priority"`
	Context          map[string]any       `json:"context"`
	Timeout          time.Duration        `json:"timeout"`
	Status           TaskStatus           `json:"status"`
	Result           string               `json:"result,omitempty"`
	ResultType       TaskResultType       `json:"result_type,omitempty"`
	StructuredResult map[string]any       `json:"structured_result,omitempty"`
	Error            string               `json:"error,omitempty"`
	Progress         *TaskProgress        `json:"progress,omitempty"`
	ExecutionMode    TaskExecutionMode    `json:"execution_mode,omitempty"`
	ExecutionSteps   []TaskExecutionStep  `json:"execution_steps,omitempty"`
	ExecutionTrace   []TaskExecutionTrace `json:"execution_trace,omitempty"`
	CurrentRunID     string               `json:"current_run_id,omitempty"`
	// OrchestrationMode controls how parent tasks execute their subtasks.
	OrchestrationMode TaskOrchestrationMode `json:"orchestration_mode,omitempty"`
	// ResultCombinationMode controls how a parent task combines subtask outputs.
	ResultCombinationMode TaskResultCombinationMode `json:"result_combination_mode,omitempty"`
	// CombinationInstruction adds optional guidance for result aggregation.
	CombinationInstruction string `json:"combination_instruction,omitempty"`
	// OutputSchema requires the task result to be returned as structured JSON.
	// Retained for backward compatibility; new code reads via OutputSpec.
	OutputSchema *TaskOutputSchema `json:"output_schema,omitempty"`
	// OutputContract validates task results before automatic storage.
	// Retained for backward compatibility; new code reads via OutputSpec.
	OutputContract *TaskOutputContract `json:"output_contract,omitempty"`
	// OutputSpec is the active approved structured output spec used at execution time.
	OutputSpec *TaskOutputSpec `json:"output_spec,omitempty"`
	// DraftOutputSpec is the single pending draft awaiting user approval.
	DraftOutputSpec *TaskOutputSpec `json:"draft_output_spec,omitempty"`
	// TemplateRef tracks which reusable template and step produced this task.
	TemplateRef *TaskTemplateRef `json:"template_ref,omitempty"`
	// InputTaskIDs specifies task IDs whose results should be included as input context
	InputTaskIDs []string `json:"input_task_ids,omitempty"`
	// ParentTaskID groups this task under a parent workflow task when set.
	ParentTaskID string `json:"parent_task_id,omitempty"`
	// SubtaskIndex is a 1-based ordering hint within the parent workflow.
	SubtaskIndex int        `json:"subtask_index,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`

	// Schedule fields - enables task to run on a schedule (re-runs same task)
	Schedule         *ScheduleConfig `json:"schedule,omitempty"`
	ScheduleEnabled  bool            `json:"schedule_enabled,omitempty"`
	ScheduleName     string          `json:"schedule_name,omitempty"`
	SleepPolicy      string          `json:"sleep_policy,omitempty"`
	WakeMacEnabled   bool            `json:"wake_mac_enabled,omitempty"`
	WakeLeadMinutes  int             `json:"wake_lead_minutes,omitempty"`
	WakeFallback     string          `json:"wake_fallback_policy,omitempty"`
	NextRun          *time.Time      `json:"next_run,omitempty"`
	LastRun          *time.Time      `json:"last_run,omitempty"`
	ExecutionCount   int             `json:"execution_count,omitempty"`
	FailureCount     int             `json:"failure_count,omitempty"`
	ExecutionHistory []TaskExecution `json:"execution_history,omitempty"`

	// Result storage configuration - auto-save results on completion
	ResultStorage *ResultStorageConfig `json:"result_storage,omitempty"`

	// RuntimeInputs holds data derived at execution time (e.g. results of
	// upstream tasks named in InputTaskIDs). It is rebuilt fresh for each
	// execution and never persisted — keeping it here instead of merging
	// into Context prevents the persisted task from accumulating stale
	// runtime state across re-runs.
	RuntimeInputs *TaskRuntimeInputs `json:"-"`

	// RuntimeExecution is a trusted, one-run execution policy assembled by the
	// server after an explicit fallback confirmation. It is never decoded from
	// JSON or persisted. CLI providers use its staging root instead of the real
	// workspace, which lets Ori promote exactly one verified project-file change.
	RuntimeExecution *TaskRuntimeExecution `json:"-"`

	// --- Backlog lifecycle (tasks/prd-workspace-backlog.md) ---

	// BacklogRank orders items within the Backlog stage for display and
	// promotion ordering (lower sorts first, then CreatedAt, then ID). It is
	// meaningless once the item leaves Backlog.
	BacklogRank int64 `json:"backlog_rank,omitempty"`
	// SourceType/SourceID record backlog capture provenance: which surface
	// created the item (e.g. "manual", "assistant", "action_center",
	// "backlog_markdown") and, when applicable, the originating record's ID
	// (e.g. an Action Center opportunity ID). Display-only metadata.
	SourceType string `json:"source_type,omitempty"`
	SourceID   string `json:"source_id,omitempty"`
	// RequiredCapabilities lists the abstract capability keys (see
	// task_capability.go) that must be connected and healthy before this task
	// may execute — e.g. "email" for an inbox-triage task. Provider-neutral by
	// construction: the key never names Gmail, Outlook, or any specific
	// connector. Empty (the default, and every task created before this field
	// existed) means the task has no connection precondition.
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	// FileFallbackFor lists required runtime capabilities for which this task
	// has an explicitly authored project-file fallback. It does not authorize a
	// fallback by itself: execution still requires a fresh user confirmation on
	// the structured blocked workflow. Empty means live-only behavior.
	FileFallbackFor []string `json:"file_fallback_for,omitempty"`
	// AwaitingExecutionIntent marks a Ready-or-later task that has not yet
	// received an explicit assignment, schedule, or run action (PRD FR11).
	// It is set true only by Backlog promotion and direct unassigned-Ready
	// creation; every other task-creation path leaves it false (the zero
	// value), so pre-existing and normally-created tasks are unaffected by
	// its introduction. Coordinator and scheduler claim sweeps must skip
	// tasks where this is true — see taskEligibleForCoordinatorClaim in
	// coordinator.go. It is cleared the moment a real assignee is stamped
	// (stampAssignment) or a schedule/run action is explicitly taken.
	AwaitingExecutionIntent bool `json:"awaiting_execution_intent,omitempty"`

	// --- Canonical Ticket lifecycle (tasks/prd-workspace-ticket-management.md) ---
	//
	// These fields evolve this record in place into the product-facing Ticket
	// (FR-2). There is deliberately no parallel Ticket row or mirror file: the
	// same persisted Task IS the Ticket, so every stable ID, Run reference,
	// schedule, and result above survives the rename untouched (FR-3, FR-5).

	// TicketState is the canonical, first-class lifecycle state (FR-6, FR-7).
	// It is the ONLY lifecycle authority. It is never derived from Status,
	// KanbanColumnID, tags, assignee, latest Run status, or the presence of a
	// result — those describe execution attempts or presentation, not the
	// durable workflow state of the request (FR-8).
	//
	// Empty means "not yet migrated": records persisted before this field
	// existed carry legacy Status only, and Group 7's migration backfills
	// them. Read through Task.CanonicalState() rather than this field
	// directly so unmigrated records still resolve to a sane state.
	TicketState TicketState `json:"ticket_state,omitempty"`
	// TicketNumber is the immutable, workspace-local, human-readable sequence
	// number (FR-140). It is allocated once from Workspace.TicketSequence and
	// never reused or renumbered — not by state changes, retries, reopening,
	// or migration. The stable ID above remains the relationship key; this is
	// purely what users say out loud.
	TicketNumber int64 `json:"ticket_number,omitempty"`
	// StateRank orders Tickets manually within one owning workspace and one
	// state (FR-15). Rank spaces are per owner and per state, so moving
	// between states always assigns a fresh deterministic destination rank.
	// It supersedes BacklogRank, which remains only as migration input.
	StateRank int64 `json:"state_rank,omitempty"`
	// DueDate is the first-class optional due date (FR-14), replacing the
	// generic kanban_due_date context entry that Group 7 migrates in.
	DueDate *time.Time `json:"due_date,omitempty"`
	// LinkedNoteIDs holds explicit structured references to Notes in the same
	// workspace (FR-17). Relationships are never parsed out of body Markdown,
	// and linking never mutates the Note itself (FR-18).
	LinkedNoteIDs []string `json:"linked_note_ids,omitempty"`
	// StateHistory is the append-only audit trail of state transitions
	// (FR-40). Entries are never rewritten, so a reopened or retried Ticket
	// keeps the full record of how it got here.
	StateHistory []TicketStateChange `json:"state_history,omitempty"`
	// TicketVersion is the optimistic-concurrency token (FR-93). It is bumped
	// on every canonical mutation; a mutation carrying a stale value is
	// refused with ErrTicketVersionConflict rather than silently overwriting
	// a concurrent editor.
	TicketVersion int64 `json:"ticket_version,omitempty"`
	// UpdatedAt records the last canonical mutation (FR-4). CreatedAt above
	// is never touched after creation. A zero value means the record has not
	// been mutated since it was created; readers fall back to CreatedAt.
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskRuntimeExecution is a trusted process-local provider policy. The root is
// generated by server code and deliberately cannot survive JSON round trips.
type TaskRuntimeExecution struct {
	WorkspaceRoot string
	DisableTools  bool
	FileOnly      bool
	Filename      string
}

// TaskRuntimeInputs carries the inputs computed for a task at execution time.
// All fields are keyed by input task ID.
type TaskRuntimeInputs struct {
	// TaskResults maps each input task ID to its raw text result.
	TaskResults map[string]string
	// StructuredOutputs maps each input task ID to its parsed structured
	// output (when the upstream task declared an OutputSchema and the result
	// matched).
	StructuredOutputs map[string]map[string]any
}

// ResultStorageConfig specifies how task results should be automatically stored
type ResultStorageConfig struct {
	Enabled       bool   `json:"enabled"`                    // Enable auto-save on completion
	StoreNodeID   string `json:"store_node_id,omitempty"`    // Save to specific store node (if set)
	StorageTarget string `json:"storage_target,omitempty"`   // Destination mode: workspace_folder or external/default
	Folder        string `json:"workspace_folder,omitempty"` // Folder path under the workspace-owned files root
	FilePath      string `json:"file_path,omitempty"`        // Custom file path (if no store node)
	FileName      string `json:"file_name,omitempty"`        // Custom file name within the default/derived folder (no directory); ignored when FilePath is a full file
	Format        string `json:"format,omitempty"`           // Output format: text, json, markdown, csv
	WriteMode     string `json:"write_mode,omitempty"`       // Output mode: new_file, append
}

// TaskStatus represents the current state of a task
type TaskStatus string

const (
	// TaskStatusBacklog is the uncommitted capture stage added by the
	// workspace-backlog feature (tasks/prd-workspace-backlog.md). It is a new
	// value with no legacy meaning, so no persisted task predates it — every
	// pre-existing task record is already Pending-or-later and needs no
	// status-level migration (only the kanban board's default column config
	// does; see session.MigrateLegacyKanbanBoardConfig).
	//
	// Compatibility mapping (PRD FR7-8, task-list 1.1/1.10):
	//   - Backlog is excluded from every "is this task open/pending/runnable"
	//     check by construction: those checks positive-match specific legacy
	//     statuses (Pending/Assigned/InProgress/WaitingForChoice), and Backlog
	//     never appears in that set, so no change was needed at those sites.
	//   - The few call sites that used a *negative* match instead ("anything
	//     except InProgress is runnable") needed an explicit Backlog exclusion:
	//     coordinator.go's entry-agent defaulting/claim sweep, scheduler.go's
	//     executeTaskSchedule/rerunTargetTask, and orchestrationhttp/
	//     task_execution.go's ExecuteTaskHandler/StartTaskAsync/
	//     executeTaskWithDependencies/executeInputTasksIfNeeded — see
	//     RequireTaskNotBacklog in task_backlog.go, used by all of them.
	//   - The legacy kanban_column_id value "backlog" (the old default board
	//     column in internal/session/kanban_board.go) is unrelated
	//     presentation metadata that never implied this status; it is migrated
	//     separately to "ready" by the board-config version bump.
	TaskStatusBacklog TaskStatus = "backlog"

	TaskStatusPending          TaskStatus = "pending"
	TaskStatusAssigned         TaskStatus = "assigned"
	TaskStatusInProgress       TaskStatus = "in_progress"
	TaskStatusWaitingForChoice TaskStatus = "waiting_for_choice"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCancelled        TaskStatus = "cancelled"
	TaskStatusTimeout          TaskStatus = "timeout"
)

// TaskProgress tracks the execution progress of a task
type TaskProgress struct {
	Percentage     int       `json:"percentage"`             // 0-100
	CurrentStep    string    `json:"current_step,omitempty"` // e.g. "Analyzing data..."
	TotalSteps     int       `json:"total_steps,omitempty"`
	CompletedSteps int       `json:"completed_steps,omitempty"`
	ElapsedTimeMs  float64   `json:"elapsed_time_ms,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

// TaskExecutionMode controls how internal execution steps advance.
type TaskExecutionMode string

const (
	TaskExecutionModeAuto        TaskExecutionMode = "auto"
	TaskExecutionModeStepThrough TaskExecutionMode = "step_through"
)

// TaskOrchestrationMode controls how a parent task executes its subtasks.
type TaskOrchestrationMode string

const (
	TaskOrchestrationModeSequential TaskOrchestrationMode = "sequential"
	TaskOrchestrationModeGraph      TaskOrchestrationMode = "graph"
)

// TaskResultCombinationMode controls how parent tasks combine child results.
type TaskResultCombinationMode string

const (
	TaskResultCombinationLastResult       TaskResultCombinationMode = "last_result"
	TaskResultCombinationConcat           TaskResultCombinationMode = "concat"
	TaskResultCombinationJSONMap          TaskResultCombinationMode = "json_map"
	TaskResultCombinationStructuredOutput TaskResultCombinationMode = "structured_outputs"
)

// TaskTemplateRef tracks which reusable template and step produced a task.
type TaskTemplateRef struct {
	TemplateID   string `json:"template_id,omitempty"`
	TemplateName string `json:"template_name,omitempty"`
	StepID       string `json:"step_id,omitempty"`
	StepName     string `json:"step_name,omitempty"`
}

// TaskOutputSchema describes the structured JSON object a task must return.
type TaskOutputSchema struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Strict      bool              `json:"strict,omitempty"`
	Fields      []TaskOutputField `json:"fields,omitempty"`
}

// TaskOutputField describes one field in a structured task result.
type TaskOutputField struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // string, number, integer, boolean, object, array
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// TaskOutputContract describes the CSV-oriented row shape a task result must satisfy before storage.
type TaskOutputContract struct {
	Version string                     `json:"version,omitempty"`
	Source  string                     `json:"source,omitempty"` // ai_suggested, manual, csv_header
	Columns []TaskOutputContractColumn `json:"columns,omitempty"`
}

// TaskOutputContractColumn describes one expected CSV output column.
type TaskOutputContractColumn struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // string, number, boolean, date
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// TaskOutputSpec groups schema, contract, mapping rules, metadata policy, version,
// source, and approval metadata into a single structured output spec. A spec with
// Approval == nil is a draft; with Approval != nil is the active approved spec.
type TaskOutputSpec struct {
	Version        string                    `json:"version,omitempty"` // ocv_... contract version, assigned on approval
	Source         string                    `json:"source,omitempty"`  // ai_suggested, manual, csv_header
	Schema         *TaskOutputSchema         `json:"schema,omitempty"`
	Contract       *TaskOutputContract       `json:"contract,omitempty"`
	Mappings       []TaskOutputMapping       `json:"mappings,omitempty"`
	MetadataPolicy *TaskOutputMetadataPolicy `json:"metadata_policy,omitempty"`
	Approval       *TaskOutputApproval       `json:"approval,omitempty"`
}

// TaskOutputMapping connects a schema field to a CSV contract column.
type TaskOutputMapping struct {
	SchemaField  string `json:"schema_field"`
	CSVColumn    string `json:"csv_column"`
	Transform    string `json:"transform,omitempty"` // identity, json_string
	DefaultValue string `json:"default_value,omitempty"`
}

// TaskOutputMetadataPolicy declares which system metadata fields are appended to CSV
// rows. Fields kept on run artifacts but hidden from CSV have Include=false.
type TaskOutputMetadataPolicy struct {
	Fields []TaskOutputMetadataField `json:"fields,omitempty"`
}

// TaskOutputMetadataField represents one system metadata field's CSV visibility.
type TaskOutputMetadataField struct {
	Name    string `json:"name"`              // run_id, executed_at, status, duration_ms
	Include bool   `json:"include,omitempty"` // true = include in CSV; false = run artifact only
}

// TaskOutputApproval records who approved a spec and when. Presence promotes a spec from draft to active.
type TaskOutputApproval struct {
	ApprovedAt time.Time `json:"approved_at"`
	ApprovedBy string    `json:"approved_by,omitempty"`
}

// Supported mapping transforms (PRD Tech Consideration 1.3).
const (
	TaskOutputMappingTransformIdentity   = "identity"
	TaskOutputMappingTransformJSONString = "json_string"
)

// Default system metadata field names attached to runs.
var DefaultTaskOutputMetadataFieldNames = []string{"run_id", "executed_at", "status", "duration_ms"}

// TaskValidationStatus records whether a completed run satisfied its output contract.
type TaskValidationStatus string

const (
	TaskValidationNotApplicable    TaskValidationStatus = "not_applicable"
	TaskValidationPassed           TaskValidationStatus = "passed"
	TaskValidationNeedsReview      TaskValidationStatus = "needs_review"
	TaskValidationDismissed        TaskValidationStatus = "dismissed"
	TaskValidationManuallyApproved TaskValidationStatus = "manually_approved"
)

// TaskStorageStatus records what happened to a run after validation.
type TaskStorageStatus string

const (
	TaskStorageNotAttempted     TaskStorageStatus = "not_attempted"
	TaskStorageSaved            TaskStorageStatus = "saved"
	TaskStorageAppended         TaskStorageStatus = "appended"
	TaskStorageSkippedInvalid   TaskStorageStatus = "skipped_invalid"
	TaskStorageManuallyAppended TaskStorageStatus = "manually_appended"
)

// TaskValidationResult stores the validation and storage outcome for one run.
type TaskValidationResult struct {
	ValidationStatus TaskValidationStatus  `json:"validation_status"`
	StorageStatus    TaskStorageStatus     `json:"storage_status"`
	ContractVersion  string                `json:"contract_version,omitempty"`
	Errors           []TaskValidationError `json:"errors,omitempty"`
	RawOutputRef     string                `json:"raw_output_ref,omitempty"`
	NormalizedRowRef string                `json:"normalized_row_ref,omitempty"`
	NormalizedRow    map[string]any        `json:"normalized_row,omitempty"`
	OutputSpec       *TaskOutputSpec       `json:"output_spec_snapshot,omitempty"`
	RepairStatus     string                `json:"repair_status,omitempty"`
	ManualApproval   *TaskManualApproval   `json:"manual_approval,omitempty"`
	ValidatedAt      *time.Time            `json:"validated_at,omitempty"`
}

// TaskValidationError is a structured, user-facing validation failure.
type TaskValidationError struct {
	Code     string   `json:"code"`
	Column   string   `json:"column,omitempty"`
	Message  string   `json:"message"`
	Expected []string `json:"expected,omitempty"`
	Actual   []string `json:"actual,omitempty"`
}

// TaskManualApproval records who manually approved a gated result and when.
type TaskManualApproval struct {
	ApprovedAt time.Time `json:"approved_at"`
	ApprovedBy string    `json:"approved_by,omitempty"`
}

// TaskExecutionStepStatus tracks a single internal execution step.
type TaskExecutionStepStatus string

const (
	TaskExecutionStepPending    TaskExecutionStepStatus = "pending"
	TaskExecutionStepInProgress TaskExecutionStepStatus = "in_progress"
	TaskExecutionStepCompleted  TaskExecutionStepStatus = "completed"
	TaskExecutionStepFailed     TaskExecutionStepStatus = "failed"
	TaskExecutionStepBlocked    TaskExecutionStepStatus = "blocked"
	TaskExecutionStepSkipped    TaskExecutionStepStatus = "skipped"
)

// TaskExecutionStep represents a persisted internal step for a task.
type TaskExecutionStep struct {
	ID          string                  `json:"id"`
	Index       int                     `json:"index"`
	Title       string                  `json:"title"`
	Detail      string                  `json:"detail,omitempty"`
	Tag         string                  `json:"tag,omitempty"`
	Status      TaskExecutionStepStatus `json:"status"`
	Result      string                  `json:"result,omitempty"`
	Error       string                  `json:"error,omitempty"`
	StartedAt   *time.Time              `json:"started_at,omitempty"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}

// TaskExecutionTrace represents a persisted execution event for a task run.
type TaskExecutionTrace struct {
	Type      string    `json:"type"`
	Status    string    `json:"status,omitempty"`
	Title     string    `json:"title,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Source    string    `json:"source,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ScheduleType represents the type of schedule
type ScheduleType string

const (
	ScheduleOnce          ScheduleType = "once"           // Execute once at specific time
	ScheduleInterval      ScheduleType = "interval"       // Every X duration
	ScheduleDaily         ScheduleType = "daily"          // Every day at specific time
	ScheduleWeekly        ScheduleType = "weekly"         // Every week on specific day/time
	ScheduleMonthly       ScheduleType = "monthly"        // Every month on a specific day/time
	ScheduleCron          ScheduleType = "cron"           // Cron expression (advanced)
	ScheduleRelativeDelay ScheduleType = "relative_delay" // Delay from trigger/enable time
)

// ScheduleConfig defines how a scheduled task should be executed
type ScheduleConfig struct {
	Type ScheduleType `json:"type"` // Type of schedule

	// For "once" type
	ExecuteAt *time.Time `json:"execute_at,omitempty"`

	// For "interval" type
	Interval time.Duration `json:"interval,omitempty"` // e.g., 5m, 1h, 24h

	// For "cron" type
	CronExpr string `json:"cron_expr,omitempty"` // e.g., "0 9 * * *"

	// For "daily" type
	TimeOfDay string `json:"time_of_day,omitempty"` // e.g., "09:00", "14:30"

	// For "weekly" type
	DayOfWeek int `json:"day_of_week,omitempty"` // 0=Sunday, 1=Monday, ..., 6=Saturday

	// For "monthly" type. DayOfMonth is 1-31; if the current month has fewer
	// days than DayOfMonth the next-run calculation clamps to the last day of
	// that month (e.g., DayOfMonth=31 in February fires on Feb 28/29).
	DayOfMonth int `json:"day_of_month,omitempty"`

	// For "relative_delay" type
	DelayDuration time.Duration `json:"delay_duration,omitempty"` // e.g., 30s, 5m, 1h
	TriggerOnce   bool          `json:"trigger_once,omitempty"`   // true = one-time after delay, false = repeat after each delay

	// Limits
	MaxRuns int        `json:"max_runs,omitempty"` // 0 = infinite
	EndDate *time.Time `json:"end_date,omitempty"` // nil = no end date
}

// TaskExecution represents a single recorded run of a task.
type TaskExecution struct {
	TaskID     string                `json:"task_id"`            // ID of the executed task
	RunID      string                `json:"run_id,omitempty"`   // Workspace Run backing this execution, when available
	ExecutedAt time.Time             `json:"executed_at"`        // When the run started
	Status     string                `json:"status"`             // "success", "failed", or "blocked"
	Summary    string                `json:"summary,omitempty"`  // Short result or failure summary (truncated, ~360 chars)
	Result     string                `json:"result,omitempty"`   // Full result body, capped at maxRecordedTaskExecutionResult bytes
	Error      string                `json:"error,omitempty"`    // Full error message if failed or blocked
	Duration   int64                 `json:"duration,omitempty"` // Execution duration in milliseconds
	Validation *TaskValidationResult `json:"validation_result,omitempty"`
}

// ScheduledTask represents a recurring or one-time scheduled task template
type ScheduledTask struct {
	ID           string         `json:"id"`
	WorkspaceID  string         `json:"workspace_id"`
	CanvasNodeID string         `json:"canvas_node_id,omitempty"` // Links to canvas scheduler node (empty for dashboard-created tasks)
	TargetTaskID string         `json:"target_task_id,omitempty"` // Links to a canvas task node to execute on schedule
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	From         string         `json:"from"`   // Sender agent
	To           string         `json:"to"`     // Recipient agent
	Prompt       string         `json:"prompt"` // Task description/prompt
	Priority     int            `json:"priority"`
	Context      map[string]any `json:"context"`

	// Scheduling configuration
	Schedule ScheduleConfig `json:"schedule"`
	NextRun  *time.Time     `json:"next_run"`
	LastRun  *time.Time     `json:"last_run"`
	Enabled  bool           `json:"enabled"`

	// Execution tracking
	ExecutionCount int    `json:"execution_count"`
	FailureCount   int    `json:"failure_count"`
	LastResult     string `json:"last_result,omitempty"`
	LastError      string `json:"last_error,omitempty"`

	// Execution history (last 20 executions)
	ExecutionHistory []TaskExecution `json:"execution_history,omitempty"`

	// Metadata
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StoreNode represents a file storage node on the canvas
type StoreNode struct {
	ID            string    `json:"id"`
	CanvasNodeID  string    `json:"canvas_node_id"`
	AgentNodeID   string    `json:"agent_node_id"` // Agent instance this store is connected to
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	BaseDir       string    `json:"base_dir"` // Base directory (e.g., "reports/")
	StorageTarget string    `json:"storage_target,omitempty"`
	Folder        string    `json:"workspace_folder,omitempty"`
	Format        string    `json:"format"`     // "json", "text", "markdown", "csv", "binary"
	WriteMode     string    `json:"write_mode"` // "overwrite", "append"
	AutoCreateDir bool      `json:"auto_create_dir"`
	AutoStore     bool      `json:"auto_store"` // Automatically store task results on completion
	LastWriteTime time.Time `json:"last_write_time"`
	WriteCount    int       `json:"write_count"`
	LastError     string    `json:"last_error"`
	LastFilePath  string    `json:"last_file_path"` // Last written file (relative to base_dir)
	X             float64   `json:"x"`
	Y             float64   `json:"y"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// MCPBinding represents a concrete MCP binding owned by the workspace.
// ServerName maps to the globally configured MCP server template/definition.
type MCPBinding struct {
	ID         string `json:"id"`
	ServerName string `json:"server_name"`
	Alias      string `json:"alias,omitempty"`
	Enabled    bool   `json:"enabled"`
	// RuntimeKind is how this binding is realized: an MCP server template
	// ("mcp") or one of Ori's native capabilities ("native_email"). It is
	// consulted BEFORE any MCP template lookup, which is what keeps a native
	// mailbox binding from being mistaken for a missing MCP server. Empty on
	// every binding authored before this field existed; see
	// EffectiveRuntimeKind for the backward-compatible classification.
	RuntimeKind BindingRuntimeKind `json:"runtime_kind,omitempty"`
	Scope       map[string]any     `json:"scope,omitempty"`
	Config      map[string]any     `json:"config,omitempty"`
	// DefaultSideEffect classifies the binding's tools when no per-tool override
	// applies. Empty means unclassified; mission runs must not invoke this
	// binding until the user classifies it (one-time prompt on mission enable).
	DefaultSideEffect SideEffect `json:"default_side_effect,omitempty"`
	// ToolOverrides maps individual tool names to a SideEffect classification,
	// overriding DefaultSideEffect for that tool. Used for mixed-capability
	// servers (e.g. filesystem servers with both read_file and write_file).
	ToolOverrides map[string]SideEffect `json:"tool_overrides,omitempty"`
	// AllowedTools, when non-nil, restricts which of the server's tools this
	// binding may expose to chat/tasks/missions/delegated agents -- an
	// explicit empty list means no tools are exposed. A nil value (the
	// default, and every binding authored before this field existed)
	// preserves legacy all-tools behavior. See AllowsAllTools/ToolAllowed.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// CapabilityMappings binds this server's concrete tools onto Ori's
	// abstract capability contracts (e.g. "calendar") so consuming code can
	// invoke semantic operations without knowing which connector is bound.
	// See internal/calendar for the calendar capability's contract and the
	// deterministic JSON-Pointer mapping types in this package.
	CapabilityMappings []CapabilityMapping `json:"capability_mappings,omitempty"`
	CreatedAt          time.Time           `json:"created_at,omitempty"`
	UpdatedAt          time.Time           `json:"updated_at,omitempty"`
}

// AgentMCPAccess narrows which workspace MCP bindings an agent instance
// may use. When no access entry exists for an instance, all enabled bindings are allowed.
type AgentMCPAccess struct {
	AgentInstanceID   string    `json:"agent_instance_id"`
	EnabledBindingIDs []string  `json:"enabled_binding_ids,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

// SkillBinding represents a skill binding owned by the workspace.
// SkillName maps to a skill known to the SkillManager (resolved by name at runtime).
type SkillBinding struct {
	ID        string         `json:"id"`
	SkillName string         `json:"skill_name"`
	Enabled   bool           `json:"enabled"`
	Trusted   bool           `json:"trusted"`
	Config    map[string]any `json:"config,omitempty"`
	// DefaultSideEffect classifies the skill when no per-tool override applies.
	// Empty means unclassified; mission runs must not invoke this skill until
	// the user classifies it. Prompt-only skills typically default to read.
	DefaultSideEffect SideEffect `json:"default_side_effect,omitempty"`
	// ToolOverrides maps tool names exposed by the skill to a SideEffect
	// classification, overriding DefaultSideEffect for that tool.
	ToolOverrides map[string]SideEffect `json:"tool_overrides,omitempty"`
	CreatedAt     time.Time             `json:"created_at,omitempty"`
	UpdatedAt     time.Time             `json:"updated_at,omitempty"`
}

// AgentSkillAccess narrows which workspace skill bindings an agent instance
// may use. When no access entry exists for an instance, all enabled bindings are allowed.
type AgentSkillAccess struct {
	AgentInstanceID   string    `json:"agent_instance_id"`
	EnabledBindingIDs []string  `json:"enabled_binding_ids,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

// DirectoryReference represents a reference to a filesystem directory for reading files
type DirectoryReference struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"` // Display name for the directory
	Path        string    `json:"path"` // Absolute filesystem path to the directory
	X           float64   `json:"x"`    // Canvas position X
	Y           float64   `json:"y"`    // Canvas position Y
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FileInfo represents information about a file in a directory
type FileInfo struct {
	ID           string     `json:"id,omitempty"`            // Stable item ID when available
	AttachmentID string     `json:"attachment_id,omitempty"` // Attachment ID for workspace-owned files
	FolderID     string     `json:"folder_id,omitempty"`     // Managed folder ID for workspace-owned folders
	Source       string     `json:"source,omitempty"`        // Source namespace, e.g. workspace or linked_directory
	Name         string     `json:"name"`                    // File name
	RelativePath string     `json:"relative_path"`           // Path relative to directory root
	URL          string     `json:"url,omitempty"`           // Workspace file URL when available
	Size         int64      `json:"size"`                    // File size in bytes
	IsDir        bool       `json:"is_dir"`                  // True if this is a directory
	ModTime      time.Time  `json:"mod_time"`                // Last modification time
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`    // Soft-delete timestamp when the item is trashed
	Status       string     `json:"status,omitempty"`        // e.g. "missing" when an attachment's file is absent on disk
	// Workspace reference: set when this directory entry is itself a registered
	// workspace folder (it contains a workspace.json with a known id). Lets
	// linked folders surface nested workspaces as openable references.
	IsWorkspace   bool   `json:"is_workspace,omitempty"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceSlug string `json:"workspace_slug,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
}

// CreateWorkspaceParams contains parameters for creating a new workspace
type CreateWorkspaceParams struct {
	Name        string
	Description string
	Agents      []string
	InitialData map[string]any
}

// AgentStats holds statistics for a single agent
type AgentStats struct {
	Name            string    `json:"name"`
	Status          string    `json:"status"` // "idle", "active", "busy", "error", "queued", "waiting"
	CurrentTasks    []string  `json:"current_tasks"`
	QueuedTasks     []string  `json:"queued_tasks"`
	CompletedTasks  int       `json:"completed_tasks"`
	FailedTasks     int       `json:"failed_tasks"`
	TotalExecutions int       `json:"total_executions"`
	LastActive      time.Time `json:"last_active,omitempty"`
}

// Progress represents overall workspace progress metrics
type Progress struct {
	TotalTasks      int       `json:"total_tasks"`
	CompletedTasks  int       `json:"completed_tasks"`
	InProgressTasks int       `json:"in_progress_tasks"`
	PendingTasks    int       `json:"pending_tasks"`
	FailedTasks     int       `json:"failed_tasks"`
	Percentage      int       `json:"percentage"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	EstimatedEnd    time.Time `json:"estimated_end,omitempty"`
	ElapsedTimeMs   float64   `json:"elapsed_time_ms,omitempty"`
	RemainingTimeMs float64   `json:"remaining_time_ms,omitempty"`
	ActiveAgents    int       `json:"active_agents"`
	IdleAgents      int       `json:"idle_agents"`
	TotalAgents     int       `json:"total_agents"`
	AverageTaskTime float64   `json:"average_task_time_ms,omitempty"`
}
