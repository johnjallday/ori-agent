// Package runtimecapability resolves a workspace's persisted blueprint runtime
// contract through a compiled adapter registry. Blueprint data selects only an
// allowlisted adapter ID; every probe, action, path, endpoint, and verification
// protocol remains compiled server behavior.
package runtimecapability

import (
	"context"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	ErrNoRuntimeContract         = errors.New("workspace has no runtime requirements")
	ErrUnsupportedSnapshot       = errors.New("unsupported runtime requirements snapshot")
	ErrUnknownMode               = errors.New("unknown runtime operating mode")
	ErrModeRequired              = errors.New("runtime operating mode selection required")
	ErrUnknownRequirement        = errors.New("unknown runtime requirement")
	ErrUnknownAction             = errors.New("unknown runtime requirement action")
	ErrUnknownAdapter            = errors.New("unknown runtime adapter")
	ErrVerificationFailed        = errors.New("runtime verification failed")
	ErrGrantNotAllowed           = errors.New("runtime capability grant is not allowed")
	ErrAgentNotSupported         = errors.New("runtime capability agent is not supported")
	ErrExecutionScopeUnavailable = errors.New("runtime capability execution scope is unavailable")
)

// Durable setup states are intentionally separate from live availability.
// Aliasing the workspace vocabulary keeps API projection and persistence from
// inventing subtly different meanings for the same state.
const (
	DurableNotStarted     = workspace.RuntimeConfigurationNotStarted
	DurableInProgress     = workspace.RuntimeConfigurationInProgress
	DurableConfigured     = workspace.RuntimeConfigurationConfigured
	DurableNeedsAttention = workspace.RuntimeConfigurationNeedsAttention
)

// Live availability states are fresh, non-persisted observations.
const (
	LiveNotApplicable = "not_applicable"
	LiveNotChecked    = "not_checked"
	LiveAvailable     = "available"
	LiveOffline       = "offline"
	LiveWrongTarget   = "wrong_target"
	LiveUnavailable   = "unavailable"
	LiveCheckFailed   = "check_failed"
)

// Safe generic reason codes used when the service, rather than an adapter,
// classifies a failure.
const (
	ReasonModeSelectionRequired  = "mode_selection_required"
	ReasonAdapterUnavailable     = "adapter_unavailable"
	ReasonCheckFailed            = "check_failed"
	ReasonVerificationRequired   = "verification_required"
	ReasonUnsupportedSnapshot    = "unsupported_snapshot"
	ReasonModeNotEnabled         = "runtime_mode_not_enabled"
	ReasonRequirementUnsupported = "runtime_requirement_unsupported"
	ReasonTaskAgentRequired      = "runtime_task_agent_required"
	ReasonTaskGrantRequired      = "runtime_task_grant_required"
)

