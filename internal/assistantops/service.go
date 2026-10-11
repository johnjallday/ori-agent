package assistantops

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Guard answers, fresh on every call, what only the host can know. Nothing it
// returns is cached by the service. An answer that wraps ErrInvalidated is
// definitive (the thing is gone or no longer this owner's) and ends the
// operation's continuation; any other error means "could not check" and
// changes nothing.
type Guard interface {
	// CurrentUser is the server-resolved caller.
	CurrentUser(ctx context.Context) (string, error)
	// Relationship resolves that user's hired assistant when it is ready for
	// work. ConversationID and TurnMessageID are left empty.
	Relationship(ctx context.Context, userID string) (Owner, error)
	// ConversationOwned confirms the conversation still exists and still
	// belongs to that owner.
	ConversationOwned(ctx context.Context, owner Owner) error
	// Workspace re-reads the accepted workspace for the user and returns its
	// current slug and name.
	Workspace(ctx context.Context, userID, workspaceID string) (Context, error)
	// Agent confirms that this exact agent instance is in that workspace's own
	// roster right now and returns its canonical name and node. A namesake in
	// the app-wide roster, or another workspace's instance, is not a match.
	Agent(ctx context.Context, userID, workspaceID, agentName, nodeID string) (name, node string, err error)
}

// Service owns operation lifecycle and owner-scoped access. It executes
// nothing: it records what was requested, what a tab observed and what the
// host confirmed.
type Service struct {
	store Store
	guard Guard
	// tasks reads and starts the one task a task operation is bound to. Nil
	// until SetTaskHost: without it a draft can be prepared but not committed.
	tasks TaskHost
	now   func() time.Time
	newID func() string
	ttl   time.Duration
	// startGrace is how long an accepted Start may show no run before the
	// operation says its outcome is unresolved.
	startGrace time.Duration
	// flights counts the create and start claims this process is carrying out
	// right now, so a concurrent read does not mistake one for an abandoned
	// claim. A claim absent from it after a restart is settled from the task.
	flightMu sync.Mutex
	flights  map[string]int
}

// NewService wires the store and the host guard.
func NewService(store Store, guard Guard) *Service {
	return &Service{store: store, guard: guard, now: time.Now, newID: uuid.NewString, ttl: DefaultTTL,
		startGrace: DefaultStartGrace, flights: map[string]int{}}
}

// Available reports whether operations can be offered at all.
func (s *Service) Available() bool { return s != nil && s.store != nil && s.guard != nil }

// CreateRequest is a validated request for one operation. The caller (the
// assistant's tool path) has already resolved every identity in it; Create
// checks them again before anything is stored.
type CreateRequest struct {
	Owner   Owner
	Kind    Kind
	Context Context
	// TabID is the originating tab. Watch says whether that tab chose to let
	// this operation move its view.
	TabID string
	Watch bool
	// Actions are the named browser steps, in order. Their arguments come
	// from Context, never from the caller's free text.
	Actions []Action
	// Draft is the task a KindTask operation prepares: a title and a brief for
	// the agent in Context. Preparing it writes no task and starts nothing.
	Draft *Draft
}

