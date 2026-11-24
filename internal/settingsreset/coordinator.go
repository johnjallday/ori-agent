package settingsreset

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

var (
	ErrInvalidRequest       = errors.New("a reviewed preview, request identity and exact RESET confirmation are required")
	ErrLifecycleUnavailable = errors.New("safe reset admission and writer draining are unavailable on this runtime")
	ErrOperationConflict    = errors.New("another reset operation already owns this installation; recover its result instead")
	ErrOperationNotFound    = errors.New("reset operation is unknown; absence is not evidence of completion")
	ErrAdmissionUncertain   = errors.New("reset admission is interrupted or uncertain; preserve recovery metadata and fully relaunch")
	ErrActiveWork           = errors.New("active or unowned work prevents reset; finish that work and review again")
	// ErrAgentsFolderUnconfirmed rejects a reset that removes the agents
	// folder without the second confirmation naming that folder.
	ErrAgentsFolderUnconfirmed = errors.New("this reset removes the agents in your Workspace Directory; confirm that folder too, then reset again")
	ErrHQArchiveUnconfirmed    = errors.New("confirm the Personal HQ archive in the current review, or review the reset without it")
)

// Lifecycle is supplied by runtime construction, never by HTTP. TryFence must
// atomically reject active/unknown work without cancelling it, or fence ALL new
// mutations/dispatch. After success, no error or request disconnect may unfence
// the runtime. Drain joins known writers without deleting any selected data.
// The current production builder deliberately supplies no such capability yet.
type Lifecycle interface {
	TryFence(context.Context) error
	Drain(context.Context) error
}

// Coordinator performs admission only. It cannot apply a category or unlock an
// installation. Reading/replaying never calls lifecycle hooks. A lease-shared
// gate protects against multiple instances as well as concurrent HTTP requests.
type Coordinator struct {
	lease     *resetstate.Lease
	planner   *Planner
	lifecycle Lifecycle
	now       func() time.Time
	// betweenCancelReads is a test seam: it runs after Cancel's host decision and
	// before its second read of the receipt and policy. Nil in production.
	betweenCancelReads func()
}

func NewCoordinator(lease *resetstate.Lease, planner *Planner, lifecycle Lifecycle) *Coordinator {
	return &Coordinator{lease: lease, planner: planner, lifecycle: lifecycle, now: time.Now}
}

func ValidateExecuteRequest(req ExecuteRequest) error {
	if req.Confirmation != "RESET" || !validID(req.PreviewID) || !validID(req.RequestID) || len(req.ConfirmAgentsFolder) > 4096 ||
		(req.ConfirmHQArchive != "" && !validArchiveDigest(req.ConfirmHQArchive)) {
		return ErrInvalidRequest
	}
	return nil
}

