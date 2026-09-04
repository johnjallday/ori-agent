// Package setupjourney owns bounded durable progress for host-driven specialist
// setup journeys. It stores only structural state and canonical identifiers;
// canonical plugin, workspace, runtime, Assistant Program, and catalog owners
// remain authoritative for whether a step is complete.
package setupjourney

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

const (
	MaxRunSteps            = 16
	MaxStepStatesJSON      = 8 << 10
	MaxOperationResultJSON = 8 << 10
	MaxCanonicalIDBytes    = 128
	MaxIdempotencyKeyBytes = 128
	MaxOwnerRevisions      = 16
)

var (
	// ErrNotFound means the requested run or receipt does not exist.
	ErrNotFound = errors.New("setup journey record not found")
	// ErrConflict means a run compare-and-swap revision is stale or a unique
	// canonical scope is already represented by another run.
	ErrConflict = errors.New("setup journey state revision conflict")
	// ErrIdempotencyConflict means a key was reused for another normalized
	// action, step, input digest, or review digest.
	ErrIdempotencyConflict = errors.New("setup journey idempotency conflict")
	// ErrOperationBusy means an accepted operation still requires completion or
	// canonical reconciliation before another mutation may start.
	ErrOperationBusy = errors.New("setup journey operation is already in progress")
	// ErrInvalid means a caller supplied an unbounded or structurally invalid
	// persistence value.
	ErrInvalid = errors.New("setup journey value is invalid")
	// ErrMalformed means persisted authority/identity fields cannot be safely
	// interpreted. Malformed structural progress is instead returned as a
	// bounded needs-attention Run with NeedsNormalization set.
	ErrMalformed = errors.New("setup journey persisted record is malformed")
)

// RunKind separates the accepted relationship's root setup from each later
// independently resumable project setup.
type RunKind string

const (
	RunKindRoot  RunKind = "root"
	RunKindChild RunKind = "child"
)

// LifecycleState is derived by reconciliation; browser input never selects it.
type LifecycleState string

const (
	LifecycleNotStarted     LifecycleState = "not_started"
	LifecycleInProgress     LifecycleState = "in_progress"
	LifecycleReady          LifecycleState = "ready"
	LifecycleNeedsAttention LifecycleState = "needs_attention"
)

// StepStatus is the closed structural projection persisted for one declaration
// step. It is not evidence that a canonical consequence still exists.
type StepStatus string

const (
	StepPending         StepStatus = "pending"
	StepActive          StepStatus = "active"
	StepComplete        StepStatus = "complete"
	StepBlocked         StepStatus = "blocked"
	StepOptionalSkipped StepStatus = "optional_skipped"
)

// ReasonCode is safe, compiled guidance identity. No downstream error text is
// persisted in a setup journey row or receipt.
type ReasonCode string

const (
	ReasonDeclarationInvalid          ReasonCode = "declaration_invalid"
	ReasonJourneyUnavailable          ReasonCode = "journey_unavailable"
	ReasonRelationshipNotAccepted     ReasonCode = "relationship_not_accepted"
	ReasonRunNotFound                 ReasonCode = "run_not_found"
	ReasonRevisionConflict            ReasonCode = "revision_conflict"
	ReasonIdempotencyConflict         ReasonCode = "idempotency_conflict"
	ReasonStepNotCurrent              ReasonCode = "step_not_current"
	ReasonActionUnavailable           ReasonCode = "action_unavailable"
	ReasonInputInvalid                ReasonCode = "input_invalid"
	ReasonReviewRequired              ReasonCode = "review_required"
	ReasonReviewStale                 ReasonCode = "review_stale"
	ReasonOwnerUnavailable            ReasonCode = "owner_unavailable"
	ReasonOperationFailed             ReasonCode = "operation_failed"
	ReasonIntegrationNotInstalled     ReasonCode = "integration_not_installed"
	ReasonIntegrationDisabled         ReasonCode = "integration_disabled"
	ReasonIntegrationUpdateRequired   ReasonCode = "integration_update_required"
	ReasonIntegrationIdentityMismatch ReasonCode = "integration_identity_mismatch"
	ReasonIntegrationUnsupported      ReasonCode = "integration_unsupported"
	ReasonBlueprintUnavailable        ReasonCode = "blueprint_unavailable"
	ReasonAssistantProgramMismatch    ReasonCode = "assistant_program_mismatch"
	ReasonProjectSelectionRequired    ReasonCode = "project_selection_required"
	ReasonProjectScopeInvalid         ReasonCode = "project_scope_invalid"
	ReasonProjectAlreadyConnected     ReasonCode = "project_already_connected"
	ReasonProjectUnavailable          ReasonCode = "project_unavailable"
	ReasonRuntimeSetupRequired        ReasonCode = "runtime_setup_required"
	ReasonRuntimeNeedsAttention       ReasonCode = "runtime_needs_attention"
	ReasonHomeUnavailable             ReasonCode = "home_unavailable"
	ReasonStaffingRequired            ReasonCode = "staffing_required"
	ReasonStaffingNeedsAttention      ReasonCode = "staffing_needs_attention"
)