// Create stores a new operation and returns the originating tab's snapshot.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Snapshot, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if err := req.Owner.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Owner.ConversationID) == "" {
		return nil, fmt.Errorf("%w: a stored conversation is required", ErrInvalidCommand)
	}
	if req.TabID != "" && !ValidTabID(req.TabID) {
		return nil, fmt.Errorf("%w: tab identity is malformed", ErrInvalidCommand)
	}
	if req.Watch && req.TabID == "" {
		return nil, fmt.Errorf("%w: watching needs a tab", ErrInvalidCommand)
	}
	if len(req.Actions) == 0 || len(req.Actions) > MaxCommands {
		return nil, fmt.Errorf("%w: between 1 and %d actions", ErrBounds, MaxCommands)
	}
	fresh, err := s.verify(ctx, req.Owner, req.Context.WorkspaceID)
	if err != nil {
		return nil, err
	}
	accepted := req.Context
	accepted.WorkspaceID, accepted.WorkspaceSlug, accepted.WorkspaceName = fresh.WorkspaceID, fresh.WorkspaceSlug, fresh.WorkspaceName
	accepted.AgentName, accepted.AgentNodeID = strings.TrimSpace(accepted.AgentName), strings.TrimSpace(accepted.AgentNodeID)

	now := s.now().UTC().Truncate(time.Millisecond)
	op := &Operation{
		Version: ContractVersion, ID: s.newID(), Kind: req.Kind, Status: StatusRequested, Revision: 1,
		Owner: req.Owner, Context: accepted, TabID: req.TabID, Watch: req.Watch,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(s.ttl),
	}
	if (req.Kind == KindTask) != (req.Draft != nil) {
		return nil, fmt.Errorf("%w: a task operation carries exactly one draft", ErrInvalidCommand)
	}
	if req.Draft != nil {
		// The assignment is this workspace's own roster instance, checked now.
		// An agent that is not there is never replaced by a namesake or by the
		// workspace's entry agent.
		name, node, err := s.guard.Agent(ctx, req.Owner.UserID, accepted.WorkspaceID, accepted.AgentName, accepted.AgentNodeID)
		if err != nil {
			return nil, guardError(err)
		}
		if strings.TrimSpace(name) == "" || strings.TrimSpace(node) == "" {
			return nil, fmt.Errorf("%w: the task needs an agent in this workspace", ErrInvalidated)
		}
		accepted.AgentName, accepted.AgentNodeID = name, node
		op.Context = accepted
		op.Draft = &Draft{
			Revision: 1, Title: strings.TrimSpace(req.Draft.Title), Brief: strings.TrimSpace(req.Draft.Brief),
			AgentName: name, AgentNodeID: node,
		}
		// Preparing is a server fact of its own: a draft revision now exists.
		// It is not a task, and no browser has shown it yet.
		op.Status = StatusPrepared
		appendReceipt(op, Receipt{Kind: ReceiptDraftPrepared, At: now, Ref: "1"})
	}
	for i, action := range req.Actions {
		command := Command{ID: s.newID(), Sequence: i + 1, Action: action, Status: CommandPending, IssuedAt: now,
			WorkspaceID: accepted.WorkspaceID, WorkspaceSlug: accepted.WorkspaceSlug}
		switch action {
		case ActionOpenWorkspace:
		case ActionSelectAgent:
			if accepted.AgentName == "" {
				return nil, fmt.Errorf("%w: selecting an agent needs a resolved agent", ErrInvalidCommand)
			}
			command.AgentName, command.AgentNodeID = accepted.AgentName, accepted.AgentNodeID
		case ActionOpenTaskDraft:
			if op.Draft == nil {
				return nil, fmt.Errorf("%w: there is no draft to open", ErrInvalidCommand)
			}
			command.DraftRevision = op.Draft.Revision
			command.AgentName, command.AgentNodeID = op.Draft.AgentName, op.Draft.AgentNodeID
		default:
			// Task commands are issued by their own transitions, which bind
			// them to a task.
			return nil, fmt.Errorf("%w: %q cannot start an operation", ErrInvalidCommand, action)
		}
		op.Commands = append(op.Commands, command)
	}
	if err := op.Validate(); err != nil {
		return nil, err
	}

	// Cleanup is bounded and never decides the request: a failed sweep leaves
	// old rows for the next one.
	_, _ = s.store.Sweep(ctx, now, CleanupBatch)
	if err := s.makeRoom(ctx, req.Owner.UserID, req.TabID, now); err != nil {
		return nil, err
	}
	if err := s.store.Insert(ctx, op); err != nil {
		return nil, err
	}
	return SnapshotFor(op, req.TabID), nil
}

