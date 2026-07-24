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
	ErrAgentMissing         ErrorCode = "agent_missing"
	ErrAgentAmbiguous       ErrorCode = "agent_ambiguous"
	ErrScheduleInvalid      ErrorCode = "schedule_invalid"
	ErrSchedulerUnsupported ErrorCode = "scheduler_unsupported"
	ErrStateCorrupt         ErrorCode = "state_corrupt"
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
	ID              string        `json:"id"`
	FeaturePath     string        `json:"feature_path"`
	Role            string        `json:"role"`
	AgentName       string        `json:"agent_name"`
	AgentKind       string        `json:"agent_kind"`
	WorkspaceID     string        `json:"workspace_id"`
	PaneID          string        `json:"pane_id"`
	TerminalID      string        `json:"terminal_id"`
	NativeSession   NativeSession `json:"native_session"`
	DueAt           time.Time     `json:"due_at"`
	RetryUntil      time.Time     `json:"retry_until"`
	Timezone        string        `json:"timezone"`
	Prompt          string        `json:"prompt"`
	State           ScheduleState `json:"state"`
	Attempts        int           `json:"attempts"`
	LastCheckedAt   time.Time     `json:"last_checked_at,omitempty"`
	LastAttemptAt   time.Time     `json:"last_attempt_at,omitempty"`
	DeliveredAt     time.Time     `json:"delivered_at,omitempty"`
	CanceledAt      time.Time     `json:"canceled_at,omitempty"`
	FailureReason   string        `json:"failure_reason,omitempty"`
	RecoveryCommand string        `json:"recovery_command,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
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
}

type FeatureState struct {
	Feature         Feature              `json:"feature"`
	WorkspaceID     string               `json:"workspace_id,omitempty"`
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
	UpdatedAt         time.Time    `json:"updated_at,omitempty"`
}

type HandoffStage string

const (
	HandoffRecorded        HandoffStage = "recorded"
	HandoffWorkspaceOpened HandoffStage = "workspace_opened"
	HandoffPrimaryStarted  HandoffStage = "primary_started"
	HandoffReady           HandoffStage = "ready"
	HandoffPrompted        HandoffStage = "prompted"
)

func NewBridgeState() BridgeState {
	return BridgeState{
		Version:  StateVersion,
		Features: make(map[string]FeatureState),
	}
}
