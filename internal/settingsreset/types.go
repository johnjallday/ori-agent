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
	CategoryInstalledPlugins CategoryID = "installed_plugins"
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

// CategoryItem names one member of a category's reviewed scope, such as a single
// installed plugin. Every string is bounded and sanitized by the owner that
// produced it; none is executable, a filesystem target, or raw plugin markup.
// The field is additive: a category without members omits it entirely, so
// receipts written before items existed stay byte-identical.
type CategoryItem struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Details []string `json:"details,omitempty"`
}

type CategoryPreview struct {
	ID          CategoryID     `json:"id"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Facts       []CountFact    `json:"facts"`
	Items       []CategoryItem `json:"items,omitempty"`
	Removed     []Location     `json:"removed"`
	Retained    []Location     `json:"retained"`
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
	// AgentsFolder is present when the reset removes the user's agents from
	// their Workspace Directory. That folder is visible to the user and may be
	// synced to other machines, so it needs its own confirmation.
	AgentsFolder *AgentsFolderReview `json:"agents_folder,omitempty"`
}

// AgentsFolderReview describes <root>/Agents in a reset preview. Executing the
// reset requires ExecuteRequest.ConfirmAgentsFolder to repeat Path exactly.
type AgentsFolderReview struct {
	Path                 string `json:"path"`
	Notice               string `json:"notice"`
	ConfirmationRequired bool   `json:"confirmation_required"`
	// AlsoRemoved lists what Start Fresh removes beside the agents folder
	// under the same confirmation: the Skills folder and the plugin list.
	AlsoRemoved []string `json:"also_removed,omitempty"`
}

// AgentsFolderNotice is the warning shown with the agents folder in a preview.
const AgentsFolderNotice = "This folder is in your Workspace Directory, where you can see it, and it may be synced to your other machines. Resetting removes each agent's folder from it — on every machine it syncs to. Other files there are kept."

// AgentsFolderFreshNotice is the warning when Start Fresh also removes the
// Skills folder and the plugin list beside the agents folder.
const AgentsFolderFreshNotice = "These are in your Workspace Directory, where you can see them, and they may be synced to your other machines. Start Fresh removes each agent's folder, each skill in your Skills folder, and your plugin list (Plugins.json) — on every machine the Workspace Directory syncs to. Other files there are kept."

// ExecuteRequest references a server-held, reviewed plan. Neither categories,
// intent nor filesystem targets can be broadened at execution time. HTTP must
// reject unknown fields and retain its CSRF, size and exact RESET checks.
type ExecuteRequest struct {
	PreviewID    string `json:"preview_id"`
	RequestID    string `json:"request_id"`
	Confirmation string `json:"confirmation"`
	// ConfirmAgentsFolder is the second confirmation a reset that removes the
	// agents folder needs: the reviewed Preview.AgentsFolder.Path, repeated.
	ConfirmAgentsFolder string `json:"confirm_agents_folder,omitempty"`
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

// ItemResult reports one member of a category's scope. It exists so a partial
// failure can say which plugin is unresolved without replaying verified work or
// leaking a raw path or error string. Additive and omitted when unused.
type ItemResult struct {
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
	Items     []ItemResult  `json:"items,omitempty"`
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