// makeRoom retires what a new operation replaces. Commands still pending for
// the same tab are superseded, so two operations never drive one view, and the
// oldest operations end when the per-user bound is reached.
func (s *Service) makeRoom(ctx context.Context, userID, tabID string, now time.Time) error {
	open, err := s.store.OpenForUser(ctx, userID)
	if err != nil {
		return err
	}
	remaining := len(open)
	for _, existing := range open {
		if tabID != "" && existing.TabID == tabID && len(existing.PendingCommands()) > 0 {
			supersedePending(existing, now, "replaced by a newer request")
			existing.UpdatedAt = now
			if _, err := s.store.Update(ctx, existing, existing.Revision); err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
	for _, existing := range open {
		if remaining < MaxActivePerUser {
			break
		}
		// A start that is being decided is never retired from under itself.
		if existing.Status == StatusStartRequested || existing.Status == StatusUnresolved {
			continue
		}
		current, err := s.store.Get(ctx, existing.ID)
		if err != nil {
			continue
		}
		if _, err := s.end(ctx, current, StatusExpired, "retired to make room for a newer request"); err == nil {
			remaining--
		}
	}
	if remaining >= MaxActivePerUser {
		return fmt.Errorf("%w: too many operations are still open", ErrBounds)
	}
	return nil
}

// Snapshot returns the operation as tabID may see it, after revalidating it.
// An ended operation is still readable by its user, so a returning tab can be
// told why it ended.
func (s *Service) Snapshot(ctx context.Context, id, tabID string) (*Snapshot, error) {
	if tabID != "" && !ValidTabID(tabID) {
		return nil, fmt.Errorf("%w: tab identity is malformed", ErrInvalidCommand)
	}
	op, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if op.Status.Terminal() {
		return SnapshotFor(op, tabID), nil
	}
	op, err = s.revalidate(ctx, op)
	if err != nil && !ended(err) {
		return nil, err
	}
	if err == nil {
		// A read is also where a claim left by a lost response is settled, from
		// what the task itself now says.
		if op, err = s.settle(ctx, op); err != nil {
			return nil, err
		}
	}
	return SnapshotFor(op, tabID), nil
}

// Acknowledge records what the lease-holding tab observed for one command. A
// repeat of an already-recorded outcome returns the current snapshot and
// writes nothing. It is view evidence only and never authorizes a write.
func (s *Service) Acknowledge(ctx context.Context, ack Acknowledgement) (*Snapshot, error) {
	if err := ack.Validate(); err != nil {
		return nil, err
	}
	op, err := s.loadOpen(ctx, ack.OperationID)
	if err != nil {
		return nil, err
	}
	if !op.Watch || op.TabID != ack.TabID {
		return nil, ErrTabMismatch
	}
	command, ok := op.Command(ack.CommandID)
	if !ok {
		return nil, fmt.Errorf("%w: unknown command", ErrInvalidCommand)
	}
	if command.Status != CommandPending {
		if command.Status == ack.Outcome {
			return SnapshotFor(op, ack.TabID), nil
		}
		return nil, fmt.Errorf("%w: the command was already settled", ErrConflict)
	}
	if ack.Revision != op.Revision {
		return nil, ErrConflict
	}
	for _, earlier := range op.Commands {
		if earlier.Sequence < command.Sequence && earlier.Status == CommandPending {
			return nil, fmt.Errorf("%w: an earlier step is still pending", ErrConflict)
		}
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	command.SettledAt, command.Status = &now, ack.Outcome
	if ack.Outcome == CommandAcknowledged {
		if err := observedMatches(*command, ack.Observed); err != nil {
			return nil, err
		}
		command.Observed = ack.Observed
		appendReceipt(op, Receipt{Kind: viewReceipt(command.Action), At: now, Ref: command.ID})
	} else {
		command.Reason = boundedReason(ack.Reason)
		appendReceipt(op, Receipt{Kind: ReceiptTargetMissing, At: now, Ref: command.ID, Reason: command.Reason})
		// The steps after a missing target cannot run. The manual route stays.
		for i := range op.Commands {
			if op.Commands[i].Sequence > command.Sequence && op.Commands[i].Status == CommandPending {
				op.Commands[i].Status, op.Commands[i].SettledAt = CommandSuperseded, &now
				op.Commands[i].Reason = "an earlier step was unavailable"
			}
		}
	}
	op.UpdatedAt = now
	stored, err := s.store.Update(ctx, op, op.Revision)
	if err != nil {
		return nil, err
	}
	return SnapshotFor(stored, ack.TabID), nil
}

// SetWatch turns watching on or off for one tab. Taking the lease from another
// tab needs adopt, an explicit choice in the adopting tab; turning it off
// cancels nothing.
func (s *Service) SetWatch(ctx context.Context, id, tabID string, revision int64, watch, adopt bool) (*Snapshot, error) {
	if !ValidTabID(tabID) {
		return nil, fmt.Errorf("%w: tab identity is malformed", ErrInvalidCommand)
	}
	op, err := s.loadOpen(ctx, id)
	if err != nil {
		return nil, err
	}
	holder := op.TabID == tabID
	if !holder && op.TabID != "" && (!watch || !adopt) {
		return nil, ErrTabMismatch
	}
	if holder && op.Watch == watch {
		return SnapshotFor(op, tabID), nil
	}
	if revision != op.Revision {
		return nil, ErrConflict
	}
	op.TabID, op.Watch, op.UpdatedAt = tabID, watch, s.now().UTC().Truncate(time.Millisecond)
	stored, err := s.store.Update(ctx, op, op.Revision)
	if err != nil {
		return nil, err
	}
	return SnapshotFor(stored, tabID), nil
}

// Cancel ends the operation's continuation at the user's request. A task it
// created or started is untouched: cancelling work is the task's own action.
func (s *Service) Cancel(ctx context.Context, id, tabID string, revision int64) (*Snapshot, error) {
	if tabID != "" && !ValidTabID(tabID) {
		return nil, fmt.Errorf("%w: tab identity is malformed", ErrInvalidCommand)
	}
	op, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if op.Status == StatusCancelled {
		return SnapshotFor(op, tabID), nil
	}
	if op.Status.Terminal() {
		return nil, statusError(op.Status)
	}
	if !CanTransition(op.Status, StatusCancelled) {
		return nil, ErrTransition
	}
	if revision != op.Revision {
		return nil, ErrConflict
	}
	stored, err := s.end(ctx, op, StatusCancelled, "dismissed")
	if err != nil {
		return nil, err
	}
	return SnapshotFor(stored, tabID), nil
}

// load reads an operation for the current user. Another user's operation is
// reported exactly like one that does not exist, so an ID cannot probe.
func (s *Service) load(ctx context.Context, id string) (*Operation, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	userID, err := s.guard.CurrentUser(ctx)
	if err != nil || strings.TrimSpace(userID) == "" {
		return nil, ErrUnavailable
	}
	op, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if op.Owner.UserID != userID {
		return nil, ErrNotFound
	}
	if op.Version != ContractVersion {
		return nil, ErrUnavailable
	}
	return op, nil
}

// loadOpen is load plus revalidation, for anything that changes the operation.
func (s *Service) loadOpen(ctx context.Context, id string) (*Operation, error) {
	op, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if op.Status.Terminal() {
		return nil, statusError(op.Status)
	}
	return s.revalidate(ctx, op)
}

// revalidate reruns every ownership and scope check against the host's
// current state. A definitive failure ends the operation and returns it with
// the matching error; "could not check" changes nothing.
func (s *Service) revalidate(ctx context.Context, op *Operation) (*Operation, error) {
	if !s.now().Before(op.ExpiresAt) {
		closed, err := s.end(ctx, op, StatusExpired, "the operation ran out of time")
		if err != nil {
			return nil, err
		}
		return closed, ErrExpired
	}
	fresh, err := s.verify(ctx, op.Owner, op.Context.WorkspaceID)
	if err != nil {
		if !errors.Is(err, ErrInvalidated) {
			return nil, err
		}
		closed, endErr := s.end(ctx, op, StatusInvalidated, "the assistant, conversation or workspace changed")
		if endErr != nil {
			return nil, endErr
		}
		return closed, ErrInvalidated
	}
	if fresh.WorkspaceSlug == op.Context.WorkspaceSlug && fresh.WorkspaceName == op.Context.WorkspaceName {
		return op, nil
	}
	// A rename keeps the operation but not its old address: pending commands
	// follow the workspace's current slug.
	op.Context.WorkspaceSlug, op.Context.WorkspaceName = fresh.WorkspaceSlug, fresh.WorkspaceName
	for i := range op.Commands {
		if op.Commands[i].Status == CommandPending {
			op.Commands[i].WorkspaceSlug = fresh.WorkspaceSlug
		}
	}
	op.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)
	stored, err := s.store.Update(ctx, op, op.Revision)
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// verify checks an owner and workspace against the host and returns the
// workspace's current address.
func (s *Service) verify(ctx context.Context, owner Owner, workspaceID string) (Context, error) {
	userID, err := s.guard.CurrentUser(ctx)
	if err != nil || strings.TrimSpace(userID) == "" {
		return Context{}, ErrUnavailable
	}
	if userID != owner.UserID {
		return Context{}, ErrForbidden
	}
	current, err := s.guard.Relationship(ctx, userID)
	if err != nil {
		return Context{}, guardError(err)
	}
	if current.AssistantID != owner.AssistantID || current.StateVersion != owner.StateVersion ||
		current.HQWorkspaceID != owner.HQWorkspaceID || !strings.EqualFold(current.ProfileName, owner.ProfileName) {
		return Context{}, fmt.Errorf("%w: the assistant relationship changed", ErrInvalidated)
	}
	if err := s.guard.ConversationOwned(ctx, owner); err != nil {
		return Context{}, guardError(err)
	}
	fresh, err := s.guard.Workspace(ctx, userID, workspaceID)
	if err != nil {
		return Context{}, guardError(err)
	}
	if fresh.WorkspaceID != workspaceID || strings.TrimSpace(fresh.WorkspaceSlug) == "" {
		return Context{}, fmt.Errorf("%w: the workspace cannot be opened", ErrInvalidated)
	}
	return fresh, nil
}

// end moves an operation to a terminal status, supersedes what was pending
// and records why. If another request ended it first, that result stands.
func (s *Service) end(ctx context.Context, op *Operation, status Status, reason string) (*Operation, error) {
	if !CanTransition(op.Status, status) {
		return nil, ErrTransition
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	supersedePending(op, now, reason)
	if !op.TaskConfirmed() {
		// A reservation that was never confirmed is not a task. An ended
		// operation must not go on naming one that may never have been written.
		op.Task = nil
	}
	op.Status, op.UpdatedAt = status, now
	appendReceipt(op, Receipt{Kind: ReceiptContinuationEnd, At: now, Ref: string(status), Reason: boundedReason(reason)})
	stored, err := s.store.Update(ctx, op, op.Revision)
	if errors.Is(err, ErrConflict) {
		current, getErr := s.store.Get(ctx, op.ID)
		if getErr == nil && current.Status.Terminal() {
			return current, nil
		}
	}
	return stored, err
}

func supersedePending(op *Operation, now time.Time, reason string) {
	for i := range op.Commands {
		if op.Commands[i].Status == CommandPending {
			op.Commands[i].Status, op.Commands[i].SettledAt = CommandSuperseded, &now
			op.Commands[i].Reason = boundedReason(reason)
		}
	}
}

// appendReceipt keeps the newest facts within MaxReceipts. View facts are
// dropped first, then the oldest answers to Start (a retried Start adds one
// each time). That a task was created and that a run was observed are kept
// for as long as anything else can go.
func appendReceipt(op *Operation, receipt Receipt) {
	op.Receipts = append(op.Receipts, receipt)
	for len(op.Receipts) > MaxReceipts {
		older := op.Receipts[:len(op.Receipts)-1]
		drop := -1
		for _, expendable := range []func(ReceiptKind) bool{viewFact, startAnswer} {
			for i, existing := range older {
				if expendable(existing.Kind) {
					drop = i
					break
				}
			}
			if drop >= 0 {
				break
			}
		}
		if drop < 0 {
			drop = 0
		}
		op.Receipts = append(op.Receipts[:drop], op.Receipts[drop+1:]...)
	}
}

func startAnswer(kind ReceiptKind) bool {
	switch kind {
	case ReceiptStartRefused, ReceiptStartAccepted, ReceiptStartUnresolved:
		return true
	}
	return false
}

func viewFact(kind ReceiptKind) bool {
	switch kind {
	case ReceiptViewOpened, ReceiptAgentSelected, ReceiptDraftOpened, ReceiptTargetMissing, ReceiptDraftPrepared:
		return true
	}
	return false
}

func viewReceipt(action Action) ReceiptKind {
	switch action {
	case ActionSelectAgent:
		return ReceiptAgentSelected
	case ActionOpenTaskDraft:
		return ReceiptDraftOpened
	}
	return ReceiptViewOpened
}

// observedMatches refuses an acknowledgement whose evidence does not describe
// the command. It cannot prove what a browser shows; it does stop a tab from
// reporting arrival somewhere else as arrival here.
func observedMatches(command Command, observed *Observation) error {
	if observed == nil {
		return fmt.Errorf("%w: an acknowledgement needs what was observed", ErrInvalidCommand)
	}
	switch command.Action {
	case ActionOpenWorkspace:
		path, err := url.PathUnescape(strings.TrimRight(observed.Path, "/"))
		if err != nil || path != "/workspaces/"+command.WorkspaceSlug {
			return fmt.Errorf("%w: the observed page is not that workspace", ErrInvalidCommand)
		}
	case ActionSelectAgent:
		if foldSpace(observed.AgentKey) == "" || foldSpace(observed.AgentKey) != foldSpace(command.AgentName) {
			return fmt.Errorf("%w: the observed selection is not that agent", ErrInvalidCommand)
		}
	case ActionOpenTaskDraft:
		if observed.DraftRevision != command.DraftRevision {
			return fmt.Errorf("%w: the observed draft is another revision", ErrInvalidCommand)
		}
	case ActionOpenTask:
		if observed.TaskID == "" || observed.TaskID != command.TaskID {
			return fmt.Errorf("%w: the observed task is another task", ErrInvalidCommand)
		}
	}
	return nil
}

func foldSpace(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

// boundedReason keeps a reason printable and within MaxReasonRunes.
func boundedReason(value string) string {
	value = strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsControl(r) }), " ")
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= MaxReasonRunes {
		return value
	}
	return string([]rune(value)[:MaxReasonRunes])
}

// guardError keeps a definitive answer and reduces everything else to
// "could not check", so a storage error never ends an operation.
func guardError(err error) error {
	if errors.Is(err, ErrInvalidated) {
		return err
	}
	return ErrUnavailable
}

func statusError(status Status) error {
	switch status {
	case StatusExpired:
		return ErrExpired
	case StatusInvalidated:
		return ErrInvalidated
	}
	return ErrTransition
}

// ended reports an error that means the operation was just ended, where the
// ended record is still worth showing.
func ended(err error) bool {
	return errors.Is(err, ErrExpired) || errors.Is(err, ErrInvalidated)
}
