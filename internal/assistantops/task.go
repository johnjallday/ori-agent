package assistantops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TaskState is what the host reports about one task, read fresh each time.
// It is the only source for whether a task exists, who it is assigned to and
// whether it ran; the service copies none of it into authority.
type TaskState struct {
	TaskID string
	// OperationID is the stamp the canonical create wrote on the task.
	OperationID string
	AgentName   string
	AgentNodeID string
	// Status is the task's own status word, used only to word a refusal.
	Status string
	// Startable: the task is waiting and has never run.
	Startable bool
	// Ran: the task shows execution of any kind (in progress, a run identity,
	// or a recorded execution).
	Ran bool
	// RunID is the first run the task reports, when it reports one.
	RunID string

	// What follows attributes a result to an execution; see ResolveOutcome.
	// Active: the task is executing now. Waiting: it stopped for the user.
	Active  bool
	Waiting bool
	// HasResult: the task holds a result now (its latest execution's).
	HasResult bool
	// Runs are the executions the task recorded, oldest first.
	Runs []TaskRun
	// Launched: an operation's own Start was launched for this task.
	// LaunchPending: that start has not recorded an execution yet.
	// LaunchedRunAt: when set, the record with this time is that start's.
	// Launched with neither means the start will never record one.
	Launched      bool
	LaunchPending bool
	LaunchedRunAt time.Time
}

// TaskHost is the host's task owner as the service needs it. Task returns an
// error wrapping ErrTaskMissing when the task is definitely gone; any other
// error means "could not check". Start asks the canonical start owner to run
// a waiting task once and never reruns a finished one; a refusal is a
// *StartRefusal with wording safe to show.
type TaskHost interface {
	Task(ctx context.Context, workspaceID, taskID string) (TaskState, error)
	Start(ctx context.Context, workspaceID, taskID string) error
}

// StartRefusal is the start owner saying no, in words a user may read.
type StartRefusal struct{ Reason string }

func (r *StartRefusal) Error() string { return ErrStartRefused.Error() + ": " + r.Reason }
func (r *StartRefusal) Unwrap() error { return ErrStartRefused }

// Refusal is a Start the operation did not make, or that its owner declined.
// Kind is ErrStartRefused or ErrAssignee; Reason is bounded text the user may
// read. The operation keeps its task and stays where it was.
type Refusal struct {
	Kind   error
	Reason string
}

func (r *Refusal) Error() string { return r.Kind.Error() + ": " + r.Reason }
func (r *Refusal) Unwrap() error { return r.Kind }

// SetTaskHost wires task creation and Start. It is set once, at startup.
func (s *Service) SetTaskHost(host TaskHost) {
	if s != nil {
		s.tasks = host
	}
}

// TasksAvailable reports whether a prepared task can be committed and started.
func (s *Service) TasksAvailable() bool { return s.Available() && s.tasks != nil }

// TaskCreate is the canonical create asking to bind the task it is about to
// write. Every value comes from the validated create request and the task ID
// the handler pre-assigned; none is taken from the operation's draft.
type TaskCreate struct {
	OperationID   string
	DraftRevision int
	WorkspaceID   string
	AgentName     string
	AgentNodeID   string
	TaskID        string
}

// CreateClaim is the answer. Existing means the operation already produced
// TaskID and this request may write nothing; otherwise the caller writes the
// task with exactly this identity and assignee, then confirms or releases.
type CreateClaim struct {
	TaskID      string
	AgentName   string
	AgentNodeID string
	Existing    bool
}