func (c *Coordinator) Stage(ctx context.Context, req ExecuteRequest) (Operation, error) {
	if err := ValidateExecuteRequest(req); err != nil {
		return Operation{}, err
	}
	if c.lease == nil {
		return Operation{}, ErrLifecycleUnavailable
	}
	var operation Operation
	err := c.lease.WithAdmission(ctx, func() error {
		existing, err := c.load()
		if err != nil {
			return err
		}
		retireCompleted := false
		if existing != nil {
			operation = c.publicOperation(existing)
			if existing.Plan.Preview.ID == req.PreviewID && existing.RequestID == req.RequestID {
				if !confirmedHQArchive(existing.Plan, req.ConfirmHQArchive) {
					return ErrHQArchiveUnconfirmed
				}
				if c.lease.Uncertain() {
					return ErrAdmissionUncertain
				}
				return nil // No preview revalidation/drain/rewrite on an admitted replay.
			}
			if existing.Operation.State != StateCompleted && existing.Operation.State != StateCanceled {
				return ErrOperationConflict
			}
			// One bounded journal doubles as the durable last result. A newly
			// reviewed operation may replace only a terminal receipt: a fully
			// verified one, or one canceled before anything was applied. Blocked,
			// partial and unknown evidence remains recoverable and exclusive. A
			// replay of the canceled operation's own preview and request id was
			// answered above without writing, so it can never revive the old plan.
			retireCompleted = true
		}
		if c.lease.Uncertain() {
			return ErrAdmissionUncertain
		}
		// Policy-only/orphan state must not be overwritten with a new operation.
		if !retireCompleted {
			if err := c.lease.RequireCleanStart(); err != nil {
				return err
			}
		}
		if c.planner == nil || c.lifecycle == nil {
			return ErrLifecycleUnavailable
		}
		plan, err := c.planner.validatedPlan(ctx, req.PreviewID)
		if err != nil {
			return err
		}
		if plan.Installation != c.lease.Path() {
			return ErrScopeChanged
		}
		// The second confirmation must name the folder that was reviewed, so a
		// stale or generic "yes" cannot remove a different Workspace
		// Directory's agents.
		if folder := plan.Evidence.AgentsFolder; folder != "" && req.ConfirmAgentsFolder != folder {
			return ErrAgentsFolderUnconfirmed
		}
		if !confirmedHQArchive(plan, req.ConfirmHQArchive) {
			return ErrHQArchiveUnconfirmed
		}
		j := newJournal(plan, req.RequestID, c.lease.Identity(), c.now().UTC())
		// Include room for a bounded failure result before freezing the runtime.
		data, err := encodeJournal(j)
		if err != nil {
			return err
		}
		if len(data) > resetstate.MaxRecordBytes-2048 {
			return resetstate.ErrRecordLimit
		}
		fenceReturned := false
		defer func() {
			if !fenceReturned {
				c.lease.MarkUncertain()
			}
		}()
		fenceErr := c.lifecycle.TryFence(ctx)
		fenceReturned = true
		if fenceErr != nil {
			return ErrActiveWork
		}
		settled := false
		defer func() {
			if !settled {
				c.lease.MarkUncertain()
			}
		}()
		// From this point the lifecycle stays fenced even if persistence fails.
		// Revalidate after the atomic fence: work finishing just before it may
		// have changed roots/dependencies since the earlier preview check.
		if _, err := c.planner.Validate(ctx, req.PreviewID); err != nil {
			blockAdmission(j, "scope_changed_after_fence")
			err := c.persistResult(j, &operation)
			settled = err == nil
			return err
		}
		// A preparing boundary makes a crash DURING drain distinguishable from a
		// staged plan. Pre-start must never interpret this as permission to apply.
		if err := c.persist(j); err != nil {
			operation = uncertainOperation(j.Operation)
			return ErrAdmissionUncertain
		}
		if err := c.lifecycle.Drain(ctx); err != nil || ctx.Err() != nil {
			blockAdmission(j, "drain_failed")
		} else if err := capturePostDrainEvidence(ctx, j); err != nil {
			blockAdmission(j, "retained_evidence_unavailable")
		} else {
			// Drain may legitimately flush a retained workspace index. The
			// post-drain digest is the only baseline taken after every cooperating
			// writer stopped and therefore the one pre-start apply must preserve.
			j.Operation.State = StateAwaitingRestart
			j.Operation.Revision++
		}
		err = c.persistResult(j, &operation)
		settled = err == nil
		return err
	})
	return operation, err
}

func confirmedHQArchive(plan resolvedPlan, supplied string) bool {
	if plan.Evidence.HQArchive == nil {
		return supplied == ""
	}
	return supplied == plan.Preview.ScopeDigest
}

func capturePostDrainEvidence(ctx context.Context, j *journal) error {
	if err := refreshProtectedDigests(ctx, &j.Plan.Evidence); err != nil {
		return err
	}
	if j.Archive == nil {
		return nil
	}
	moved, err := observeArchiveFiles(ctx, j)
	if err != nil || moved {
		return ErrScopeChanged
	}
	digest, err := j.Plan.Evidence.digestArchiveRemainder(ctx)
	if err != nil {
		return err
	}
	j.Archive.Remainder = digest
	j.Archive.Baseline = archiveBaseline(j)
	return nil
}

func (c *Coordinator) persistResult(j *journal, operation *Operation) error {
	j.Operation.UpdatedAt = c.now().UTC()
	if err := c.persist(j); err != nil {
		*operation = uncertainOperation(j.Operation)
		return ErrAdmissionUncertain
	}
	*operation = c.publicOperation(j)
	return nil
}

func (c *Coordinator) persist(j *journal) error {
	data, err := encodeJournal(j)
	if err == nil {
		err = c.lease.Replace(resetstate.OperationRecord, data)
	}
	if err != nil {
		c.lease.MarkUncertain()
	} else {
		c.lease.ExpectOperation()
	}
	return err
}

