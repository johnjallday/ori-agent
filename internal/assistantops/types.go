// Package assistantops is the bounded contract for "Operate Ori": an operation
// the hired personal assistant prepares in a conversation and the browser
// carries out through existing page controllers. The host owns every identity
// here. Model output, browser acknowledgements and conversation history never
// supply authority: a browser acknowledgement moves a view, a server receipt
// records a write, and task state alone establishes execution.
package assistantops

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ContractVersion is the version carried by every stored operation and every
// snapshot sent to the browser. A reader that sees another version treats the
// record as unavailable, never as a fresh operation.
const ContractVersion = 1

// Bounds. They are enforced before anything is stored or sent.
const (
	// MaxCommands is how many browser commands one operation may hold in its
	// lifetime. An operation is one journey, not a queue.
	MaxCommands = 8
	// MaxReceipts caps the server-recorded facts kept with an operation.
	MaxReceipts = 16
	// MaxDraftTitleRunes and MaxDraftBriefRunes bound the only draft fields v1
	// prepares. They match the task composer's description/details use.
	MaxDraftTitleRunes = 200
	MaxDraftBriefRunes = 4000
	// MaxReasonRunes bounds any human-readable reason stored or shown.
	MaxReasonRunes = 240
	// MaxActivePerUser bounds retention: older open operations expire first.
	MaxActivePerUser = 20
	// DefaultTTL is how long an operation may continue. Expiry invalidates
	// continuation; it never starts or cancels a task.
	DefaultTTL = 24 * time.Hour
	// DefaultStartGrace is how long an accepted Start may show no run before
	// the operation reports the outcome as unresolved.
	DefaultStartGrace = 90 * time.Second
	// CleanupBatch bounds how many expired rows one sweep removes.
	CleanupBatch = 50
	// PurgeAfter is how long an ended operation stays readable, so a returning
	// tab can be told why it ended instead of finding nothing.
	PurgeAfter = 24 * time.Hour
	// MaxTabIDLen bounds the opaque per-tab identity a browser sends.
	MaxTabIDLen = 64
	// MaxObservedPathRunes bounds the path a tab reports on arrival.
	MaxObservedPathRunes = 300
	// Stored JSON columns are bounded in bytes; the schema repeats these checks.
	MaxContextBytes  = 4000
	MaxCommandsBytes = 16000
	MaxDraftBytes    = 24000
	MaxReceiptsBytes = 16000
)