// Action is one exact user-facing repair projected by an adapter. Token is a
// short opaque value the browser may echo only to the confirmed-action
// endpoint. It is not an adapter name, path, URL target, or request payload.
type Action struct {
	Token string `json:"token"`
	Code  string `json:"code"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

// EvaluationRequest is everything compiled adapter code receives for a
// read-only durable/live evaluation. Every field comes from the canonical
// workspace snapshot and durable state. No browser request can supply an
// adapter, path, endpoint, command, or probe input.
type EvaluationRequest struct {
	WorkspaceID string
	Mode        workspace.RuntimeOperatingMode
	Requirement workspace.RuntimeRequirement
	Persisted   workspace.RuntimeRequirementState
}

// ConfirmedActionRequest is passed only after the service has freshly
// evaluated the requirement and matched ActionToken to the exact action it
// currently offers.
type ConfirmedActionRequest struct {
	EvaluationRequest
	ActionToken string
}

// VerificationRequest has no caller-authored payload. The adapter owns the
// bounded harmless protocol and resolves every target from trusted state.
type VerificationRequest struct {
	EvaluationRequest
}

// DurableResult is a read-only adapter verdict about durable configuration.
// State is normalized by the service; an unknown value becomes in_progress,
// never configured. Summary, reason, and action are bounded before projection.
type DurableResult struct {
	State                string
	ReasonCode           string
	Summary              string
	Action               *Action
	VerificationRequired bool
}

// LiveResult is one fresh, transient observation. It is returned to the caller
// and never persisted as authority.
type LiveResult struct {
	State      string
	ReasonCode string
	Summary    string
	Action     *Action
}

// VerificationResult is an adapter's bounded outcome from its trusted explicit
// verification method. The service re-evaluates durable state before recording
// success; Succeeded alone can never mark a workspace configured.
type VerificationResult struct {
	Succeeded  bool
	ReasonCode string
	Summary    string
	Action     *Action
}

// Adapter is the required compiled runtime behavior. EvaluateDurable must be
// read-only. Live checks, confirmed repairs, and active verification are
// separate optional capabilities below, so a durable-only adapter cannot be
// invoked accidentally as if it supported a live or mutating operation.
type Adapter interface {
	ID() string
	EvaluateDurable(context.Context, EvaluationRequest) (DurableResult, error)
}

// LiveChecker is the optional read-only current-availability probe.
type LiveChecker interface {
	Adapter
	CheckLive(context.Context, EvaluationRequest) (LiveResult, error)
}

// ActionConfirmer performs one freshly offered, explicitly confirmed repair.
type ActionConfirmer interface {
	Adapter
	ConfirmAction(context.Context, ConfirmedActionRequest) error
}

// GrantValidationRequest contains only canonical workspace declarations and a
// stable agent instance. Capability adapters resolve provider/root details from
// trusted injected services; no path or provider comes from the client.
type GrantValidationRequest struct {
	WorkspaceID string
	Mode        workspace.RuntimeOperatingMode
	Requirement workspace.RuntimeRequirement
	Agent       workspace.AgentInstance
}

// GrantAuthorizer is an optional compiled adapter policy for explicit grants.
type GrantAuthorizer interface {
	Adapter
	ValidateGrant(context.Context, GrantValidationRequest) error
}

type CapabilityNetworkPosture string

const (
	CapabilityNetworkDisabled CapabilityNetworkPosture = "disabled"
	// CapabilityNetworkLocal reaches loopback only through capability-owned
	// trusted MCP/helper operations; it never enables general shell networking.
	CapabilityNetworkLocal CapabilityNetworkPosture = "capability_local"
)

type ExecutionScopeRequest struct {
	WorkspaceID string
	Mode        workspace.RuntimeOperatingMode
	Requirement workspace.RuntimeRequirement
	Agent       workspace.AgentInstance
}

type CapabilityExecutionScope struct {
	AdditionalWritableRoots []string
	NetworkPosture          CapabilityNetworkPosture
	AllowedMCPServers       []string
}

// ExecutionScopeProvider revalidates trusted capability-owned roots and local
// helper posture immediately before provider invocation.
type ExecutionScopeProvider interface {
	Adapter
	ResolveExecutionScope(context.Context, ExecutionScopeRequest) (CapabilityExecutionScope, error)
}

// Verifier performs an adapter-owned bounded verification protocol after an
// explicit user request.
type Verifier interface {
	Adapter
	Verify(context.Context, VerificationRequest) (VerificationResult, error)
}

// ModeStatus is safe public metadata from the persisted contract.
type ModeStatus struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Selected    bool   `json:"selected,omitempty"`
}

// RequirementStatus combines inert declaration metadata with the current safe
// server verdict. Adapter IDs and local implementation details are deliberately
// omitted from this workspace status surface.
type RequirementStatus struct {
	Key                string     `json:"key"`
	Label              string     `json:"label"`
	Description        string     `json:"description"`
	Disclosure         string     `json:"disclosure,omitempty"`
	DurableState       string     `json:"durable_state"`
	LiveState          string     `json:"live_state"`
	ReasonCode         string     `json:"reason_code,omitempty"`
	Summary            string     `json:"summary,omitempty"`
	Action             *Action    `json:"action,omitempty"`
	FirstVerifiedAt    *time.Time `json:"first_verified_at,omitempty"`
	LastVerifiedAt     *time.Time `json:"last_verified_at,omitempty"`
	VerificationNeeded bool       `json:"verification_needed,omitempty"`
}

// Blocker is the first requirement the user can act on, in mode declaration
// order. Only one action is projected.
type Blocker struct {
	RequirementKey string  `json:"requirement_key,omitempty"`
	ReasonCode     string  `json:"reason_code"`
	Summary        string  `json:"summary"`
	Action         *Action `json:"action,omitempty"`
}

// Status is the single normalized workspace runtime status shared by setup,
// workspace surfaces, and task preflight.
type Status struct {
	WorkspaceID           string              `json:"workspace_id"`
	Applicable            bool                `json:"applicable"`
	ContractVersion       int                 `json:"contract_version,omitempty"`
	Modes                 []ModeStatus        `json:"modes,omitempty"`
	SelectedModeID        string              `json:"selected_mode_id,omitempty"`
	ModeSelectionRequired bool                `json:"mode_selection_required,omitempty"`
	DurableState          string              `json:"durable_state"`
	LiveState             string              `json:"live_state"`
	Requirements          []RequirementStatus `json:"requirements,omitempty"`
	FirstBlocker          *Blocker            `json:"first_blocker,omitempty"`
	FirstVerifiedAt       *time.Time          `json:"first_verified_at,omitempty"`
	LastVerifiedAt        *time.Time          `json:"last_verified_at,omitempty"`
}
