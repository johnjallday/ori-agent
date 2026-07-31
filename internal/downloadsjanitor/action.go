package downloadsjanitor

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// A FileAction is the durable record of one attempted file mutation.
//
// It is the feature's accountability record, and its shape follows from what it
// has to prove after the fact:
//
//   - That the user approved this exact operation on this exact file, before it
//     happened. ApprovedAt and ApprovedBy are written when the user confirms,
//     and the record is journaled before the mutation is issued (FR-92, FR-94).
//   - What the file looked like when it was approved, and what it looked like
//     afterwards. Before/After fingerprints are what make an undo safe and a
//     silent replacement detectable.
//   - What actually happened, as opposed to what was intended. Result is set
//     from a verified outcome, never from an assumption that the call worked.
//
// Once created, the identity and approval fields never change. Execution fills
// in the outcome fields exactly once; undo appends its own outcome. Nothing
// rewrites history — a failed action stays in the journal as a failed action.
type FileAction struct {
	// ID is stable and is what undo and history refer to.
	ID string `json:"id"`
	// WorkspaceID, BatchID, and CandidateID tie the action back to what the
	// user was looking at when they approved it.
	WorkspaceID string `json:"workspace_id"`
	BatchID     string `json:"batch_id,omitempty"`
	CandidateID string `json:"candidate_id"`

	// RootID identifies WHICH managed folder this action was performed against.
	//
	// Every path in this record is relative to the configured folder, which is
	// what keeps absolute paths out of the journal (FR-143). The cost is that
	// after a relink those same relative paths would read as if they belonged to
	// the NEW folder, and undo would reverse them against it. RootID is the
	// annotation that prevents both: it is stamped from the settings at approval
	// time, and a relink issues a fresh one, so an action from a previous folder
	// is identifiable as such forever (FR-57).
	//
	// Empty on actions journaled before this field existed; those are treated as
	// belonging to the current root, which is what they did when written.
	RootID string `json:"root_id,omitempty"`

	// Operation is what was approved: a move into a category, or a send to the
	// recoverable system Trash. There is no third option — version 1 has no
	// permanent delete anywhere (FR-91).
	Operation Operation `json:"operation"`

	// SourceName is the file's top-level name within the configured folder.
	// Like a candidate's Name it is never a path, so an action can never
	// address anything outside that folder.
	SourceName string `json:"source_name"`
	// DestinationCategory is the allowlisted category a move files into.
	DestinationCategory Category `json:"destination_category,omitempty"`
	// DestinationRelative is the destination as shown and journaled:
	// "Filed/Documents/report (2).pdf", relative to the configured folder.
	// Absolute paths are derived at execution time and never stored (FR-110).
	DestinationRelative string `json:"destination_relative,omitempty"`

	// BeforeFingerprint is the file state the user approved. It is rechecked
	// immediately before the mutation; a mismatch makes the action stale.
	BeforeFingerprint Fingerprint `json:"before_fingerprint"`
	// AfterFingerprint is the moved file's state, recorded once the move is
	// verified. Undo checks it before reversing anything.
	AfterFingerprint Fingerprint `json:"after_fingerprint,omitzero"`

	// ApprovedAt and ApprovedBy record the human authorization. An agent
	// proposal is never approval, and these fields are the proof (FR-94).
	ApprovedAt time.Time `json:"approved_at"`
	ApprovedBy string    `json:"approved_by"`
	// IdempotencyKey makes a retry of the same approved operation a no-op
	// rather than a second mutation (FR-86).
	IdempotencyKey string `json:"idempotency_key"`

	// StartedAt and CompletedAt bracket the execution attempt.
	StartedAt   time.Time `json:"started_at,omitzero"`
	CompletedAt time.Time `json:"completed_at,omitzero"`

	// Result is the verified outcome.
	Result ActionResult `json:"result"`
	// ErrorSummary is a short, user-facing explanation of a failure. It carries
	// no raw OS error text and no absolute path.
	ErrorSummary string `json:"error_summary,omitempty"`

	// Undo state. TrashRestoreToken is the platform Trash handle captured when
	// a file is trashed; it is the only way a restore can find the item again.
	Undo              UndoState `json:"undo"`
	TrashRestoreToken string    `json:"trash_restore_token,omitempty"`
	UndoneAt          time.Time `json:"undone_at,omitzero"`
	UndoError         string    `json:"undo_error,omitempty"`
}

// Operation is the kind of mutation an action performs.
type Operation string

const (
	// OperationMove files a candidate into <root>/Filed/<category>.
	OperationMove Operation = "move"
	// OperationTrash sends a candidate to the recoverable system Trash. It is
	// only ever set from an explicit per-file user decision.
	OperationTrash Operation = "trash"
)

// ValidOperations is the complete set. There is no permanent-delete operation,
// and adding one would be a product decision, not an implementation detail.
var ValidOperations = []Operation{OperationMove, OperationTrash}

// ActionResult is the verified outcome of an attempt.
type ActionResult string