// ValidTabID reports whether id is a well-formed opaque tab identity. The
// value identifies a tab for Watch delivery only; it is never authority.
func ValidTabID(id string) bool {
	if id == "" || len(id) > MaxTabIDLen {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// Kind names the journey an operation belongs to.
type Kind string

const (
	// KindNavigation opens a workspace and selects an agent (group 1).
	KindNavigation Kind = "navigation"
	// KindTask additionally prepares, reviews and starts one task (groups 2-4).
	KindTask Kind = "task"
)

// Status is the operation's own lifecycle. Task outcome is never copied into
// it: completion, failure and needs-input are read from the task each time.
type Status string

const (
	// StatusRequested: created by a validated tool call; commands may be pending.
	StatusRequested Status = "requested"
	// StatusPrepared: a draft revision exists and was handed to the composer.
	StatusPrepared Status = "prepared"
	// StatusReviewed: the composer's save created the task bound to this operation.
	StatusReviewed Status = "reviewed"
	// StatusStartRequested: Start was claimed once for the bound task.
	StatusStartRequested Status = "start_requested"
	// StatusStarted: execution was established (a run was observed).
	StatusStarted Status = "started"
	// StatusUnresolved: a start was claimed but its outcome could not be
	// proven; only an explicit inspection or retry through the claim may follow.
	StatusUnresolved Status = "unresolved"
	// StatusCancelled: the user dismissed the operation. The task, if any, is untouched.
	StatusCancelled Status = "cancelled"
	// StatusExpired: TTL or retention ended continuation.
	StatusExpired Status = "expired"
	// StatusInvalidated: the owner, conversation, relationship or workspace
	// access changed under it.
	StatusInvalidated Status = "invalidated"
)

// terminal statuses accept no further transition.
func (s Status) Terminal() bool {
	switch s {
	case StatusCancelled, StatusExpired, StatusInvalidated:
		return true
	}
	return false
}

// transitions is the closed state machine. Anything absent is refused.
var transitions = map[Status][]Status{
	StatusRequested: {StatusPrepared, StatusReviewed, StatusCancelled, StatusExpired, StatusInvalidated},
	StatusPrepared:  {StatusPrepared, StatusReviewed, StatusCancelled, StatusExpired, StatusInvalidated},
	// reviewed -> started is a start the operation did not make: the user ran
	// the task from its own controls, and the task says so.
	StatusReviewed:       {StatusStartRequested, StatusStarted, StatusCancelled, StatusExpired, StatusInvalidated},
	StatusStartRequested: {StatusStarted, StatusReviewed, StatusUnresolved, StatusExpired, StatusInvalidated},
	StatusUnresolved:     {StatusStarted, StatusStartRequested, StatusReviewed, StatusCancelled, StatusExpired, StatusInvalidated},
	StatusStarted:        {StatusCancelled, StatusExpired, StatusInvalidated},
}

// CanTransition reports whether the lifecycle permits from -> to. A repeated
// prepare (prepared -> prepared) is a new draft revision, not a new operation.
func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// Action names the only things the browser may be asked to do. The browser
// maps each to one existing page controller; it never receives JavaScript,
// selectors, arbitrary URLs or coordinates.
type Action string

const (
	// ActionOpenWorkspace navigates to the workspace page named by a
	// server-resolved slug. Acknowledged on arrival, never when queued.
	ActionOpenWorkspace Action = "open_workspace"
	// ActionSelectAgent selects one roster agent on the open workspace page.
	ActionSelectAgent Action = "select_agent"
	// ActionOpenTaskDraft opens the task composer prefilled from the
	// operation's current draft revision (group 2).
	ActionOpenTaskDraft Action = "open_task_draft"
)

// Known reports whether a is one of the named actions. Opening a task or its
// result is not one: the user opens those from the drawer, on request, so no
// command ever pushes a result view onto a tab.
func (a Action) Known() bool {
	switch a {
	case ActionOpenWorkspace, ActionSelectAgent, ActionOpenTaskDraft:
		return true
	}
	return false
}

// CommandStatus is what the originating tab observed for one command.
type CommandStatus string

const (
	CommandPending      CommandStatus = "pending"
	CommandAcknowledged CommandStatus = "acknowledged"
	// CommandUnavailable: the page or its target could not take the command
	// (missing control, wrong page, unsaved editor). The manual route remains.
	CommandUnavailable CommandStatus = "unavailable"
	// CommandSuperseded: a later command or revision replaced it.
	CommandSuperseded CommandStatus = "superseded"
)

// Command is one named browser action. Its arguments are host-resolved
// references, so the browser can verify them against the page it is on.
type Command struct {
	ID       string        `json:"id"`
	Sequence int           `json:"sequence"`
	Action   Action        `json:"action"`
	Status   CommandStatus `json:"status"`
	// WorkspaceID and WorkspaceSlug name the page. The browser builds the URL
	// from the slug through its existing route helper and never from free text.
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceSlug string `json:"workspace_slug,omitempty"`
	// AgentName and AgentNodeID name the exact roster instance to select.
	AgentName   string `json:"agent_name,omitempty"`
	AgentNodeID string `json:"agent_node_id,omitempty"`
	// DraftRevision binds open_task_draft to one prepared revision.
	DraftRevision int `json:"draft_revision,omitempty"`
	// Observed is what the browser reported with its acknowledgement.
	Observed  *Observation `json:"observed,omitempty"`
	IssuedAt  time.Time    `json:"issued_at"`
	SettledAt *time.Time   `json:"settled_at,omitempty"`
	// Reason explains unavailable, bounded and user-safe.
	Reason string `json:"reason,omitempty"`
}

// Observation is the bounded evidence a tab attaches to an acknowledgement.
type Observation struct {
	Path          string `json:"path,omitempty"`
	AgentKey      string `json:"agent_key,omitempty"`
	DraftRevision int    `json:"draft_revision,omitempty"`
}

// Acknowledgement is the only thing a browser may send about a command. It
// carries the operation's current revision, so a stale tab cannot settle a
// command the server has since superseded.
type Acknowledgement struct {
	OperationID string        `json:"operation_id"`
	CommandID   string        `json:"command_id"`
	Revision    int64         `json:"revision"`
	TabID       string        `json:"tab_id"`
	Outcome     CommandStatus `json:"outcome"`
	Observed    *Observation  `json:"observed,omitempty"`
	Reason      string        `json:"reason,omitempty"`
}

// Validate refuses anything but a well-formed acknowledged/unavailable outcome.
func (a Acknowledgement) Validate() error {
	if strings.TrimSpace(a.OperationID) == "" || strings.TrimSpace(a.CommandID) == "" || strings.TrimSpace(a.TabID) == "" {
		return fmt.Errorf("%w: operation, command and tab are required", ErrInvalidCommand)
	}
	if a.Revision <= 0 {
		return fmt.Errorf("%w: revision is required", ErrInvalidCommand)
	}
	if !ValidTabID(a.TabID) {
		return fmt.Errorf("%w: tab identity is malformed", ErrInvalidCommand)
	}
	if a.Outcome != CommandAcknowledged && a.Outcome != CommandUnavailable {
		return fmt.Errorf("%w: outcome must be acknowledged or unavailable", ErrInvalidCommand)
	}
	if o := a.Observed; o != nil && (utf8.RuneCountInString(o.Path) > MaxObservedPathRunes ||
		utf8.RuneCountInString(o.AgentKey) > MaxReasonRunes) {
		return fmt.Errorf("%w: observation too long", ErrBounds)
	}
	if utf8.RuneCountInString(a.Reason) > MaxReasonRunes {
		return fmt.Errorf("%w: reason too long", ErrBounds)
	}
	return nil
}

// ReceiptKind names a server-recorded fact. View changes are browser
// acknowledgements; writes and execution facts are recorded by the server.
type ReceiptKind string

const (
	ReceiptViewOpened      ReceiptKind = "view_opened"        // from an acknowledged open_workspace
	ReceiptAgentSelected   ReceiptKind = "agent_selected"     // from an acknowledged select_agent
	ReceiptDraftPrepared   ReceiptKind = "draft_prepared"     // server: a draft revision was issued
	ReceiptDraftOpened     ReceiptKind = "draft_opened"       // from an acknowledged open_task_draft
	ReceiptTaskCreated     ReceiptKind = "task_created"       // server: canonical create bound a task
	ReceiptStartAccepted   ReceiptKind = "start_accepted"     // server: Start claim accepted by execution
	ReceiptStartRefused    ReceiptKind = "start_refused"      // server: Start claim refused
	ReceiptStartUnresolved ReceiptKind = "start_unresolved"   // server: a claimed Start showed no outcome
	ReceiptRunEstablished  ReceiptKind = "run_established"    // server: task reported a run
	ReceiptTargetMissing   ReceiptKind = "target_unavailable" // from an unavailable command
	ReceiptContinuationEnd ReceiptKind = "continuation_ended" // server: cancelled, expired or invalidated
)

// Receipt is one recorded fact. Ref names the task, run or command it is about.
type Receipt struct {
	Kind   ReceiptKind `json:"kind"`
	At     time.Time   `json:"at"`
	Ref    string      `json:"ref,omitempty"`
	Reason string      `json:"reason,omitempty"`
}

// Owner is the server-resolved scope an operation is bound to. Every read,
// acknowledgement and claim revalidates it; a mismatch invalidates
// continuation rather than retargeting it.
type Owner struct {
	UserID        string `json:"user_id"`
	AssistantID   string `json:"assistant_id"`
	StateVersion  int64  `json:"state_version"`
	HQWorkspaceID string `json:"hq_workspace_id"`
	ProfileName   string `json:"profile_name"`
	// ConversationID and TurnMessageID bind the operation to the stored turn
	// that requested it. A replaced or deleted conversation invalidates it.
	ConversationID string `json:"conversation_id"`
	TurnMessageID  string `json:"turn_message_id,omitempty"`
}

func (o Owner) Validate() error {
	if strings.TrimSpace(o.UserID) == "" || strings.TrimSpace(o.AssistantID) == "" || o.StateVersion <= 0 ||
		strings.TrimSpace(o.HQWorkspaceID) == "" || strings.TrimSpace(o.ProfileName) == "" {
		return fmt.Errorf("%w: owner is incomplete", ErrForbidden)
	}
	return nil
}

// Context is the accepted workspace and agent the operation acts on. Both are
// canonical IDs resolved by the host from the turn's pinned scope or roster.
type Context struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug"`
	WorkspaceName string `json:"workspace_name"`
	AgentName     string `json:"agent_name,omitempty"`
	AgentNodeID   string `json:"agent_node_id,omitempty"`
}

// Draft is the only task content v1 prepares: a title, a brief and the
// assignment. A human edit in the composer supersedes it; the server keeps
// the revision so a late command cannot reapply an older one.
type Draft struct {
	Revision    int    `json:"revision"`
	Title       string `json:"title"`
	Brief       string `json:"brief,omitempty"`
	AgentName   string `json:"agent_name,omitempty"`
	AgentNodeID string `json:"agent_node_id,omitempty"`
}

func (d Draft) Validate() error {
	if d.Revision <= 0 {
		return fmt.Errorf("%w: draft revision is required", ErrConflict)
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("%w: a task title is required", ErrBounds)
	}
	if utf8.RuneCountInString(d.Title) > MaxDraftTitleRunes {
		return fmt.Errorf("%w: title exceeds %d characters", ErrBounds, MaxDraftTitleRunes)
	}
	if utf8.RuneCountInString(d.Brief) > MaxDraftBriefRunes {
		return fmt.Errorf("%w: brief exceeds %d characters", ErrBounds, MaxDraftBriefRunes)
	}
	return nil
}

// TaskBinding is the canonical task identity the operation produced and the
// run the host observed for it. While the operation is still prepared the
// identity is a claim reserved for one create; it is the task once the
// operation is reviewed. RunID is the first run the task itself reported
// (current_run_id or execution history), never an acceptance, and is not
// replaced by a later rerun. ClaimedAt is when Start was last claimed.
type TaskBinding struct {
	TaskID    string     `json:"task_id"`
	RunID     string     `json:"run_id,omitempty"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
}

// Operation is the stored record. Revision is the compare-and-swap token for
// every write; TabID is the Watch lease for command delivery.
type Operation struct {
	Version   int          `json:"version"`
	ID        string       `json:"id"`
	Kind      Kind         `json:"kind"`
	Status    Status       `json:"status"`
	Revision  int64        `json:"revision"`
	Owner     Owner        `json:"owner"`
	Context   Context      `json:"context"`
	TabID     string       `json:"tab_id,omitempty"`
	Watch     bool         `json:"watch"`
	Commands  []Command    `json:"commands,omitempty"`
	Draft     *Draft       `json:"draft,omitempty"`
	Task      *TaskBinding `json:"task,omitempty"`
	Receipts  []Receipt    `json:"receipts,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	ExpiresAt time.Time    `json:"expires_at"`
}

// TaskConfirmed reports whether the bound task was confirmed by the canonical
// create. While an operation is still prepared, a task identity is only a
// claim: the write it reserves may not have happened.
func (o *Operation) TaskConfirmed() bool {
	if o == nil || o.Task == nil || o.Task.TaskID == "" {
		return false
	}
	return o.Status != StatusRequested && o.Status != StatusPrepared
}

// HasReceipt reports whether a fact of that kind was recorded.
func (o *Operation) HasReceipt(kind ReceiptKind) bool {
	if o == nil {
		return false
	}
	for _, receipt := range o.Receipts {
		if receipt.Kind == kind {
			return true
		}
	}
	return false
}

// PendingCommands returns the commands the lease holder still has to carry out.
func (o *Operation) PendingCommands() []Command {
	if o == nil {
		return nil
	}
	var pending []Command
	for _, command := range o.Commands {
		if command.Status == CommandPending {
			pending = append(pending, command)
		}
	}
	return pending
}

// Command finds a command by ID.
func (o *Operation) Command(id string) (*Command, bool) {
	if o == nil {
		return nil, false
	}
	for i := range o.Commands {
		if o.Commands[i].ID == id {
			return &o.Commands[i], true
		}
	}
	return nil, false
}

// Validate refuses a record that breaks any bound before it is stored.
func (o *Operation) Validate() error {
	if o == nil {
		return ErrNotFound
	}
	if o.Version != ContractVersion {
		return fmt.Errorf("%w: unsupported operation version %d", ErrInvalidated, o.Version)
	}
	if strings.TrimSpace(o.ID) == "" || (o.Kind != KindNavigation && o.Kind != KindTask) {
		return fmt.Errorf("%w: operation identity is incomplete", ErrInvalidCommand)
	}
	if _, ok := transitions[o.Status]; !ok && !o.Status.Terminal() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidCommand, o.Status)
	}
	if err := o.Owner.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(o.Context.WorkspaceID) == "" || strings.TrimSpace(o.Context.WorkspaceSlug) == "" {
		return fmt.Errorf("%w: a resolved workspace is required", ErrInvalidCommand)
	}
	if len(o.Commands) > MaxCommands {
		return fmt.Errorf("%w: more than %d commands", ErrBounds, MaxCommands)
	}
	seen := map[string]bool{}
	for _, command := range o.Commands {
		if !command.Action.Known() {
			return fmt.Errorf("%w: unknown action %q", ErrInvalidCommand, command.Action)
		}
		if strings.TrimSpace(command.ID) == "" || seen[command.ID] {
			return fmt.Errorf("%w: command ids must be unique", ErrInvalidCommand)
		}
		seen[command.ID] = true
		if utf8.RuneCountInString(command.Reason) > MaxReasonRunes {
			return fmt.Errorf("%w: command reason too long", ErrBounds)
		}
	}
	if o.Draft != nil {
		if err := o.Draft.Validate(); err != nil {
			return err
		}
	}
	if len(o.Receipts) > MaxReceipts {
		return fmt.Errorf("%w: more than %d receipts", ErrBounds, MaxReceipts)
	}
	for _, receipt := range o.Receipts {
		if utf8.RuneCountInString(receipt.Reason) > MaxReasonRunes {
			return fmt.Errorf("%w: receipt reason too long", ErrBounds)
		}
	}
	if o.ExpiresAt.IsZero() || !o.ExpiresAt.After(o.CreatedAt) {
		return fmt.Errorf("%w: expiry must follow creation", ErrBounds)
	}
	return nil
}

