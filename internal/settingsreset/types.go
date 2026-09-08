// Package settingsreset defines the bounded Settings reset contract shared by
// HTTP handlers and the pre-start host apply boundary. It is not a general job
// or restart service. These types alone perform no mutations.
package settingsreset

import "time"

const SchemaVersion = 1

type Intent string

const (
	IntentSelectedData   Intent = "selected_data"
	IntentStartFresh     Intent = "start_fresh"
	IntentReplaySetup    Intent = "replay_setup"
	IntentGettingStarted Intent = "getting_started"
)

type CategoryID string

const (
	CategorySettings         CategoryID = "settings"
	CategoryAgents           CategoryID = "agents"
	CategoryAppRecords       CategoryID = "app_records"
	CategorySetupSteps       CategoryID = "setup_steps"
	CategoryIdentityProgress CategoryID = "identity_progress"
	CategoryAppConfiguration CategoryID = "app_configuration"
	CategoryIntegrations     CategoryID = "integrations"
	CategoryTemplates        CategoryID = "templates"
	CategoryActivity         CategoryID = "activity"
	CategoryRuntimeCache     CategoryID = "runtime_cache"
)

// CountFact uses null for unavailable counts. Zero means a successful count of
// zero records, not an unreadable store. No secret values belong in facts.
type CountFact struct {
	Name              string `json:"name"`
	Count             *int64 `json:"count"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// Location is display-only evidence. Execution never accepts a client-supplied
// Location or derives a deletion path from a serialized display value.
type Location struct {
	DisplayPath string `json:"display_path"`
	Reason      string `json:"reason"`
}

type Blocker struct {
	Code     string     `json:"code"`
	Category CategoryID `json:"category,omitempty"`
	Message  string     `json:"message"`
	Recovery string     `json:"recovery"`
}

type CategoryPreview struct {
	ID          CategoryID  `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Facts       []CountFact `json:"facts"`
	Removed     []Location  `json:"removed"`
	Retained    []Location  `json:"retained"`
}

type RestartMode string

const (
	RestartNone            RestartMode = "none"
	RestartProcessRelaunch RestartMode = "process_relaunch"
)

// No managed-restart mode is exposed until a host can prove safe teardown.
// In particular, a menubar server stop/start is not a process relaunch.
type RestartInfo struct {
	Mode         RestartMode `json:"mode"`
	Instructions string      `json:"instructions,omitempty"`
}

type Preview struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Allocated before submission so response loss can be recovered with GET.
	OperationID      string            `json:"operation_id"`
	Intent           Intent            `json:"intent"`
	Selected         []CategoryID      `json:"selected"`
	Dependencies     []CategoryID      `json:"dependencies"`
	Categories       []CategoryPreview `json:"categories"`
	Blockers         []Blocker         `json:"blockers"`
	ScopeDigest      string            `json:"scope_digest"`
	ExpiresAt        time.Time         `json:"expires_at"`
	Restart          RestartInfo       `json:"restart"`
	RetryOperationID string            `json:"retry_operation_id,omitempty"`
}

// ExecuteRequest references a server-held, reviewed plan. Neither categories,
// intent nor filesystem targets can be broadened at execution time. HTTP must
// reject unknown fields and retain its CSRF, size and exact RESET checks.
type ExecuteRequest struct {
	PreviewID    string `json:"preview_id"`
	RequestID    string `json:"request_id"`
	Confirmation string `json:"confirmation"`
}

type OperationState string

const (
	StatePreparing       OperationState = "preparing"
	StateAwaitingRestart OperationState = "awaiting_restart"
	StateApplying        OperationState = "applying"
	StateVerifying       OperationState = "verifying"
	StateCompleted       OperationState = "completed"
	StatePartialFailure  OperationState = "partial_failure"
	StateBlocked         OperationState = "blocked"
	StateInterrupted     OperationState = "interrupted"
)

type Outcome string

const (
	OutcomePending   Outcome = "pending"
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeSkipped   Outcome = "skipped"
	OutcomePreserved Outcome = "preserved"
	OutcomeUnknown   Outcome = "unknown"
)

// CheckResult is an authoritative named postcondition, not a generic health
// probe. Unknown checks retain OutcomeUnknown rather than claiming success.
type CheckResult struct {
	Name    string  `json:"name"`
	Outcome Outcome `json:"outcome"`
	Message string  `json:"message,omitempty"`
}

type CategoryResult struct {
	ID        CategoryID    `json:"id"`
	Outcome   Outcome       `json:"outcome"`
	Message   string        `json:"message,omitempty"`
	Retryable bool          `json:"retryable"`
	Checks    []CheckResult `json:"checks"`
	Retained  []Location    `json:"retained"`
}

// Operation is the public result, not the private apply journal. Private
// process identity, resolved targets and confirmation tokens never come back
// as executable input. Revision changes let stale tabs detect an update.
type Operation struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	Intent        Intent           `json:"intent"`
	State         OperationState   `json:"state"`
	Revision      uint64           `json:"revision"`
	Results       []CategoryResult `json:"results"`
	Blockers      []Blocker        `json:"blockers"`
	Restart       RestartInfo      `json:"restart"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}
