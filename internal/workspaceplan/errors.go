package workspaceplan

import "errors"

// Typed domain errors. Callers match on these sentinels with errors.Is; the
// HTTP layer maps each one to a stable machine-readable code so clients can
// react to a condition without parsing prose (FR-166).
//
// Storage details never leak past this package: a store returns one of these
// rather than a driver error, so swapping SQLite for the in-memory store
// changes nothing a caller can observe.
var (
	// ErrPlanNotFound is returned when no Plan with that ID exists in the
	// requested workspace. Cross-workspace reads return this rather than a
	// permission error so one workspace cannot probe another's ID space
	// (FR-163, FR-167).
	ErrPlanNotFound = errors.New("plan not found")
	// ErrPlanExists is returned when creating a Plan whose ID is already taken.
	ErrPlanExists = errors.New("plan already exists")
	// ErrWorkspaceNotFound is returned when a Plan is filed against a workspace
	// that does not exist. Every Plan is owned by exactly one workspace, so an
	// unknown owner is a 404 about the workspace rather than an opaque storage
	// failure (FR-2).
	ErrWorkspaceNotFound = errors.New("workspace not found")
	// ErrVersionNotFound is returned for an unknown immutable review version.
	ErrVersionNotFound = errors.New("plan version not found")
	// ErrApprovalNotFound is returned for an unknown approval record.
	ErrApprovalNotFound = errors.New("plan approval not found")

	// ErrInvalidTransition is returned when a status change is not an edge in
	// the transition table (FR-14).
	ErrInvalidTransition = errors.New("invalid plan status transition")
	// ErrApprovalAuthority is returned when something other than an explicit
	// user action attempts an approval transition. Agent output, workspace
	// files, tool results, skill instructions, and chat text are never
	// approval (FR-59, FR-60).
	ErrApprovalAuthority = errors.New("plan approval requires an explicit user action")

	// ErrStaleDraft is returned when a draft write carries an outdated
	// revision token, meaning another session saved first (FR-30).
	ErrStaleDraft = errors.New("plan draft revision is stale")
	// ErrStaleVersion is returned when an action references a Plan version
	// that is no longer current (FR-74).
	ErrStaleVersion = errors.New("plan version is stale")
	// ErrApprovalMismatch is returned when an approval's version, content
	// hash, workspace, or declared effect no longer matches current state
	// (FR-69).
	ErrApprovalMismatch = errors.New("plan approval no longer matches current state")
	// ErrApprovalConsumed is returned when an approval has already been used
	// for its declared materialization and execution effect (FR-72).
	ErrApprovalConsumed = errors.New("plan approval has already been consumed")

	// ErrReconciliationNotFound is returned when no confirmation exists for a
	// preview token — including the case where the state moved and the token
	// the caller holds now describes a preview that was never confirmed.
	ErrReconciliationNotFound = errors.New("plan reconciliation confirmation not found")
	// ErrReconciliationConsumed is returned when a confirmation has already
	// been applied (FR-77).
	ErrReconciliationConsumed = errors.New("plan reconciliation has already been applied")
	// ErrStalePreview is returned when work changed after a reconciliation
	// preview was shown. The preview described cancelling Tasks whose state has
	// since moved, so acting on it could cancel work that has since started
	// (FR-77).
	ErrStalePreview = errors.New("plan reconciliation preview is out of date")

	// ErrValidation is returned when Plan content fails typed validation
	// (FR-41, FR-42).
	ErrValidation = errors.New("plan content is not valid")
	// ErrLimitExceeded is returned when content exceeds a hard bound. The Plan
	// must be split or superseded; content is never truncated (FR-43).
	ErrLimitExceeded = errors.New("plan exceeds a supported limit")
	// ErrUnavailableCapability is returned when a proposed assignee, agent, or
	// required capability is not available (FR-48, FR-85).
	ErrUnavailableCapability = errors.New("plan requires an unavailable agent or capability")
	// ErrUnsafePath is returned when a proposed artifact path escapes the
	// workspace root or is otherwise refused at the write boundary (FR-97).
	ErrUnsafePath = errors.New("plan artifact path is not within the workspace")

	// ErrMaterializationConflict is returned when a competing materialization
	// for the same Plan, version, and approval is already in flight or has
	// already committed (FR-91, FR-178).
	ErrMaterializationConflict = errors.New("plan materialization conflict")
	// ErrExecutionConflict is returned when another Plan holds the workspace
	// execution slot, or a stale worker attempts to dispatch after the lease
	// moved on (FR-106).
	ErrExecutionConflict = errors.New("another plan holds the workspace execution slot")

	// ErrPlanNotDeletable is returned when a hard delete is refused because
	// the Plan was approved or has linked Tasks, Runs, or artifacts. Those
	// Plans are archived instead, never silently deleted (FR-17).
	ErrPlanNotDeletable = errors.New("plan has effects and can only be archived")
	// ErrPlanArchived is returned when an action requires an active Plan but
	// the record is in History.
	ErrPlanArchived = errors.New("plan is archived")
)