var validReasonCodes = map[ReasonCode]struct{}{
	ReasonDeclarationInvalid: {}, ReasonJourneyUnavailable: {}, ReasonRelationshipNotAccepted: {},
	ReasonRunNotFound: {}, ReasonRevisionConflict: {}, ReasonIdempotencyConflict: {},
	ReasonStepNotCurrent: {}, ReasonActionUnavailable: {}, ReasonInputInvalid: {},
	ReasonReviewRequired: {}, ReasonReviewStale: {}, ReasonOwnerUnavailable: {},
	ReasonOperationFailed: {}, ReasonIntegrationNotInstalled: {}, ReasonIntegrationDisabled: {},
	ReasonIntegrationUpdateRequired: {}, ReasonIntegrationIdentityMismatch: {},
	ReasonIntegrationUnsupported: {}, ReasonBlueprintUnavailable: {}, ReasonAssistantProgramMismatch: {},
	ReasonProjectSelectionRequired: {}, ReasonProjectScopeInvalid: {}, ReasonProjectAlreadyConnected: {},
	ReasonProjectUnavailable: {}, ReasonRuntimeSetupRequired: {}, ReasonRuntimeNeedsAttention: {},
	ReasonHomeUnavailable: {}, ReasonStaffingRequired: {}, ReasonStaffingNeedsAttention: {},
}

// StepState is bounded ordered structural state for one declaration step.
type StepState struct {
	StepID     string     `json:"step_id"`
	Status     StepStatus `json:"status"`
	ReasonCode ReasonCode `json:"reason_code,omitempty"`
}

// Run is one independently revisioned root or child journey record.
type Run struct {
	ID                       string
	Kind                     RunKind
	RootRunID                string
	OwnerUserID              string
	RelationshipID           string
	SpecialistSlug           string
	JourneyID                string
	DeclarationSchemaVersion int
	DeclarationVersion       int
	StateRevision            int64
	Lifecycle                LifecycleState
	CurrentStepID            string
	StepStates               []StepState
	Dismissed                bool
	IntegrationPluginID      string
	IntegrationVersion       string
	HomeWorkspaceID          string
	ProjectWorkspaceID       string
	SelectedModeID           string
	FirstOpenedAt            *time.Time
	LastDismissedAt          *time.Time
	FirstCompletedAt         *time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time

	// NeedsNormalization is set only on read when persisted lifecycle or
	// structural progress is unknown/corrupt. The reconciler replaces that
	// projection using the current built-in declaration and canonical owners.
	NeedsNormalization bool
}

// RootSpec is the immutable accepted-relationship identity used by atomic
// root create-or-get. StepIDs come from the normalized built-in declaration.
type RootSpec struct {
	OwnerUserID              string
	RelationshipID           string
	SpecialistSlug           string
	JourneyID                string
	DeclarationSchemaVersion int
	DeclarationVersion       int
	StepIDs                  []string
}

