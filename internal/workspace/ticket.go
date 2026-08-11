package workspace

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// This file defines the canonical Ticket domain vocabulary
// (tasks/prd-workspace-ticket-management.md). A Ticket is the product-facing
// evolution of the existing workspace.Task record — the same persisted row,
// not a parallel entity (FR-2). Everything here is about the *lifecycle*
// half of that record: the state a user reasons about, its ordering, its
// audit trail, and the validated shape of a create/edit request.
//
// The hard rule the rest of the package depends on: Ticket state is stored
// and validated as a first-class value (FR-7). It is never inferred from
// kanban_column_id, tags, latest Run status, assignee, or the presence of a
// result. Run status describes one execution attempt and stays independent
// (FR-8).

// TicketState is the canonical durable workflow state of a Ticket. Exactly
// six values are legal (FR-6).
type TicketState string

const (
	// TicketStateBacklog holds captured work that is not committed to. A
	// Backlog Ticket can never be assigned, scheduled, run, reviewed, or
	// completed (FR-20, FR-21).
	TicketStateBacklog TicketState = "backlog"
	// TicketStateReady is committed work that may be assigned, scheduled, or
	// started — but stays quiescent until an explicit intent (FR-24).
	TicketStateReady TicketState = "ready"
	// TicketStateInProgress is work explicitly started, manually or by a Run.
	TicketStateInProgress TicketState = "in_progress"
	// TicketStateReview is work awaiting human acceptance. A successful
	// one-shot Run lands here, never directly in Done (FR-28, FR-29).
	TicketStateReview TicketState = "review"
	// TicketStateDone is work a user explicitly accepted (FR-30).
	TicketStateDone TicketState = "done"
	// TicketStateCancelled is work closed without completion, retained for
	// history and reopenable (FR-38, FR-39).
	TicketStateCancelled TicketState = "cancelled"
)

// AllTicketStates lists every canonical state in board/left-to-right order,
// with the non-column terminal state last. Board column construction and
// validation both read this rather than repeating the literals.
var AllTicketStates = []TicketState{
	TicketStateBacklog,
	TicketStateReady,
	TicketStateInProgress,
	TicketStateReview,
	TicketStateDone,
	TicketStateCancelled,
}

// Valid reports whether s is one of the six canonical states.
func (s TicketState) Valid() bool {
	switch s {
	case TicketStateBacklog, TicketStateReady, TicketStateInProgress,
		TicketStateReview, TicketStateDone, TicketStateCancelled:
		return true
	}
	return false
}

// Terminal reports whether s is a closed state. Terminal Tickets are subject
// to the 14-day recent/archive default (FR-143) and are never deleted by it.
func (s TicketState) Terminal() bool {
	return s == TicketStateDone || s == TicketStateCancelled
}

// Label returns the user-facing name for the state. Copy lives here so List,
// Board, detail, and events cannot drift apart (FR-102).
func (s TicketState) Label() string {
	switch s {
	case TicketStateBacklog:
		return "Backlog"
	case TicketStateReady:
		return "Ready"
	case TicketStateInProgress:
		return "In Progress"
	case TicketStateReview:
		return "Review"
	case TicketStateDone:
		return "Done"
	case TicketStateCancelled:
		return "Cancelled"
	}
	return string(s)
}

// ParseTicketState validates and normalizes an inbound state value.
func ParseTicketState(value string) (TicketState, error) {
	state := TicketState(strings.ToLower(strings.TrimSpace(value)))
	if !state.Valid() {
		return "", fmt.Errorf("ticket state must be one of backlog, ready, in_progress, review, done, cancelled (got %q)", value)
	}
	return state, nil
}