// ClaimTaskCreate reserves the operation's single task for one create request.
// The reservation is stored before the task is written, so a repeat, a second
// tab or a retry after a lost response finds it and cannot create another.
func (s *Service) ClaimTaskCreate(ctx context.Context, req TaskCreate) (CreateClaim, error) {
	if !s.TasksAvailable() {
		return CreateClaim{}, ErrUnavailable
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" || len(taskID) > MaxTabIDLen {
		return CreateClaim{}, fmt.Errorf("%w: a task identity is required", ErrInvalidCommand)
	}
	op, err := s.loadOpen(ctx, req.OperationID)
	if err != nil {
		return CreateClaim{}, err
	}
	if op.Kind != KindTask || op.Draft == nil {
		return CreateClaim{}, fmt.Errorf("%w: this operation prepares no task", ErrInvalidCommand)
	}
	if op.Context.WorkspaceID != strings.TrimSpace(req.WorkspaceID) {
		return CreateClaim{}, fmt.Errorf("%w: the prepared task belongs to another workspace", ErrInvalidCommand)
	}
	// A reservation left by an earlier request is settled from the task first.
	if op, err = s.settle(ctx, op); err != nil {
		return CreateClaim{}, err
	}
	if op.Status.Terminal() {
		return CreateClaim{}, statusError(op.Status)
	}
	if op.Task != nil {
		if op.TaskConfirmed() {
			return CreateClaim{TaskID: op.Task.TaskID, AgentName: op.Context.AgentName, AgentNodeID: op.Context.AgentNodeID, Existing: true}, nil
		}
		return CreateClaim{}, ErrCreateInFlight
	}
	if op.Status != StatusPrepared {
		return CreateClaim{}, ErrTransition
	}
	if req.DraftRevision != op.Draft.Revision {
		return CreateClaim{}, fmt.Errorf("%w: the prepared task changed; open it again", ErrConflict)
	}
	// The submitted assignee is the human's reviewed choice. It must be one
	// instance in this workspace's own roster now. Nothing is substituted.
	name, node, err := s.assignee(ctx, op, req.AgentName, req.AgentNodeID)
	if err != nil {
		return CreateClaim{}, err
	}

	flight := createFlight(op.ID, taskID)
	s.takeoff(flight)
	op.Task = &TaskBinding{TaskID: taskID}
	op.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)
	if _, err := s.store.Update(ctx, op, op.Revision); err != nil {
		s.land(flight)
		if !errors.Is(err, ErrConflict) {
			return CreateClaim{}, err
		}
		// Another request moved first. Whatever it reserved stands.
		current, getErr := s.store.Get(ctx, op.ID)
		switch {
		case getErr != nil:
			return CreateClaim{}, ErrUnavailable
		case current.TaskConfirmed():
			return CreateClaim{TaskID: current.Task.TaskID, AgentName: current.Context.AgentName, AgentNodeID: current.Context.AgentNodeID, Existing: true}, nil
		case current.Task != nil:
			return CreateClaim{}, ErrCreateInFlight
		}
		return CreateClaim{}, ErrConflict
	}
	return CreateClaim{TaskID: taskID, AgentName: name, AgentNodeID: node}, nil
}