// DeclarationMigrationReceipt proves that one exact compiled step mapping was
// CAS-applied without changing canonical resource receipts.
type DeclarationMigrationReceipt struct {
	RunKind                RunKind
	RunID                  string
	FromSchemaVersion      int
	FromDeclarationVersion int
	ToSchemaVersion        int
	ToDeclarationVersion   int
	StepMappingDigest      string
	RunRevisionBefore      int64
	RunRevisionAfter       int64
	CreatedAt              time.Time
}

// OperationStatus is the restart-safe state of one accepted mutation.
type OperationStatus string

const (
	OperationClaimed           OperationStatus = "claimed"
	OperationReconcileRequired OperationStatus = "reconcile_required"
	OperationSucceeded         OperationStatus = "succeeded"
	OperationFailed            OperationStatus = "failed"
)

// OperationClaim binds one idempotency key to exactly one normalized request.
type OperationClaim struct {
	RunKind        RunKind
	RunID          string
	IfRevision     int64
	IdempotencyKey string
	StepID         string
	ActionID       string
	InputDigest    string
	ReviewToken    string
	ReviewDigest   string
}

// ReviewReceipt is a bounded consent token. The disclosure itself is returned
// to the caller but never stored in journey persistence.
type ReviewReceipt struct {
	Token               string
	RunKind             RunKind
	RunID               string
	IdempotencyKey      string
	StepID              string
	ActionID            string
	InputDigest         string
	RunRevision         int64
	OwnerRevisionDigest string
	DisclosureDigest    string
	CreatedAt           time.Time
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
	ConsumedByKey       string
}

// ReviewReceiptSpec binds one review idempotency key to exact current owner and
// disclosure digests. TTL is bounded by the store.
type ReviewReceiptSpec struct {
	RunKind             RunKind
	RunID               string
	IdempotencyKey      string
	StepID              string
	ActionID            string
	InputDigest         string
	RunRevision         int64
	OwnerRevisionDigest string
	DisclosureDigest    string
	TTL                 time.Duration
}

// CanonicalOwner identifies one authoritative subsystem without storing its
// data or an arbitrary adapter name.
type CanonicalOwner string

const (
	OwnerPlugin           CanonicalOwner = "plugin"
	OwnerWorkspace        CanonicalOwner = "workspace"
	OwnerRuntimeSetup     CanonicalOwner = "runtime_setup"
	OwnerAssistantProgram CanonicalOwner = "assistant_program"
	OwnerTicket           CanonicalOwner = "ticket"
	OwnerSampleCatalog    CanonicalOwner = "sample_catalog"
)

var validCanonicalOwners = map[CanonicalOwner]struct{}{
	OwnerPlugin: {}, OwnerWorkspace: {}, OwnerRuntimeSetup: {},
	OwnerAssistantProgram: {}, OwnerTicket: {}, OwnerSampleCatalog: {},
}

// OwnerRevision is a bounded canonical read token included in a result receipt.
type OwnerRevision struct {
	Owner    CanonicalOwner `json:"owner"`
	Revision int64          `json:"revision"`
}

// CanonicalResult contains only resource IDs needed to resume/reconcile. It
// deliberately has no generic map, message, path, manifest, prompt, or error.
type CanonicalResult struct {
	ChildRunID          string          `json:"child_run_id,omitempty"`
	IntegrationPluginID string          `json:"integration_plugin_id,omitempty"`
	IntegrationVersion  string          `json:"integration_version,omitempty"`
	HomeWorkspaceID     string          `json:"home_workspace_id,omitempty"`
	ProjectWorkspaceID  string          `json:"project_workspace_id,omitempty"`
	SelectedModeID      string          `json:"selected_mode_id,omitempty"`
	CanonicalReceiptID  string          `json:"canonical_receipt_id,omitempty"`
	OwnerRevisions      []OwnerRevision `json:"owner_revisions,omitempty"`
}

// ResultCode is a closed receipt outcome. CanonicalResult carries the exact
// resource identity, so adapters do not need to invent free-form result text.
type ResultCode string