// legalTicketTransitions is the fixed V1 workflow
// (Backlog → Ready → In Progress → Review → Done) plus cancellation and
// explicit reopening. Rows are the current state; the set holds every legal
// destination.
//
// Deliberate omissions, each enforcing a PRD rule:
//   - Backlog reaches only Ready. A direct Backlog → In Progress/Review/Done
//     move is refused so work always passes through the commitment step
//     (FR-36).
//   - In Progress never reaches Done directly. Acceptance happens from Review
//     by explicit user action (FR-29, FR-30).
//   - Done and Cancelled reopen only to Ready, never straight back into
//     execution (FR-37, FR-39).
var legalTicketTransitions = map[TicketState]map[TicketState]struct{}{
	TicketStateBacklog: {
		TicketStateReady:     {},
		TicketStateCancelled: {},
	},
	TicketStateReady: {
		TicketStateBacklog:    {}, // return to Backlog: uncommit without losing the record
		TicketStateInProgress: {},
		TicketStateCancelled:  {},
	},
	TicketStateInProgress: {
		TicketStateReady:     {}, // stop work without cancelling
		TicketStateReview:    {},
		TicketStateCancelled: {},
	},
	TicketStateReview: {
		TicketStateInProgress: {}, // request changes / retry (FR-31)
		TicketStateDone:       {}, // explicit user acceptance only (FR-30)
		TicketStateCancelled:  {},
	},
	TicketStateDone: {
		TicketStateReady: {}, // explicit reopen (FR-37)
	},
	TicketStateCancelled: {
		TicketStateReady: {}, // explicit reopen (FR-39)
	},
}

// IllegalTicketTransitionError describes a refused Ticket state transition.
// It carries both ends so the HTTP layer can render an actionable 4xx that
// names the legal destinations (FR-94).
type IllegalTicketTransitionError struct {
	TicketID string
	From     TicketState
	To       TicketState
}

func (e *IllegalTicketTransitionError) Error() string {
	legal := LegalTicketTransitions(e.From)
	if len(legal) == 0 {
		return fmt.Sprintf("ticket %s cannot move from %s to %s: %s is a closed state",
			e.TicketID, e.From.Label(), e.To.Label(), e.From.Label())
	}
	names := make([]string, 0, len(legal))
	for _, s := range legal {
		names = append(names, s.Label())
	}
	return fmt.Sprintf("ticket %s cannot move from %s to %s; allowed: %s",
		e.TicketID, e.From.Label(), e.To.Label(), strings.Join(names, ", "))
}

// LegalTicketTransitions returns every state reachable from `from`, in
// canonical board order so menus and tests are deterministic. The UI uses
// this to render only legal actions rather than offering a move the server
// will refuse (FR-50, FR-70).
func LegalTicketTransitions(from TicketState) []TicketState {
	allowed := legalTicketTransitions[from]
	if len(allowed) == 0 {
		return nil
	}
	out := make([]TicketState, 0, len(allowed))
	for _, candidate := range AllTicketStates {
		if _, ok := allowed[candidate]; ok {
			out = append(out, candidate)
		}
	}
	return out
}

// CanTransitionTicket reports whether from → to is in the legal table. A
// same-state move is not a transition and returns false, so no-op flips are
// surfaced rather than silently recorded in history.
func CanTransitionTicket(from, to TicketState) bool {
	if from == to {
		return false
	}
	_, ok := legalTicketTransitions[from][to]
	return ok
}

// Ticket state-change actors. Every transition records who caused it so the
// audit trail can distinguish a human acceptance from a Run callback (FR-40).
const (
	TicketActorUser        = "user"
	TicketActorRun         = "run"
	TicketActorScheduler   = "scheduler"
	TicketActorCoordinator = "coordinator"
	TicketActorAssistant   = "assistant"
	TicketActorMigration   = "migration"
	TicketActorSystem      = "system"
)