func newJournal(plan resolvedPlan, requestID, identity string, now time.Time) *journal {
	j := &journal{Version: SchemaVersion, AcceptedBy: identity, RequestID: requestID, Plan: plan,
		Operation: Operation{SchemaVersion: SchemaVersion, ID: plan.Preview.OperationID, Intent: plan.Preview.Intent,
			State: StatePreparing, Revision: 1, Results: []CategoryResult{}, Blockers: []Blocker{}, Restart: plan.Preview.Restart, CreatedAt: now, UpdatedAt: now}}
	if plan.Evidence.HQArchive != nil {
		j.Archive = &archiveJournal{Stage: archivePending, Consent: archiveConsent(j)}
		j.Operation.HQArchive = publicArchiveResult(j, OutcomePending)
	}
	for _, category := range plan.Preview.Categories {
		def, _ := definition(category.ID)
		result := CategoryResult{ID: category.ID, Outcome: OutcomePending, Retained: category.Retained, Checks: []CheckResult{}}
		for _, name := range def.Checks {
			result.Checks = append(result.Checks, CheckResult{Name: name, Outcome: OutcomePending})
		}
		j.Operation.Results = append(j.Operation.Results, result)
	}
	return j
}

func blockAdmission(j *journal, code string) {
	if j.Archive != nil {
		j.Operation.HQArchive = publicArchiveResult(j, OutcomeFailed)
	}
	j.Operation.State = StateBlocked
	j.Operation.Revision++
	j.Operation.Blockers = []Blocker{{Code: code, Message: "Reset could not finish safe admission; no selected data was deleted.", Recovery: "Keep ordinary work stopped. Fully quit and relaunch into recovery; do not delete reset metadata or blindly repeat the request."}}
}

func uncertainOperation(op Operation) Operation {
	if op.HQArchive != nil && op.HQArchive.Outcome != OutcomeCompleted {
		op.HQArchive = &HQArchiveResult{Outcome: OutcomeUnknown, Destination: op.HQArchive.Destination,
			Message: "Ori has not verified the archive outcome. Keep both locations and the reset receipt for recovery."}
	}
	op.State = StateInterrupted
	op.Blockers = []Blocker{{Code: "admission_uncertain", Message: "Durable admission or writer draining could not be established. Completion is unknown.", Recovery: "Fully quit and relaunch into recovery. Preserve the installation and reset metadata."}}
	return op
}

// Status is read-only. The operation identity is known from preview even if the
// entire POST response was lost. Health, an absent receipt or an expired preview
// cannot substitute for the operation's named postconditions.
func (c *Coordinator) Status(ctx context.Context, id string) (Operation, error) {
	if !validID(id) {
		return Operation{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	if c.lease == nil {
		return Operation{}, ErrLifecycleUnavailable
	}
	if err := c.lease.Check(); err != nil {
		return Operation{}, err
	}
	j, err := c.load()
	if err != nil {
		return Operation{}, err
	}
	if j == nil || j.Operation.ID != id {
		if j == nil {
			if err := c.lease.RequireCleanStart(); err != nil {
				return Operation{}, err
			}
		}
		if c.lease.Uncertain() {
			return Operation{}, ErrAdmissionUncertain
		}
		return Operation{}, ErrOperationNotFound
	}
	return c.publicOperation(j), nil
}

func (c *Coordinator) load() (*journal, error) {
	data, err := c.lease.Read(resetstate.OperationRecord)
	if err != nil {
		return nil, ErrJournalInvalid
	}
	if data == nil {
		if c.lease.ExpectsOperation() {
			return nil, ErrJournalInvalid
		}
		return nil, nil
	}
	j, err := decodeJournal(data)
	if err != nil {
		return nil, err
	}
	if j.Plan.Installation != c.lease.Path() {
		return nil, ErrJournalInvalid
	}
	c.lease.ExpectOperation()
	return j, nil
}

func (c *Coordinator) publicOperation(j *journal) Operation {
	op := j.Operation
	if c.lease.Uncertain() || (op.State == StatePreparing && j.AcceptedBy != c.lease.Identity()) {
		op = uncertainOperation(op)
	}
	// No internal slices or confirmation/target data escape the coordinator.
	data, _ := json.Marshal(op)
	var copy Operation
	_ = json.Unmarshal(data, &copy)
	return copy
}