const (
	ResultApplied         ResultCode = "applied"
	ResultNotApplied      ResultCode = "not_applied"
	ResultAlreadyCurrent  ResultCode = "already_current"
	ResultOpened          ResultCode = "opened"
	ResultDismissed       ResultCode = "dismissed"
	ResultChildRunCreated ResultCode = "child_run_created"
	ResultReconciled      ResultCode = "reconciled"
)

var validResultCodes = map[ResultCode]struct{}{
	ResultApplied: {}, ResultNotApplied: {}, ResultAlreadyCurrent: {},
	ResultOpened: {}, ResultDismissed: {}, ResultChildRunCreated: {}, ResultReconciled: {},
}

// OperationReceipt is the durable replay result for one accepted mutation.
type OperationReceipt struct {
	RunKind           RunKind
	RunID             string
	IdempotencyKey    string
	StepID            string
	ActionID          string
	InputDigest       string
	ReviewDigest      string
	Status            OperationStatus
	ResultCode        ResultCode
	ReasonCode        ReasonCode
	Result            CanonicalResult
	RunRevisionBefore int64
	RunRevisionAfter  int64
	CreatedAt         time.Time
	CompletedAt       *time.Time
}

// OperationCompletion finalizes an accepted claim. ResultCode and ReasonCode
// are closed host-compiled values. Result is strictly typed and size checked.
type OperationCompletion struct {
	Status     OperationStatus
	ResultCode ResultCode
	ReasonCode ReasonCode
	Result     CanonicalResult
}

