// Package model defines the durable, bridge-owned records shared by the
// command-line helper and its Herdr plugin entrypoints.
package model

import "time"

const StateVersion = 1

// ErrorCode is a stable machine-readable reason for an operation failure.
// Human diagnostics always include a recovery command separately.
type ErrorCode string

const (
	ErrConfigInvalid        ErrorCode = "config_invalid"
	ErrDisabled             ErrorCode = "bridge_disabled"
	ErrHerdrMissing         ErrorCode = "herdr_missing"
	ErrHerdrIncompatible    ErrorCode = "herdr_incompatible"
	ErrHerdrUnavailable     ErrorCode = "herdr_unavailable"
	ErrHerdrPermission      ErrorCode = "herdr_permission_denied"
	ErrSchemaUnsupported    ErrorCode = "schema_unsupported"
	ErrPluginUnavailable    ErrorCode = "plugin_unavailable"
	ErrAgentUnavailable     ErrorCode = "agent_executable_unavailable"
	ErrWorktreeInvalid      ErrorCode = "worktree_invalid"
	ErrNoFocusedWorkspace   ErrorCode = "no_focused_workspace"
	ErrAgentMissing         ErrorCode = "agent_missing"
	ErrAgentAmbiguous       ErrorCode = "agent_ambiguous"
	ErrScheduleInvalid      ErrorCode = "schedule_invalid"
	ErrSchedulerUnsupported ErrorCode = "scheduler_unsupported"
	ErrWakeUnavailable      ErrorCode = "wake_unavailable"
	ErrStateCorrupt         ErrorCode = "state_corrupt"
	// ErrGitHubUnavailable covers every `gh issue view` failure: missing CLI,
	// unauthenticated, network, rate limit, or an Issue GitHub will not show
	// this credential. The sanitized detail names which one.
	ErrGitHubUnavailable ErrorCode = "github_unavailable"
	// ErrIssueIneligible means the fetched Issue was read successfully but does
	// not currently qualify for planning: closed, not Ready, or missing/duplicate
	// size label.
	ErrIssueIneligible ErrorCode = "issue_ineligible"
)

// StageError adds an operation stage and an operator-safe recovery command to
// failures returned to the shell. It intentionally contains no prompt text,
// environment values, or terminal output.
type StageError struct {
	Stage    string    `json:"stage"`
	Code     ErrorCode `json:"code"`
	Message  string    `json:"message"`
	Recovery string    `json:"recovery,omitempty"`
	Cause    error     `json:"-"`
}

func (e *StageError) Error() string {
	if e == nil {
		return ""
	}
	return e.Stage + ": " + e.Message
}

func (e *StageError) Unwrap() error { return e.Cause }

// AgentStatus mirrors Herdr's semantic lifecycle values. Bridge-derived task
// progress must never be written back as an AgentStatus.
type AgentStatus string

const (
	AgentIdle    AgentStatus = "idle"
	AgentWorking AgentStatus = "working"
	AgentBlocked AgentStatus = "blocked"
	AgentDone    AgentStatus = "done"
	AgentUnknown AgentStatus = "unknown"
	AgentMissing AgentStatus = "missing"
)

// Feature identifies one Git worktree that is managed by this bridge.
type Feature struct {
	RepositoryID string `json:"repository_id"`
	Name         string `json:"name"`
	Branch       string `json:"branch"`
	Path         string `json:"path"`
}

// NativeSession is the strongest native agent-session identity Herdr exposes.
// Pane and terminal IDs remain useful runtime fallbacks but are not treated as
// a replacement conversation identity.
type NativeSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