const (
	// ResultPending means approved and journaled but not yet attempted.
	ResultPending ActionResult = "pending"
	// ResultApplying means the mutation is in flight. An action found in this
	// state at startup is reconciled against the filesystem rather than assumed
	// either way.
	ResultApplying ActionResult = "applying"
	// ResultApplied means the mutation completed and was verified.
	ResultApplied ActionResult = "applied"
	// ResultStale means the file changed between approval and execution, so
	// nothing was done. The source is untouched and needs a fresh scan.
	ResultStale ActionResult = "stale"
	// ResultFailed means the attempt did not succeed. The source is left where
	// it was whenever the operating system allows.
	ResultFailed ActionResult = "failed"
)

// Terminal reports whether the result will not change without a new action.
func (r ActionResult) Terminal() bool {
	return r == ResultApplied || r == ResultStale || r == ResultFailed
}

// UndoState is whether this action can still be reversed.
type UndoState string

const (
	// UndoUnavailable is the default: nothing to undo (not applied), or the
	// operation is not reversible in principle.
	UndoUnavailable UndoState = "unavailable"
	// UndoAvailable means the action completed and could be reversed if the
	// filesystem still allows it. Eligibility is rechecked at undo time — this
	// is a hint for the UI, never a promise.
	UndoAvailable UndoState = "available"
	// UndoInProgress means an undo is running.
	UndoInProgress UndoState = "in_progress"
	// UndoDone means the action was reversed.
	UndoDone UndoState = "undone"
	// UndoFailed means an undo was attempted and did not succeed; UndoError
	// explains why in user-facing words.
	UndoFailed UndoState = "failed"
)

// ErrInvalidAction reports an action record that could not be executed safely.
var ErrInvalidAction = errors.New("invalid downloads janitor action")

// NewApprovedAction builds the journal entry for one approved decision.
//
// It is deliberately the only constructor: an action cannot come into existence
// without an approver, an approval time, an idempotency key, and the fingerprint
// the approval was given against.
func NewApprovedAction(
	id, workspaceID string,
	candidate JanitorCandidate,
	operation Operation,
	destinationRelative string,
	approvedBy string,
	approvedAt time.Time,
	idempotencyKey string,
	rootID string,
) (FileAction, error) {
	action := FileAction{
		ID:                  strings.TrimSpace(id),
		WorkspaceID:         strings.TrimSpace(workspaceID),
		RootID:              strings.TrimSpace(rootID),
		BatchID:             candidate.BatchID,
		CandidateID:         candidate.ID,
		Operation:           operation,
		SourceName:          candidate.Name,
		BeforeFingerprint:   candidate.Fingerprint,
		ApprovedAt:          approvedAt,
		ApprovedBy:          strings.TrimSpace(approvedBy),
		IdempotencyKey:      strings.TrimSpace(idempotencyKey),
		Result:              ResultPending,
		Undo:                UndoUnavailable,
		DestinationRelative: strings.TrimSpace(destinationRelative),
	}
	if operation == OperationMove {
		action.DestinationCategory = candidate.EffectiveCategory()
	}
	if err := action.Validate(); err != nil {
		return FileAction{}, err
	}
	return action, nil
}

// Validate enforces what must be true before an action is journaled or
// executed.
func (a FileAction) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidAction)
	}
	if strings.TrimSpace(a.WorkspaceID) == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidAction)
	}
	if strings.TrimSpace(a.CandidateID) == "" {
		return fmt.Errorf("%w: candidate id is required", ErrInvalidAction)
	}
	switch a.Operation {
	case OperationMove, OperationTrash:
	default:
		return fmt.Errorf("%w: unknown operation %q", ErrInvalidAction, a.Operation)
	}
	if err := ValidateFileName(a.SourceName); err != nil {
		return fmt.Errorf("%w: source name is not a top-level file name", ErrInvalidAction)
	}
	if a.BeforeFingerprint.Zero() {
		return fmt.Errorf("%w: an approval must record the file state it was given for", ErrInvalidAction)
	}
	if a.BeforeFingerprint.Name != a.SourceName {
		return fmt.Errorf("%w: fingerprint does not describe the source file", ErrInvalidAction)
	}
	// Approval is not optional and not implicit. An action with no approver or
	// no approval time cannot be distinguished later from one the system
	// invented, which is exactly the thing the journal exists to rule out.
	if a.ApprovedAt.IsZero() {
		return fmt.Errorf("%w: an action must record when it was approved", ErrInvalidAction)
	}
	if strings.TrimSpace(a.ApprovedBy) == "" {
		return fmt.Errorf("%w: an action must record who approved it", ErrInvalidAction)
	}
	if strings.TrimSpace(a.IdempotencyKey) == "" {
		return fmt.Errorf("%w: an action must carry an idempotency key", ErrInvalidAction)
	}
	if a.Operation == OperationMove {
		if !ValidCategory(a.DestinationCategory) {
			return fmt.Errorf("%w: %q is not a version 1 category", ErrInvalidAction, a.DestinationCategory)
		}
		if err := validateDestinationRelative(a.DestinationRelative); err != nil {
			return err
		}
	}
	if a.Operation == OperationTrash && strings.TrimSpace(a.DestinationRelative) != "" {
		return fmt.Errorf("%w: a Trash action has no destination inside the folder", ErrInvalidAction)
	}
	return nil
}