// Snapshot is what a browser sees. It carries no owner identity and no
// authority: a tab learns its own commands only while it holds the lease.
type Snapshot struct {
	Version  int     `json:"version"`
	ID       string  `json:"id"`
	Kind     Kind    `json:"kind"`
	Status   Status  `json:"status"`
	Revision int64   `json:"revision"`
	Context  Context `json:"context"`
	// Watching is true only for the tab that holds the lease. Other tabs see
	// the same facts without pending commands.
	Watching bool `json:"watching"`
	// Pending counts the steps still waiting, so a tab that is not watching
	// can offer to open them without being handed the commands.
	Pending   int          `json:"pending"`
	Commands  []Command    `json:"commands,omitempty"`
	Draft     *Draft       `json:"draft,omitempty"`
	Task      *TaskBinding `json:"task,omitempty"`
	Receipts  []Receipt    `json:"receipts,omitempty"`
	ExpiresAt time.Time    `json:"expires_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	// Outcome is what became of the run this operation started and where its
	// result is, read from the task for this response and never stored.
	Outcome *Outcome `json:"outcome,omitempty"`
}

// SnapshotFor projects an operation for one tab. Pending commands are shown
// only to the lease holder; a copied or foreign tab gets the facts alone.
func SnapshotFor(op *Operation, tabID string) *Snapshot {
	if op == nil {
		return nil
	}
	watching := op.Watch && tabID != "" && op.TabID == tabID
	snapshot := &Snapshot{
		Version: op.Version, ID: op.ID, Kind: op.Kind, Status: op.Status, Revision: op.Revision,
		Context: op.Context, Watching: watching, Draft: op.Draft,
		Receipts: op.Receipts, ExpiresAt: op.ExpiresAt, UpdatedAt: op.UpdatedAt,
	}
	// A task identity is shown only once the canonical create confirmed it. A
	// claim that is still being written is not yet a task anyone can open.
	if op.TaskConfirmed() {
		snapshot.Task = op.Task
	}
	for _, command := range op.Commands {
		if command.Status == CommandPending {
			snapshot.Pending++
		}
		if command.Status != CommandPending || watching {
			snapshot.Commands = append(snapshot.Commands, command)
		}
	}
	return snapshot
}

// Errors carry one meaning each; handlers map them to bounded responses and
// never expose storage or provider text.
var (
	// ErrNotFound: no such operation for this owner.
	ErrNotFound = errors.New("assistant operation: not found")
	// ErrConflict: the revision or draft revision is stale; read again.
	ErrConflict = errors.New("assistant operation: revision conflict")
	// ErrForbidden: the caller is not the operation's owner.
	ErrForbidden = errors.New("assistant operation: not owned by this assistant")
	// ErrExpired: TTL or retention ended continuation.
	ErrExpired = errors.New("assistant operation: expired")
	// ErrInvalidated: conversation, relationship or workspace access changed.
	ErrInvalidated = errors.New("assistant operation: no longer valid")
	// ErrTabMismatch: the tab does not hold the Watch lease.
	ErrTabMismatch = errors.New("assistant operation: this tab does not hold the watch")
	// ErrInvalidCommand: an action, ack or identity failed validation.
	ErrInvalidCommand = errors.New("assistant operation: invalid command")
	// ErrBounds: a payload, count or time bound was exceeded.
	ErrBounds = errors.New("assistant operation: bound exceeded")
	// ErrTransition: the lifecycle does not permit the requested change.
	ErrTransition = errors.New("assistant operation: transition not allowed")
	// ErrUnavailable: the host could not check ownership or scope right now.
	// Nothing was changed and nothing was invalidated; the caller may retry.
	ErrUnavailable = errors.New("assistant operation: state unavailable")
	// ErrAssignee: the task's agent is not one instance in this workspace's own
	// roster. The operation continues; the assignment needs a corrected review.
	ErrAssignee = errors.New("assistant operation: the agent is not in this workspace")
	// ErrTaskMissing: the bound task no longer exists. Definitive, like
	// ErrInvalidated, and reported by the task host.
	ErrTaskMissing = errors.New("assistant operation: the task no longer exists")
	// ErrStartRefused: the canonical start owner refused. Nothing ran.
	ErrStartRefused = errors.New("assistant operation: start refused")
	// ErrCreateInFlight: another request is creating this operation's task
	// right now. Nothing was written by this one.
	ErrCreateInFlight = errors.New("assistant operation: the task is being created")
)