// RoleAgent is a feature-scoped agent association owned by the bridge.
type RoleAgent struct {
	Role          string        `json:"role"`
	Name          string        `json:"name"`
	Kind          string        `json:"kind"`
	WorkspaceID   string        `json:"workspace_id"`
	TabID         string        `json:"tab_id"`
	PaneID        string        `json:"pane_id"`
	TerminalID    string        `json:"terminal_id"`
	NativeSession NativeSession `json:"native_session,omitempty"`
	Status        AgentStatus   `json:"status"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// PromptDelivery records submission state without storing user prompt content
// in command diagnostics.
type PromptDelivery struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Schedule records a one-time continuation. Prompt text stays in user-local
// state and must never be rendered by status or diagnostics.
type Schedule struct {
	ID                string        `json:"id"`
	FeaturePath       string        `json:"feature_path"`
	Role              string        `json:"role"`
	AgentName         string        `json:"agent_name"`
	AgentKind         string        `json:"agent_kind"`
	WorkspaceID       string        `json:"workspace_id"`
	PaneID            string        `json:"pane_id"`
	TerminalID        string        `json:"terminal_id"`
	NativeSession     NativeSession `json:"native_session"`
	DueAt             time.Time     `json:"due_at"`
	RetryUntil        time.Time     `json:"retry_until"`
	Timezone          string        `json:"timezone"`
	Prompt            string        `json:"prompt"`
	WakeRequired      bool          `json:"wake_required,omitempty"`
	WakeCandidateID   string        `json:"wake_candidate_id,omitempty"`
	WakeSource        string        `json:"wake_source,omitempty"`
	WakePurpose       string        `json:"wake_purpose,omitempty"`
	WakeRequestedAt   time.Time     `json:"wake_requested_at,omitempty"`
	WakeRegisteredAt  time.Time     `json:"wake_registered_at,omitempty"`
	WakeProgrammedAt  time.Time     `json:"wake_programmed_at,omitempty"`
	WakeVerifiedAt    time.Time     `json:"wake_verified_at,omitempty"`
	WakeProtocol      int           `json:"wake_protocol_version,omitempty"`
	WakeDaemonBuild   string        `json:"wake_daemon_build,omitempty"`
	WakeHelperBuild   string        `json:"wake_helper_build,omitempty"`
	WakeResult        string        `json:"wake_result,omitempty"`
	WakeCode          string        `json:"wake_code,omitempty"`
	WakeUncertain     bool          `json:"wake_uncertain,omitempty"`
	WakeRollbackAt    time.Time     `json:"wake_rollback_attempted_at,omitempty"`
	WakeRollbackOKAt  time.Time     `json:"wake_rollback_verified_at,omitempty"`
	WakeRollbackState string        `json:"wake_rollback_result,omitempty"`
	WakeRollbackInfo  string        `json:"wake_rollback_detail,omitempty"`
	WakeWithdrawnAt   time.Time     `json:"wake_withdrawn_at,omitempty"`
	WakeFailureReason string        `json:"wake_failure_reason,omitempty"`
	State             ScheduleState `json:"state"`
	Attempts          int           `json:"attempts"`
	LastCheckedAt     time.Time     `json:"last_checked_at,omitempty"`
	LastAttemptAt     time.Time     `json:"last_attempt_at,omitempty"`
	DeliveredAt       time.Time     `json:"delivered_at,omitempty"`
	CanceledAt        time.Time     `json:"canceled_at,omitempty"`
	FailureReason     string        `json:"failure_reason,omitempty"`
	RecoveryCommand   string        `json:"recovery_command,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// ScheduleState is intentionally richer than a boolean so users can see
// whether a continuation is waiting safely, was delivered, or needs review.
type ScheduleState string

const (
	SchedulePending    ScheduleState = "pending"
	ScheduleWaiting    ScheduleState = "waiting"
	ScheduleDelivering ScheduleState = "delivering"
	ScheduleDelivered  ScheduleState = "delivered"
	ScheduleFailed     ScheduleState = "failed"
	ScheduleUncertain  ScheduleState = "uncertain"
	ScheduleCanceled   ScheduleState = "canceled"
)

func (s ScheduleState) IsUnresolved() bool {
	return s == SchedulePending || s == ScheduleWaiting || s == ScheduleDelivering || s == ScheduleUncertain
}

// BridgeState is the versioned state-file envelope. It lives in a user-local
// runtime directory, never inside a Git checkout.
type BridgeState struct {
	Version  int                     `json:"version"`
	Features map[string]FeatureState `json:"features"`
	// Runs are Overnight Runs keyed by run ID. The field is additive on
	// purpose: a state file written before Overnight Runs existed simply has no
	// key here, and must keep loading rather than being migrated or rejected.
	Runs map[string]OvernightRun `json:"runs,omitempty"`
	// PlanningSessions are issue-scoped Pi planning sessions, keyed by
	// "<repository_id>:<issue_number>". This is deliberately its own map and
	// never a FeatureState: a planning session has no PRD-driven feature
	// identity yet, and code that walks Features to find roles, continuation
	// targets, Overnight participants, or wt done candidates must never
	// stumble onto a planner by accident. The field is additive, exactly like
	// Runs: a state file written before this feature existed has no key here
	// and keeps loading unmodified.
	PlanningSessions map[string]PlanningSession `json:"planning_sessions,omitempty"`
}

type FeatureState struct {
	Feature     Feature `json:"feature"`
	WorkspaceID string  `json:"workspace_id,omitempty"`
	// TabID is the feature's own tab inside a shared workspace. It is absent on
	// records written before tab-backed handoff, and that absence is meaningful:
	// cleanup treats such a feature as workspace-backed and refuses to close
	// anything on its behalf rather than closing the whole workspace.
	TabID           string               `json:"tab_id,omitempty"`
	SourceID        string               `json:"source_id,omitempty"`
	MetadataEnabled *bool                `json:"metadata_enabled,omitempty"`
	Agents          map[string]RoleAgent `json:"agents,omitempty"`
	Schedules       map[string]Schedule  `json:"schedules,omitempty"`
	Handoff         HandoffState         `json:"handoff,omitempty"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// HandoffState is persisted before the bridge changes Herdr. Each completed
// stage is written atomically so retry can resume only missing work and never
// resend a confirmed bootstrap prompt without an explicit later override.
type HandoffState struct {
	Stage             HandoffStage `json:"stage,omitempty"`
	RootPaneID        string       `json:"root_pane_id,omitempty"`
	PrimaryRole       string       `json:"primary_role,omitempty"`
	PrimaryKind       string       `json:"primary_kind,omitempty"`
	PrimaryAgentName  string       `json:"primary_agent_name,omitempty"`
	BootstrapPrompted bool         `json:"bootstrap_prompted,omitempty"`
	// SkipBootstrapPrompt marks a feature that has no PRD and no checklist to
	// be pointed at. The decision is persisted rather than passed per call so a
	// later `wt herd retry` cannot deliver a prompt describing planning
	// documents that were never going to exist.
	SkipBootstrapPrompt bool      `json:"skip_bootstrap_prompt,omitempty"`
	UpdatedAt           time.Time `json:"updated_at,omitempty"`
}

type HandoffStage string

const (
	HandoffRecorded HandoffStage = "recorded"
	// HandoffTabCreated is the placement stage for tab-backed features.
	HandoffTabCreated HandoffStage = "tab_created"
	// HandoffWorkspaceOpened is the pre-tab placement stage. Nothing writes it
	// any more, but records persisted by earlier versions still carry it and
	// must keep loading and rendering without error.
	HandoffWorkspaceOpened HandoffStage = "workspace_opened"
	HandoffPrimaryStarted  HandoffStage = "primary_started"
	HandoffReady           HandoffStage = "ready"
	HandoffPrompted        HandoffStage = "prompted"
)

func NewBridgeState() BridgeState {
	return BridgeState{
		Version:          StateVersion,
		Features:         make(map[string]FeatureState),
		Runs:             make(map[string]OvernightRun),
		PlanningSessions: make(map[string]PlanningSession),
	}
}

// PlanningStage tracks how far one issue-scoped planning session has
// progressed placing and prompting its Pi planner. It mirrors
// HandoffStage's shape but is its own type: a planning session is not a
// feature handoff, and the two must never be assignable to one another by
// accident.
type PlanningStage string

const (
	PlanningRecorded     PlanningStage = "recorded"
	PlanningTabCreated   PlanningStage = "tab_created"
	PlanningAgentStarted PlanningStage = "agent_started"
	PlanningReady        PlanningStage = "ready"
	PlanningPrompted     PlanningStage = "prompted"
)

// PlanningSession is one issue-scoped Pi planning session in
// ori-agent-dev. It is deliberately not a Feature or FeatureState: a
// planning session has no PRD-backed feature role, is never selectable for
// Overnight Runs, continuations, PR delivery, or `wt done`, and generic code
// that iterates BridgeState.Features must never observe one.
type PlanningSession struct {
	RepositoryID string `json:"repository_id"`
	// IssueNumber preserves records and consumers from the original
	// single-Issue planner. For a bundle it is the first canonical member.
	IssueNumber int `json:"issue_number"`
	// IssueNumbers is additive bundle identity. Legacy records omit it and are
	// interpreted as the one-member set containing IssueNumber.
	IssueNumbers []int  `json:"issue_numbers,omitempty"`
	Slug         string `json:"slug"`
	// WorktreePath is the canonical ori-agent-dev checkout this session plans
	// in. It is recorded so a later invocation can detect a mismatched dev
	// worktree instead of silently placing a second planner in the wrong tree.
	WorktreePath string        `json:"worktree_path"`
	TabID        string        `json:"tab_id,omitempty"`
	RootPaneID   string        `json:"root_pane_id,omitempty"`
	Planner      RoleAgent     `json:"planner,omitempty"`
	Stage        PlanningStage `json:"stage,omitempty"`
	Prompted     bool          `json:"prompted,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// MemberIssueNumbers returns a defensive copy of the session's canonical
// membership while preserving state files written before IssueNumbers existed.
func (s PlanningSession) MemberIssueNumbers() []int {
	if len(s.IssueNumbers) > 0 {
		return append([]int(nil), s.IssueNumbers...)
	}
	if s.IssueNumber > 0 {
		return []int{s.IssueNumber}
	}
	return nil
}