// Digest returns the lowercase SHA-256 digest expected by operation/review
// persistence. Callers digest normalized bounded input, never store it here.
func Digest(normalized []byte) string {
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

func (r *Run) Clone() *Run {
	if r == nil {
		return nil
	}
	clone := *r
	clone.StepStates = append([]StepState(nil), r.StepStates...)
	clone.FirstOpenedAt = cloneTime(r.FirstOpenedAt)
	clone.LastDismissedAt = cloneTime(r.LastDismissedAt)
	clone.FirstCompletedAt = cloneTime(r.FirstCompletedAt)
	return &clone
}

func (r *OperationReceipt) Clone() *OperationReceipt {
	if r == nil {
		return nil
	}
	clone := *r
	clone.Result.OwnerRevisions = append([]OwnerRevision(nil), r.Result.OwnerRevisions...)
	clone.CompletedAt = cloneTime(r.CompletedAt)
	return &clone
}

func (r *ReviewReceipt) Clone() *ReviewReceipt {
	if r == nil {
		return nil
	}
	clone := *r
	clone.ConsumedAt = cloneTime(r.ConsumedAt)
	return &clone
}

func cloneTime(source *time.Time) *time.Time {
	if source == nil {
		return nil
	}
	value := source.UTC()
	return &value
}

var (
	stableIDPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	canonicalRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@+:-]{0,127}$`)
	digestPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func validateStableID(value string) bool {
	return stableIDPattern.MatchString(value)
}

func validateCanonicalRef(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	return len(value) <= MaxCanonicalIDBytes && canonicalRefPattern.MatchString(value) &&
		!sensitive.ContainsSecretLikeText(value)
}

func validateDigest(value string, allowEmpty bool) bool {
	return (allowEmpty && value == "") || digestPattern.MatchString(value)
}

func validateReasonCode(code ReasonCode, allowEmpty bool) bool {
	if code == "" {
		return allowEmpty
	}
	_, ok := validReasonCodes[code]
	return ok
}

func validateResultCode(code ResultCode) bool {
	_, ok := validResultCodes[code]
	return ok
}

func normalizeStepIDs(stepIDs []string) ([]StepState, error) {
	if len(stepIDs) == 0 || len(stepIDs) > MaxRunSteps {
		return nil, ErrInvalid
	}
	seen := make(map[string]struct{}, len(stepIDs))
	states := make([]StepState, len(stepIDs))
	for index, raw := range stepIDs {
		stepID := strings.ToLower(strings.TrimSpace(raw))
		if !validateStableID(stepID) {
			return nil, ErrInvalid
		}
		if _, exists := seen[stepID]; exists {
			return nil, ErrInvalid
		}
		seen[stepID] = struct{}{}
		states[index] = StepState{StepID: stepID, Status: StepPending}
	}
	return states, nil
}

func normalizeStepStates(states []StepState) ([]StepState, error) {
	if len(states) == 0 || len(states) > MaxRunSteps {
		return nil, ErrInvalid
	}
	seen := make(map[string]struct{}, len(states))
	normalized := make([]StepState, len(states))
	for index, state := range states {
		state.StepID = strings.ToLower(strings.TrimSpace(state.StepID))
		if !validateStableID(state.StepID) {
			return nil, ErrInvalid
		}
		if _, exists := seen[state.StepID]; exists {
			return nil, ErrInvalid
		}
		seen[state.StepID] = struct{}{}
		switch state.Status {
		case StepPending, StepActive, StepComplete, StepOptionalSkipped:
			if state.ReasonCode != "" {
				return nil, ErrInvalid
			}
		case StepBlocked:
			if !validateReasonCode(state.ReasonCode, false) {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
		normalized[index] = state
	}
	encoded, err := json.Marshal(normalized)
	if err != nil || len(encoded) > MaxStepStatesJSON {
		return nil, ErrInvalid
	}
	return normalized, nil
}

func decodePersistedStepStates(raw string) ([]StepState, bool) {
	if len(raw) == 0 || len(raw) > MaxStepStatesJSON {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var states []StepState
	if err := decoder.Decode(&states); err != nil {
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, false
	}
	normalized, err := normalizeStepStates(states)
	return normalized, err == nil
}

func encodeStepStates(states []StepState) (string, error) {
	normalized, err := normalizeStepStates(states)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil || len(encoded) > MaxStepStatesJSON {
		return "", ErrInvalid
	}
	return string(encoded), nil
}

func normalizeCanonicalResult(result CanonicalResult) (CanonicalResult, string, error) {
	refs := []*string{
		&result.ChildRunID, &result.IntegrationPluginID, &result.IntegrationVersion,
		&result.HomeWorkspaceID, &result.ProjectWorkspaceID, &result.SelectedModeID,
		&result.CanonicalReceiptID,
	}
	for _, ref := range refs {
		*ref = strings.TrimSpace(*ref)
		if !validateCanonicalRef(*ref, true) {
			return CanonicalResult{}, "", ErrInvalid
		}
	}
	if len(result.OwnerRevisions) > MaxOwnerRevisions {
		return CanonicalResult{}, "", ErrInvalid
	}
	seen := make(map[CanonicalOwner]struct{}, len(result.OwnerRevisions))
	for _, revision := range result.OwnerRevisions {
		if _, ok := validCanonicalOwners[revision.Owner]; !ok || revision.Revision <= 0 {
			return CanonicalResult{}, "", ErrInvalid
		}
		if _, duplicate := seen[revision.Owner]; duplicate {
			return CanonicalResult{}, "", ErrInvalid
		}
		seen[revision.Owner] = struct{}{}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxOperationResultJSON {
		return CanonicalResult{}, "", ErrInvalid
	}
	return result, string(encoded), nil
}

func decodeCanonicalResult(raw string) (CanonicalResult, bool) {
	if len(raw) == 0 || len(raw) > MaxOperationResultJSON {
		return CanonicalResult{}, false
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var result CanonicalResult
	if err := decoder.Decode(&result); err != nil {
		return CanonicalResult{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CanonicalResult{}, false
	}
	normalized, _, err := normalizeCanonicalResult(result)
	return normalized, err == nil
}

func validateRunKind(kind RunKind) bool {
	return kind == RunKindRoot || kind == RunKindChild
}

func validateLifecycle(state LifecycleState) bool {
	switch state {
	case LifecycleNotStarted, LifecycleInProgress, LifecycleReady, LifecycleNeedsAttention:
		return true
	default:
		return false
	}
}