// TicketStateChange is one immutable entry in a Ticket's transition history
// (FR-40). Entries are append-only: a later transition never rewrites an
// earlier one, which is what lets Ticket detail show the full story of a
// reopened or retried Ticket.
type TicketStateChange struct {
	From      TicketState `json:"from"`
	To        TicketState `json:"to"`
	Actor     string      `json:"actor"`
	ActorID   string      `json:"actor_id,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	RunID     string      `json:"run_id,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// Ticket provenance values (FR-16). These intentionally reuse the existing
// Backlog capture source strings so migration preserves already-persisted
// provenance without rewriting it.
const (
	TicketSourceManual       = BacklogSourceManual       // "manual"
	TicketSourceAssistant    = BacklogSourceAssistant    // "assistant"
	TicketSourceActionCenter = BacklogSourceActionCenter // "action_center"
	TicketSourceMarkdown     = BacklogSourceBacklogFile  // "backlog_markdown"
	// TicketSourceNote records a Ticket created from a Note (FR-73). The
	// source ID is the originating Note's ID.
	TicketSourceNote = "note"
	// TicketSourceMigration marks records that predate provenance tracking.
	TicketSourceMigration = "migration"
)

// validTicketSources gates the provenance field so an arbitrary caller
// string cannot become a permanent, unfilterable value.
var validTicketSources = map[string]struct{}{
	TicketSourceManual:       {},
	TicketSourceAssistant:    {},
	TicketSourceActionCenter: {},
	TicketSourceMarkdown:     {},
	TicketSourceNote:         {},
	TicketSourceMigration:    {},
}

// NormalizeTicketSource validates provenance, defaulting empty to manual.
func NormalizeTicketSource(value string) (string, error) {
	source := strings.ToLower(strings.TrimSpace(value))
	if source == "" {
		return TicketSourceManual, nil
	}
	if _, ok := validTicketSources[source]; !ok {
		return "", fmt.Errorf("unsupported ticket source %q", value)
	}
	return source, nil
}

// Ticket field limits. Titles are single-line and bounded so a pasted
// document cannot become a card title; the Markdown description is bounded
// far more generously because it is the body.
const (
	TicketTitleMaxLength       = 300
	TicketDescriptionMaxLength = 100000
	TicketReasonMaxLength      = 500
	// TicketPriorityDefault is the middle of the existing 1..5 task priority
	// scale, matching normalizeBacklogPriority.
	TicketPriorityDefault = 3
	TicketPriorityMin     = 1
	TicketPriorityMax     = 5
)

// ErrTicketVersionConflict is returned when a mutation carries a stale
// version token, meaning another editor already changed the Ticket (FR-93).
// The HTTP layer maps this to 409 with the current record attached so the
// client can refresh rather than silently overwrite.
var ErrTicketVersionConflict = errors.New("ticket was modified by someone else; reload and try again")

// ErrTicketNotFound is returned when a Ticket ID does not resolve inside its
// claimed owning workspace. Foreign IDs are rejected the same way as unknown
// ones so a roll-up view can never be used to probe another workspace.
var ErrTicketNotFound = errors.New("ticket not found in this workspace")

// TicketValidationError is an actionable field-level validation failure. It
// exists so handlers can return a 4xx naming the offending field without
// string-matching error text (FR-94).
type TicketValidationError struct {
	Field   string
	Message string
}

func (e *TicketValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func invalidTicketField(field, format string, args ...any) error {
	return &TicketValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// NormalizeTicketTitle validates and trims a Ticket title. The title is
// required, single-line, and bounded (FR-4).
func NormalizeTicketTitle(value string) (string, error) {
	title := strings.TrimSpace(value)
	if title == "" {
		return "", invalidTicketField("title", "title is required")
	}
	if strings.ContainsAny(title, "\r\n") {
		return "", invalidTicketField("title", "title must be a single line; use the description for detail")
	}
	if utf8.RuneCountInString(title) > TicketTitleMaxLength {
		return "", invalidTicketField("title", "title must be %d characters or fewer", TicketTitleMaxLength)
	}
	return title, nil
}

// NormalizeTicketDescription bounds the optional Markdown body. The value is
// stored raw and escaped at render time, preserving the existing trust
// boundary rather than sanitizing here (FR-104).
func NormalizeTicketDescription(value string) (string, error) {
	description := strings.TrimSpace(value)
	if utf8.RuneCountInString(description) > TicketDescriptionMaxLength {
		return "", invalidTicketField("description", "description must be %d characters or fewer", TicketDescriptionMaxLength)
	}
	return description, nil
}

// NormalizeTicketPriority clamps priority into the existing 1..5 scale,
// treating the zero value as "not supplied" and defaulting it. Out-of-range
// values are an explicit error rather than a silent clamp, so a client bug
// surfaces instead of quietly rewriting the user's choice.
func NormalizeTicketPriority(priority int) (int, error) {
	if priority == 0 {
		return TicketPriorityDefault, nil
	}
	if priority < TicketPriorityMin || priority > TicketPriorityMax {
		return 0, invalidTicketField("priority", "priority must be between %d and %d", TicketPriorityMin, TicketPriorityMax)
	}
	return priority, nil
}

// NormalizeTicketDueDate normalizes an optional first-class due date
// (FR-14). A zero time is treated as "no due date" rather than year 1.
func NormalizeTicketDueDate(due *time.Time) *time.Time {
	if due == nil || due.IsZero() {
		return nil
	}
	value := due.UTC()
	return &value
}

// NormalizeTicketReason bounds the optional free-text reason attached to a
// transition.
func NormalizeTicketReason(value string) (string, error) {
	reason := strings.TrimSpace(value)
	if utf8.RuneCountInString(reason) > TicketReasonMaxLength {
		return "", invalidTicketField("reason", "reason must be %d characters or fewer", TicketReasonMaxLength)
	}
	return reason, nil
}

// NormalizeLinkedNoteIDs trims, de-duplicates, and orders linked Note IDs
// deterministically. Membership validation against the workspace's Note
// store happens in TicketService, which is the only layer that can see it;
// this function only guarantees the stored shape is clean (FR-17).
func NormalizeLinkedNoteIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FormatTicketNumber renders a workspace-local Ticket number for display
// (FR-141). Numbers are workspace-scoped, so the bare "#12" is only
// unambiguous within one workspace; rolled-up views call
// FormatQualifiedTicketNumber instead.
func FormatTicketNumber(number int64) string {
	if number <= 0 {
		return ""
	}
	return fmt.Sprintf("#%d", number)
}

// FormatQualifiedTicketNumber renders a Ticket number with enough owning
// workspace context to disambiguate a rolled-up list (FR-141).
func FormatQualifiedTicketNumber(workspaceName string, number int64) string {
	base := FormatTicketNumber(number)
	name := strings.TrimSpace(workspaceName)
	if base == "" || name == "" {
		return base
	}
	return fmt.Sprintf("%s %s", name, base)
}

// --- Legacy status bridge -------------------------------------------------
//
// The execution engine (coordinator, scheduler, executor, Run bridge) still
// reads Task.Status. Until Group 4 routes every one of those paths through
// canonical state, the two fields are kept deliberately consistent: canonical
// TicketState is the authority, and legacy Status is a projection of it that
// existing machinery keeps working against.
//
// This is a compatibility projection, NOT a second source of truth. Nothing
// reads Status to decide a Ticket's lifecycle — CanonicalState is the only
// answer to "what state is this Ticket in" (FR-7).

// ticketStateForLegacyStatus maps a persisted legacy TaskStatus onto the
// canonical state it means. Used to read records that predate TicketState and
// as the basis for Group 7's migration mapping (FR-107 through FR-112).
//
// Note that failed and timed-out records map to In Progress, not to a failure
// state: a failed Run leaves the work open and needing attention, it does not
// close the Ticket (FR-32, FR-110).
func ticketStateForLegacyStatus(status TaskStatus) TicketState {
	switch status {
	case TaskStatusBacklog:
		return TicketStateBacklog
	case TaskStatusPending, TaskStatusAssigned:
		return TicketStateReady
	case TaskStatusInProgress, TaskStatusWaitingForChoice, TaskStatusFailed, TaskStatusTimeout:
		return TicketStateInProgress
	case TaskStatusCompleted:
		return TicketStateDone
	case TaskStatusCancelled:
		return TicketStateCancelled
	}
	// The empty status is a freshly-constructed Task that never went through
	// a capture path; treat it as committed-but-not-started work.
	return TicketStateReady
}

// legacyStatusForTicketState projects a canonical state back onto the legacy
// TaskStatus the execution engine understands.
//
// Review and Done both project to completed: legacy status has no concept of
// "finished but not yet accepted", so a legacy client sees the work as done
// while canonical state preserves the distinction. That asymmetry is exactly
// why compatibility routes must not own lifecycle rules (FR-97).
func legacyStatusForTicketState(state TicketState, assignee string) TaskStatus {
	switch state {
	case TicketStateBacklog:
		return TaskStatusBacklog
	case TicketStateReady:
		if trimmed := strings.TrimSpace(assignee); trimmed != "" && !strings.EqualFold(trimmed, "unassigned") {
			return TaskStatusAssigned
		}
		return TaskStatusPending
	case TicketStateInProgress:
		return TaskStatusInProgress
	case TicketStateReview, TicketStateDone:
		return TaskStatusCompleted
	case TicketStateCancelled:
		return TaskStatusCancelled
	}
	return TaskStatusPending
}

// CanonicalState returns the Ticket's authoritative lifecycle state (FR-7).
//
// Records persisted before TicketState existed carry only legacy Status, so
// this falls back to the documented mapping rather than returning an empty
// state. Every read path uses this instead of touching TicketState directly,
// which is what lets canonical behavior work correctly on a workspace that
// Group 7's migration has not yet visited.
func (t *Task) CanonicalState() TicketState {
	if t == nil {
		return ""
	}
	if t.TicketState.Valid() {
		return t.TicketState
	}
	return ticketStateForLegacyStatus(t.Status)
}

// applyTicketState sets canonical state and re-projects the legacy status to
// match. It performs no legality check — callers go through
// Task.TransitionTicket, which validates first.
func (t *Task) applyTicketState(state TicketState) {
	t.TicketState = state
	t.Status = legacyStatusForTicketState(state, t.To)
}

// TransitionTicket moves the Ticket to `next`, validating against the fixed
// workflow and appending an audit entry (FR-40). The Task is left completely
// untouched when the transition is refused, so a rejected move can never
// half-apply.
//
// It does not persist anything and does not bump the version — TicketService
// owns both, because they must happen inside the store transaction.
func (t *Task) TransitionTicket(next TicketState, change TicketStateChange) error {
	if t == nil {
		return fmt.Errorf("ticket is nil")
	}
	current := t.CanonicalState()
	if !next.Valid() {
		return invalidTicketField("state", "unknown ticket state %q", string(next))
	}
	if !CanTransitionTicket(current, next) {
		return &IllegalTicketTransitionError{TicketID: t.ID, From: current, To: next}
	}

	change.From = current
	change.To = next
	if strings.TrimSpace(change.Actor) == "" {
		change.Actor = TicketActorSystem
	}
	if change.Timestamp.IsZero() {
		change.Timestamp = time.Now().UTC()
	}

	t.applyTicketState(next)
	t.StateHistory = append(t.StateHistory, change)
	t.stampTerminalTimestamps(next, change.Timestamp)
	return nil
}

// stampTerminalTimestamps keeps the existing StartedAt/CompletedAt fields
// meaningful under canonical state. Reopening deliberately clears
// CompletedAt (the work is open again) while StateHistory retains the fact
// that it was once closed (FR-37).
func (t *Task) stampTerminalTimestamps(next TicketState, at time.Time) {
	switch next {
	case TicketStateInProgress:
		if t.StartedAt == nil {
			started := at
			t.StartedAt = &started
		}
	case TicketStateDone, TicketStateCancelled:
		completed := at
		t.CompletedAt = &completed
	case TicketStateReady, TicketStateBacklog:
		t.CompletedAt = nil
	}
}

// NeedsAttention reports whether the Ticket's latest execution attempt left
// it in a state a human should look at (FR-32, FR-61). It is a derived
// presentation signal, never a stored state: the Ticket itself stays In
// Progress with an open retry path.
//
// Group 1 derives this from the record's own failure fields. Group 4 refines
// it to consult the latest Workspace Run.
func (t *Task) NeedsAttention() bool {
	if t == nil || t.CanonicalState() != TicketStateInProgress {
		return false
	}
	if strings.TrimSpace(t.Error) != "" {
		return true
	}
	return t.Status == TaskStatusFailed || t.Status == TaskStatusTimeout
}

// TicketUpdatedAt returns the record's last-mutation time, falling back to
// CreatedAt for records never touched since creation (FR-4).
func (t *Task) TicketUpdatedAt() time.Time {
	if t == nil {
		return time.Time{}
	}
	if !t.UpdatedAt.IsZero() {
		return t.UpdatedAt
	}
	return t.CreatedAt
}

// --- Public representation ------------------------------------------------

// Ticket is the canonical wire representation returned by every Ticket
// operation (FR-92). Handlers return this instead of legacy task/backlog
// envelopes so a client never has to know which internal record backs it.
//
// It carries both identifiers (FR-141): ID is the stable relationship key
// that survives every state change and migration, Number is the immutable
// workspace-local number users actually say.
type Ticket struct {
	ID     string `json:"id"`
	Number int64  `json:"number,omitempty"`
	// DisplayNumber is the rendered "#12" form, and QualifiedNumber prefixes
	// the owning workspace name for rolled-up views (FR-141).
	DisplayNumber   string `json:"display_number,omitempty"`
	QualifiedNumber string `json:"qualified_number,omitempty"`

	// OwningWorkspaceID is the mutation authority for this Ticket (FR-9). In
	// a roll-up it may differ from the workspace that served the request, and
	// item mutations must be addressed to this ID (FR-12, FR-90).
	OwningWorkspaceID   string `json:"owning_workspace_id"`
	OwningWorkspaceName string `json:"owning_workspace_name,omitempty"`

	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	State       TicketState `json:"state"`
	StateLabel  string      `json:"state_label"`
	StateRank   int64       `json:"state_rank"`

	Tags         []string   `json:"tags,omitempty"`
	Priority     int        `json:"priority"`
	DueDate      *time.Time `json:"due_date,omitempty"`
	ReferenceURL string     `json:"reference_url,omitempty"`

	Source        string   `json:"source"`
	SourceID      string   `json:"source_id,omitempty"`
	LinkedNoteIDs []string `json:"linked_note_ids,omitempty"`

	// Execution configuration retained from the underlying record (FR-5).
	// These describe how the work would run; none of them determine State.
	Assignee                string   `json:"assignee,omitempty"`
	AssignedNodeID          string   `json:"assigned_node_id,omitempty"`
	RequiredCapabilities    []string `json:"required_capabilities,omitempty"`
	ParentTicketID          string   `json:"parent_ticket_id,omitempty"`
	SubticketIndex          int      `json:"subticket_index,omitempty"`
	DependsOnTicketIDs      []string `json:"depends_on_ticket_ids,omitempty"`
	ScheduleEnabled         bool     `json:"schedule_enabled,omitempty"`
	AwaitingExecutionIntent bool     `json:"awaiting_execution_intent,omitempty"`

	// Latest-attempt signals. These belong to the Run, not the Ticket, and
	// are surfaced separately so the UI can never render Run status as
	// lifecycle state (FR-8, FR-61).
	CurrentRunID   string `json:"current_run_id,omitempty"`
	NeedsAttention bool   `json:"needs_attention,omitempty"`

	StateHistory []TicketStateChange `json:"state_history,omitempty"`
	// LegalTransitions lists the states this Ticket may move to right now, so
	// clients render only legal actions (FR-70).
	LegalTransitions []TicketState `json:"legal_transitions,omitempty"`

	// Parent and Subtickets are populated on detail reads only (FR-142).
	// Hierarchy is presented inside Ticket detail; the Board renders every
	// Ticket as an independent card and never nests them, so collection reads
	// deliberately leave these empty.
	Parent     *TicketSummary  `json:"parent,omitempty"`
	Subtickets []TicketSummary `json:"subtickets,omitempty"`

	// Archived reports that this terminal Ticket has aged past the 14-day
	// recent window (FR-143). It is a presentation flag derived at read time,
	// never a stored state: the Ticket and all its history remain intact and
	// queryable, it simply stops appearing in default views.
	Archived bool `json:"archived,omitempty"`

	// Version is the optimistic-concurrency token to echo back on mutation
	// (FR-93).
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// LegacyStatus exposes the compatibility projection for diagnostics and
	// for adapters that still speak the old vocabulary. It is never lifecycle
	// authority (FR-7, FR-97).
	LegacyStatus TaskStatus `json:"legacy_status,omitempty"`
}

// TicketSummary is the compact reference used for parent/subticket links and
// for the Note-side linked-Ticket projection (FR-75, FR-142). It carries
// enough to render and navigate to a Ticket without duplicating the full
// record — and deliberately not enough to be mistaken for a second copy of it.
type TicketSummary struct {
	ID                  string      `json:"id"`
	Number              int64       `json:"number,omitempty"`
	DisplayNumber       string      `json:"display_number,omitempty"`
	Title               string      `json:"title"`
	State               TicketState `json:"state"`
	StateLabel          string      `json:"state_label"`
	OwningWorkspaceID   string      `json:"owning_workspace_id"`
	OwningWorkspaceName string      `json:"owning_workspace_name,omitempty"`
	SubticketIndex      int         `json:"subticket_index,omitempty"`
}

// NewTicketSummary builds the compact reference for a record.
func NewTicketSummary(task *Task, owningWorkspaceID, owningWorkspaceName string) TicketSummary {
	if task == nil {
		return TicketSummary{}
	}
	state := task.CanonicalState()
	ownerID := strings.TrimSpace(owningWorkspaceID)
	if ownerID == "" {
		ownerID = task.WorkspaceID
	}
	return TicketSummary{
		ID:                  task.ID,
		Number:              task.TicketNumber,
		DisplayNumber:       FormatTicketNumber(task.TicketNumber),
		Title:               task.Description,
		State:               state,
		StateLabel:          state.Label(),
		OwningWorkspaceID:   ownerID,
		OwningWorkspaceName: owningWorkspaceName,
		SubticketIndex:      task.SubtaskIndex,
	}
}

// NewTicket builds the canonical representation of a persisted record,
// attaching the owning workspace's identity so rolled-up responses always
// carry ownership (FR-11).
func NewTicket(task *Task, owningWorkspaceID, owningWorkspaceName string) Ticket {
	if task == nil {
		return Ticket{}
	}
	state := task.CanonicalState()
	ownerID := strings.TrimSpace(owningWorkspaceID)
	if ownerID == "" {
		ownerID = task.WorkspaceID
	}

	return Ticket{
		ID:                      task.ID,
		Number:                  task.TicketNumber,
		DisplayNumber:           FormatTicketNumber(task.TicketNumber),
		QualifiedNumber:         FormatQualifiedTicketNumber(owningWorkspaceName, task.TicketNumber),
		OwningWorkspaceID:       ownerID,
		OwningWorkspaceName:     owningWorkspaceName,
		Title:                   task.Description,
		Description:             task.Details,
		State:                   state,
		StateLabel:              state.Label(),
		StateRank:               task.StateRank,
		Tags:                    append([]string(nil), task.Tags...),
		Priority:                task.Priority,
		DueDate:                 task.DueDate,
		ReferenceURL:            task.ReferenceURL,
		Source:                  task.SourceType,
		SourceID:                task.SourceID,
		LinkedNoteIDs:           append([]string(nil), task.LinkedNoteIDs...),
		Assignee:                task.To,
		AssignedNodeID:          task.AssignedNodeID,
		RequiredCapabilities:    append([]string(nil), task.RequiredCapabilities...),
		ParentTicketID:          task.ParentTaskID,
		SubticketIndex:          task.SubtaskIndex,
		DependsOnTicketIDs:      append([]string(nil), task.InputTaskIDs...),
		ScheduleEnabled:         task.ScheduleEnabled,
		AwaitingExecutionIntent: task.AwaitingExecutionIntent,
		CurrentRunID:            task.CurrentRunID,
		NeedsAttention:          task.NeedsAttention(),
		StateHistory:            append([]TicketStateChange(nil), task.StateHistory...),
		LegalTransitions:        LegalTicketTransitions(state),
		Version:                 task.TicketVersion,
		CreatedAt:               task.CreatedAt,
		UpdatedAt:               task.TicketUpdatedAt(),
		LegacyStatus:            task.Status,
	}
}