// validateDestinationRelative checks the stored destination reference: it must
// be a relative path inside the filing folder with no traversal. The real
// destination is derived from server state at execution time; this guards the
// value that gets journaled, logged, and shown.
func validateDestinationRelative(relative string) error {
	relative = strings.TrimSpace(relative)
	if relative == "" {
		return fmt.Errorf("%w: a move must record its destination", ErrInvalidAction)
	}
	if strings.ContainsRune(relative, 0) || strings.HasPrefix(relative, "/") || strings.Contains(relative, `\`) {
		return fmt.Errorf("%w: destination %q must be relative to the configured folder", ErrInvalidAction, relative)
	}
	segments := strings.Split(relative, "/")
	if len(segments) < 2 {
		return fmt.Errorf("%w: destination %q must name a folder and a file", ErrInvalidAction, relative)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: destination %q contains an unusable path segment", ErrInvalidAction, relative)
		}
	}
	return nil
}

// DestinationRelativeFor builds the journalled destination reference for a move:
// "<filingRoot>/<Category>/<final name>".
func DestinationRelativeFor(filingRootName string, category Category, finalName string) (string, error) {
	definition, err := LookupCategory(string(category))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(filingRootName) == "" {
		filingRootName = DefaultFilingRootName
	}
	if err := ValidateFileName(finalName); err != nil {
		return "", fmt.Errorf("%w: destination file name is unusable", ErrInvalidAction)
	}
	relative := path.Join(filingRootName, definition.FolderName, finalName)
	if err := validateDestinationRelative(relative); err != nil {
		return "", err
	}
	return relative, nil
}

// BelongsToRoot reports whether this action was performed against the given
// managed-folder generation.
//
// An action with no RootID predates the field; it is treated as belonging to
// the current root, which is what it did when it was written. A workspace with
// no RootID yet (never relinked) likewise matches everything, so introducing
// this check cannot retroactively make existing history un-undoable.
func (a FileAction) BelongsToRoot(currentRootID string) bool {
	actionRoot := strings.TrimSpace(a.RootID)
	current := strings.TrimSpace(currentRootID)
	if actionRoot == "" || current == "" {
		return true
	}
	return actionRoot == current
}

// Undoable reports whether this action is a candidate for undo. It is a
// precondition, not a guarantee: the filesystem is rechecked at undo time, when
// the original path may be occupied or the Trash item gone.
func (a FileAction) Undoable() bool {
	if a.Result != ResultApplied {
		return false
	}
	switch a.Undo {
	case UndoAvailable:
		return true
	default:
		return false
	}
}

// MarkApplying records that execution has begun. Separated from the outcome so
// an interrupted run leaves evidence that something was in flight, rather than
// looking like it never started.
func (a FileAction) MarkApplying(at time.Time) FileAction {
	a.Result = ResultApplying
	a.StartedAt = at
	return a
}

// MarkApplied records a verified success along with the resulting file state.
func (a FileAction) MarkApplied(after Fingerprint, at time.Time) FileAction {
	a.Result = ResultApplied
	a.AfterFingerprint = after
	a.CompletedAt = at
	a.Undo = UndoAvailable
	a.ErrorSummary = ""
	return a
}

// MarkStale records that the file changed between approval and execution. The
// source was left untouched.
func (a FileAction) MarkStale(summary string, at time.Time) FileAction {
	a.Result = ResultStale
	a.CompletedAt = at
	a.ErrorSummary = summary
	a.Undo = UndoUnavailable
	return a
}

// MarkFailed records a verified failure with a user-facing summary.
func (a FileAction) MarkFailed(summary string, at time.Time) FileAction {
	a.Result = ResultFailed
	a.CompletedAt = at
	a.ErrorSummary = summary
	a.Undo = UndoUnavailable
	return a
}

// ItemOutcome is one line of an apply response: what happened to one approved
// candidate. A batch returns these in order, because a batch is applied per
// item and partial success is a normal result, not an error (FR-72, FR-88).
type ItemOutcome struct {
	CandidateID string       `json:"candidate_id"`
	ActionID    string       `json:"action_id,omitempty"`
	Name        string       `json:"name"`
	Operation   Operation    `json:"operation"`
	Result      ActionResult `json:"result"`
	// Destination is the relative reference the file ended up at, when it moved.
	Destination string `json:"destination,omitempty"`
	// Message explains a stale or failed outcome in words the user can act on.
	Message string `json:"message,omitempty"`
	// Undoable mirrors the action's undo eligibility at the time of the reply.
	Undoable bool `json:"undoable,omitempty"`
}

// SummarizeOutcomes counts a batch's per-item results. The UI states these
// plainly rather than calling a mixed batch a success (FR-72).
func SummarizeOutcomes(outcomes []ItemOutcome) (applied, failed, stale int) {
	for _, outcome := range outcomes {
		switch outcome.Result {
		case ResultApplied:
			applied++
		case ResultFailed:
			failed++
		case ResultStale:
			stale++
		}
	}
	return applied, failed, stale
}