// ErrorCode is the stable machine-readable code returned to API clients. The
// values are contract: they are safe to switch on in the browser and must not
// be renamed to improve prose (FR-166).
type ErrorCode string

const (
	CodeNotFound                = ErrorCode("plan_not_found")
	CodeWorkspaceNotFound       = ErrorCode("workspace_not_found")
	CodeVersionNotFound         = ErrorCode("plan_version_not_found")
	CodeApprovalNotFound        = ErrorCode("plan_approval_not_found")
	CodeConflict                = ErrorCode("plan_exists")
	CodeInvalidTransition       = ErrorCode("invalid_transition")
	CodeApprovalAuthority       = ErrorCode("approval_authority_required")
	CodeStaleDraft              = ErrorCode("stale_draft")
	CodeStaleVersion            = ErrorCode("stale_version")
	CodeApprovalMismatch        = ErrorCode("approval_mismatch")
	CodeApprovalConsumed        = ErrorCode("approval_consumed")
	CodeReconcileNotFound       = ErrorCode("reconciliation_not_found")
	CodeReconcileConsumed       = ErrorCode("reconciliation_consumed")
	CodeStalePreview            = ErrorCode("stale_preview")
	CodeValidationFailed        = ErrorCode("validation_failed")
	CodeLimitExceeded           = ErrorCode("limit_exceeded")
	CodeUnavailableCapability   = ErrorCode("unavailable_capability")
	CodeUnsafePath              = ErrorCode("unsafe_path")
	CodeMaterializationConflict = ErrorCode("materialization_conflict")
	CodeExecutionConflict       = ErrorCode("execution_conflict")
	CodeNotDeletable            = ErrorCode("plan_not_deletable")
	CodeArchived                = ErrorCode("plan_archived")
	// CodeModelUnavailable reports that generation is unavailable right now.
	// It is distinct from a failure: everything that does not need a model
	// still works, so the UI disables only the generate controls (FR-58).
	CodeModelUnavailable = ErrorCode("model_unavailable")
	// CodeRevisionNeedsConfirmation reports that a targeted revision would
	// discard user-authored content or break a dependency, and is waiting for
	// the user to see the disclosure (FR-56).
	CodeRevisionNeedsConfirmation = ErrorCode("revision_needs_confirmation")
	// CodeInternal is the fallback for an error with no stable mapping. It
	// never carries the underlying message to the client.
	CodeInternal = ErrorCode("internal_error")
)

// codeBySentinel maps each domain sentinel to its wire code. Written as data so
// a new sentinel without a code is visible as a missing entry rather than a
// forgotten switch arm.
var codeBySentinel = []struct {
	err  error
	code ErrorCode
}{
	{ErrPlanNotFound, CodeNotFound},
	{ErrWorkspaceNotFound, CodeWorkspaceNotFound},
	{ErrVersionNotFound, CodeVersionNotFound},
	{ErrApprovalNotFound, CodeApprovalNotFound},
	{ErrPlanExists, CodeConflict},
	{ErrInvalidTransition, CodeInvalidTransition},
	{ErrApprovalAuthority, CodeApprovalAuthority},
	{ErrStaleDraft, CodeStaleDraft},
	{ErrStaleVersion, CodeStaleVersion},
	{ErrApprovalMismatch, CodeApprovalMismatch},
	{ErrApprovalConsumed, CodeApprovalConsumed},
	{ErrReconciliationNotFound, CodeReconcileNotFound},
	{ErrReconciliationConsumed, CodeReconcileConsumed},
	{ErrStalePreview, CodeStalePreview},
	{ErrValidation, CodeValidationFailed},
	{ErrLimitExceeded, CodeLimitExceeded},
	{ErrUnavailableCapability, CodeUnavailableCapability},
	{ErrUnsafePath, CodeUnsafePath},
	{ErrMaterializationConflict, CodeMaterializationConflict},
	{ErrExecutionConflict, CodeExecutionConflict},
	{ErrPlanNotDeletable, CodeNotDeletable},
	{ErrPlanArchived, CodeArchived},
	{ErrModelUnavailable, CodeModelUnavailable},
}

// CodeFor maps an error to its stable API code, or CodeInternal when the error
// is not one of the domain sentinels.
func CodeFor(err error) ErrorCode {
	if err == nil {
		return ""
	}
	for _, entry := range codeBySentinel {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}
	return CodeInternal
}