// ConfirmTaskCreate records that the reserved task was written. It reads the
// task back rather than trusting the caller, so the receipt is a server fact.
// If it cannot be recorded now, a later read settles it the same way.
func (s *Service) ConfirmTaskCreate(ctx context.Context, operationID, taskID string) error {
	defer s.land(createFlight(operationID, taskID))
	if !s.TasksAvailable() {
		return ErrUnavailable
	}
	for attempt := 0; attempt < 3; attempt++ {
		op, err := s.load(ctx, operationID)
		if err != nil {
			return err
		}
		if op.Task == nil || op.Task.TaskID != taskID || op.Status != StatusPrepared {
			// Already confirmed, or the operation ended meanwhile. The task
			// stands either way; there is nothing left to record here.
			return nil
		}
		state, err := s.tasks.Task(ctx, op.Context.WorkspaceID, taskID)
		if err != nil {
			return hostError(err)
		}
		if state.OperationID != op.ID {
			return fmt.Errorf("%w: the saved task does not carry this operation", ErrConflict)
		}
		if _, err := s.confirmCreated(ctx, op, state); !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return ErrConflict
}

// ReleaseTaskCreate gives the reservation back after a create that wrote
// nothing, so the same reviewed form can be saved again.
func (s *Service) ReleaseTaskCreate(ctx context.Context, operationID, taskID string) error {
	defer s.land(createFlight(operationID, taskID))
	if !s.Available() {
		return ErrUnavailable
	}
	for attempt := 0; attempt < 3; attempt++ {
		op, err := s.load(ctx, operationID)
		if err != nil {
			return err
		}
		if op.Task == nil || op.Task.TaskID != taskID || op.Status != StatusPrepared {
			return nil
		}
		if _, err := s.releaseClaim(ctx, op); !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return ErrConflict
}

// Start claims Start once for the operation's task. A repeat, a second tab or
// a retry after a lost response finds the claim and starts nothing. Execution
// is reported only from the task: an acceptance is not a run.
func (s *Service) Start(ctx context.Context, id, tabID string) (*Snapshot, error) {
	if tabID != "" && !ValidTabID(tabID) {
		return nil, fmt.Errorf("%w: tab identity is malformed", ErrInvalidCommand)
	}
	if !s.TasksAvailable() {
		return nil, ErrUnavailable
	}
	op, err := s.loadOpen(ctx, id)
	if err != nil {
		return nil, err
	}
	if op.Kind != KindTask {
		return nil, fmt.Errorf("%w: this operation has no task to start", ErrInvalidCommand)
	}
	if op, err = s.settle(ctx, op); err != nil {
		return nil, err
	}
	switch op.Status {
	case StatusStartRequested, StatusStarted:
		return s.project(ctx, op, tabID), nil
	case StatusReviewed, StatusUnresolved:
	default:
		if op.Status.Terminal() {
			return SnapshotFor(op, tabID), statusError(op.Status)
		}
		return nil, fmt.Errorf("%w: save the task before starting it", ErrTransition)
	}
	state, err := s.tasks.Task(ctx, op.Context.WorkspaceID, op.Task.TaskID)
	if err != nil {
		return nil, hostError(err)
	}
	if state.Ran {
		// It started between the two reads. The next read reports it.
		if op, err = s.settle(ctx, op); err != nil {
			return nil, err
		}
		return s.project(ctx, op, tabID), nil
	}
	if !state.Startable {
		return s.refuse(ctx, op, tabID, ErrStartRefused, "The task is "+taskStatusWords(state.Status)+", so it cannot be started from here. Open the task to decide what to do.")
	}
	// The task's own assignee, as saved and possibly edited since, must still
	// be one instance in this workspace. A similarly named agent elsewhere or
	// the workspace's entry agent is never used instead.
	if _, _, err := s.assignee(ctx, op, state.AgentName, state.AgentNodeID); err != nil {
		if !errors.Is(err, ErrAssignee) {
			return nil, err
		}
		who := strings.TrimSpace(state.AgentName)
		if who == "" || strings.EqualFold(who, "unassigned") {
			// Removing an agent leaves its tasks unassigned.
			return s.refuse(ctx, op, tabID, ErrAssignee, "The task has no agent now. Assign it to an agent in this workspace, then start it.")
		}
		return s.refuse(ctx, op, tabID, ErrAssignee, who+" is not in this workspace any more. Reassign the task, then start it.")
	}

	flight := startFlight(op.ID)
	s.takeoff(flight)
	defer s.land(flight)
	now := s.now().UTC().Truncate(time.Millisecond)
	op.Status, op.UpdatedAt = StatusStartRequested, now
	op.Task.ClaimedAt = &now
	if _, err := s.store.Update(ctx, op, op.Revision); err != nil {
		if !errors.Is(err, ErrConflict) {
			return nil, err
		}
		// Another request claimed or changed it first; that decision stands.
		current, getErr := s.load(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		if current.Status == StatusStartRequested || current.Status == StatusStarted {
			return SnapshotFor(current, tabID), nil
		}
		return nil, ErrConflict
	}

	startErr := s.tasks.Start(ctx, op.Context.WorkspaceID, op.Task.TaskID)
	recorded, err := s.recordStart(ctx, id, startErr)
	if err != nil {
		// The claim stands without its outcome. A later read settles it from
		// the task and never starts it again on its own.
		return nil, err
	}
	if startErr != nil && recorded.Status == StatusReviewed {
		return SnapshotFor(recorded, tabID), &Refusal{Kind: ErrStartRefused, Reason: refusalReason(startErr)}
	}
	// Accepted, or refused only because the task was already running: either
	// way the task now says what is true.
	if recorded, err = s.settle(ctx, recorded); err != nil {
		return nil, err
	}
	return s.project(ctx, recorded, tabID), nil
}

// recordStart writes what the start owner answered for the current claim. If
// the task was observed running meanwhile, that observation already stands.
func (s *Service) recordStart(ctx context.Context, id string, startErr error) (*Operation, error) {
	for attempt := 0; attempt < 3; attempt++ {
		op, err := s.store.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if op.Status != StatusStartRequested || op.Task == nil {
			return op, nil
		}
		now := s.now().UTC().Truncate(time.Millisecond)
		op.UpdatedAt = now
		if startErr != nil {
			op.Status, op.Task.ClaimedAt = StatusReviewed, nil
			appendReceipt(op, Receipt{Kind: ReceiptStartRefused, At: now, Ref: op.Task.TaskID, Reason: refusalReason(startErr)})
		} else {
			appendReceipt(op, Receipt{Kind: ReceiptStartAccepted, At: now, Ref: op.Task.TaskID})
		}
		stored, err := s.store.Update(ctx, op, op.Revision)
		if err == nil {
			return stored, nil
		}
		if !errors.Is(err, ErrConflict) {
			return nil, err
		}
	}
	return nil, ErrConflict
}

// refuse records a Start that was not attempted, keeps the operation where it
// was and returns both the snapshot and the reason.
func (s *Service) refuse(ctx context.Context, op *Operation, tabID string, kind error, reason string) (*Snapshot, error) {
	now := s.now().UTC().Truncate(time.Millisecond)
	reason = boundedReason(reason)
	op.UpdatedAt = now
	appendReceipt(op, Receipt{Kind: ReceiptStartRefused, At: now, Ref: op.Task.TaskID, Reason: reason})
	stored, err := s.store.Update(ctx, op, op.Revision)
	if err != nil {
		// The refusal is still true even if it could not be written down.
		current, getErr := s.store.Get(ctx, op.ID)
		if getErr != nil {
			return nil, &Refusal{Kind: kind, Reason: reason}
		}
		stored = current
	}
	return SnapshotFor(stored, tabID), &Refusal{Kind: kind, Reason: reason}
}

// settle brings a task operation up to date with what its task says. It is
// the only place a lost response is recovered, and it starts nothing: a
// reservation whose task exists is confirmed, one whose task never appeared is
// given back, and a claimed Start becomes started only when the task shows a
// run. An error means the record could not be re-read; "could not check the
// task" returns the operation unchanged.
func (s *Service) settle(ctx context.Context, op *Operation) (*Operation, error) {
	if s.tasks == nil || op == nil || op.Kind != KindTask || op.Task == nil || op.Status.Terminal() {
		return op, nil
	}
	if op.Status == StatusPrepared {
		if s.flying(createFlight(op.ID, op.Task.TaskID)) {
			return op, nil
		}
		state, err := s.readTask(ctx, op)
		switch {
		case err == nil && state.OperationID == op.ID:
			stored, writeErr := s.confirmCreated(ctx, op, state)
			return s.written(ctx, op.ID, stored, writeErr)
		case err == nil || errors.Is(err, ErrTaskMissing):
			// No request in this process is writing it and it is not there:
			// the create wrote nothing. The reviewed form may be saved again.
			stored, writeErr := s.releaseClaim(ctx, op)
			return s.written(ctx, op.ID, stored, writeErr)
		}
		return op, nil
	}
	state, err := s.readTask(ctx, op)
	if errors.Is(err, ErrTaskMissing) {
		closed, endErr := s.end(ctx, op, StatusInvalidated, "the task no longer exists")
		return s.written(ctx, op.ID, closed, endErr)
	}
	if err != nil {
		return op, nil
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	changed := false
	if state.Ran && op.Status != StatusStarted {
		op.Status, changed = StatusStarted, true
		ref := state.RunID
		if ref == "" {
			ref = op.Task.TaskID
		}
		appendReceipt(op, Receipt{Kind: ReceiptRunEstablished, At: now, Ref: ref})
	}
	if op.Status == StatusStarted && op.Task.RunID == "" && state.RunID != "" {
		// The first run the task reports is this operation's run. A later
		// rerun has its own identity and never replaces it.
		op.Task.RunID, changed = state.RunID, true
	}
	if !state.Ran && op.Status == StatusStartRequested && !s.flying(startFlight(op.ID)) {
		switch claimed := op.Task.ClaimedAt; {
		case !acceptedSince(op, claimed):
			op.Status, changed = StatusUnresolved, true
			appendReceipt(op, Receipt{Kind: ReceiptStartUnresolved, At: now, Ref: op.Task.TaskID,
				Reason: "Ori could not confirm whether the start request arrived."})
		case claimed != nil && now.Sub(*claimed) > s.startGrace:
			op.Status, changed = StatusUnresolved, true
			appendReceipt(op, Receipt{Kind: ReceiptStartUnresolved, At: now, Ref: op.Task.TaskID,
				Reason: "The start was accepted, but the task has not begun."})
		}
	}
	if !changed {
		return op, nil
	}
	op.UpdatedAt = now
	stored, writeErr := s.store.Update(ctx, op, op.Revision)
	return s.written(ctx, op.ID, stored, writeErr)
}

// project is the snapshot for one tab plus, for a started task, what became of
// the operation's own run. The outcome is read from the task for this response
// only; when the task cannot be read it is simply absent.
func (s *Service) project(ctx context.Context, op *Operation, tabID string) *Snapshot {
	snapshot := SnapshotFor(op, tabID)
	if snapshot == nil || s.tasks == nil || op.Status != StatusStarted || !op.TaskConfirmed() {
		return snapshot
	}
	state, err := s.tasks.Task(ctx, op.Context.WorkspaceID, op.Task.TaskID)
	if err != nil {
		return snapshot
	}
	snapshot.Outcome = ResolveOutcome(op.Task, state, s.now(), s.startGrace)
	return snapshot
}

// readTask reads the bound task for settling. "Missing" is the one answer that
// gives a reservation back or ends an operation, so it is believed only when a
// second read agrees: a workspace caught between two writes must not look like
// a deleted task.
func (s *Service) readTask(ctx context.Context, op *Operation) (TaskState, error) {
	state, err := s.tasks.Task(ctx, op.Context.WorkspaceID, op.Task.TaskID)
	if !errors.Is(err, ErrTaskMissing) {
		return state, err
	}
	return s.tasks.Task(ctx, op.Context.WorkspaceID, op.Task.TaskID)
}

// written turns the result of a write into the stored record: the write's own
// result when it succeeded, otherwise whatever is stored now. The in-memory
// copy a failed write was built from is never returned.
func (s *Service) written(ctx context.Context, id string, stored *Operation, err error) (*Operation, error) {
	if err == nil && stored != nil {
		return stored, nil
	}
	current, getErr := s.store.Get(ctx, id)
	if getErr != nil {
		return nil, ErrUnavailable
	}
	return current, nil
}

// confirmCreated moves a reserved operation to reviewed for a task the host
// just reported as written with this operation's stamp.
func (s *Service) confirmCreated(ctx context.Context, op *Operation, state TaskState) (*Operation, error) {
	now := s.now().UTC().Truncate(time.Millisecond)
	op.Status, op.UpdatedAt = StatusReviewed, now
	if name := strings.TrimSpace(state.AgentName); name != "" {
		// The saved assignee is the reviewed one, including a human's edit.
		op.Context.AgentName, op.Context.AgentNodeID = name, strings.TrimSpace(state.AgentNodeID)
	}
	supersedePending(op, now, "the task was saved")
	appendReceipt(op, Receipt{Kind: ReceiptTaskCreated, At: now, Ref: op.Task.TaskID})
	return s.store.Update(ctx, op, op.Revision)
}

func (s *Service) releaseClaim(ctx context.Context, op *Operation) (*Operation, error) {
	op.Task, op.UpdatedAt = nil, s.now().UTC().Truncate(time.Millisecond)
	return s.store.Update(ctx, op, op.Revision)
}

// assignee checks one agent against the workspace's own roster. A definitive
// "not there" is ErrAssignee and leaves the operation open for a corrected
// review; "could not check" is ErrUnavailable.
func (s *Service) assignee(ctx context.Context, op *Operation, agentName, nodeID string) (string, string, error) {
	agentName, nodeID = strings.TrimSpace(agentName), strings.TrimSpace(nodeID)
	if agentName == "" || strings.EqualFold(agentName, "unassigned") {
		return "", "", fmt.Errorf("%w: choose an agent in this workspace", ErrAssignee)
	}
	name, node, err := s.guard.Agent(ctx, op.Owner.UserID, op.Context.WorkspaceID, agentName, nodeID)
	if err != nil {
		if errors.Is(err, ErrInvalidated) {
			return "", "", fmt.Errorf("%w: %s", ErrAssignee, agentName)
		}
		return "", "", ErrUnavailable
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(node) == "" {
		return "", "", fmt.Errorf("%w: %s", ErrAssignee, agentName)
	}
	return name, node, nil
}

// acceptedSince reports whether the start owner accepted the current claim.
// An acceptance recorded for an earlier claim does not count.
func acceptedSince(op *Operation, claimed *time.Time) bool {
	for _, receipt := range op.Receipts {
		if receipt.Kind == ReceiptStartAccepted && (claimed == nil || !receipt.At.Before(*claimed)) {
			return true
		}
	}
	return false
}

// refusalReason keeps the start owner's own wording when it gave one and says
// nothing about storage or providers otherwise.
func refusalReason(err error) string {
	var refusal *StartRefusal
	if errors.As(err, &refusal) && strings.TrimSpace(refusal.Reason) != "" {
		return boundedReason(refusal.Reason)
	}
	return "Ori could not start the task right now."
}

// hostError keeps the task host's definitive answer and reduces everything
// else to "could not check".
func hostError(err error) error {
	if errors.Is(err, ErrTaskMissing) {
		return err
	}
	return ErrUnavailable
}

func taskStatusWords(status string) string {
	status = strings.TrimSpace(strings.ReplaceAll(status, "_", " "))
	if status == "" {
		return "not waiting to start"
	}
	return status
}

func createFlight(operationID, taskID string) string { return "create/" + operationID + "/" + taskID }
func startFlight(operationID string) string          { return "start/" + operationID }

func (s *Service) takeoff(flight string) {
	s.flightMu.Lock()
	s.flights[flight]++
	s.flightMu.Unlock()
}

func (s *Service) land(flight string) {
	s.flightMu.Lock()
	if s.flights[flight] > 1 {
		s.flights[flight]--
	} else {
		delete(s.flights, flight)
	}
	s.flightMu.Unlock()
}

func (s *Service) flying(flight string) bool {
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	return s.flights[flight] > 0
}
