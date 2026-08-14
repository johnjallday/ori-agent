package workspaceplan

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Service is the app-owned lifecycle authority for Plans.
//
// Everything that changes a Plan's state goes through here rather than through
// a handler reaching into the store, because the invariants worth having are
// the ones that cannot be bypassed: a status only moves along a validated edge,
// every move leaves an activity record, an approval transition only accepts an
// explicit user action, and nothing that materialized work can be deleted
// (FR-14, FR-15, FR-17, FR-59).
type Service struct {
	store Store
	now   func() time.Time
	// progress derives the Plan read model from live Task and Run state. It is
	// optional: with no source, a Plan reads without progress rather than with
	// a persisted copy of execution state (FR-12).
	progress ProgressSource
	// generator proposes Plan content. It is optional: without it every
	// non-generating operation still works (FR-58, FR-177).
	generator *Generator
}

// ProgressSource computes a Plan's progress from the linked Tasks and Runs.
// Group 5 supplies the real implementation over the existing Task and Run
// services; keeping it an interface is what stops this package from growing its
// own copy of execution state (FR-11, FR-107).
type ProgressSource interface {
	PlanProgress(ctx context.Context, plan *Plan) (Progress, error)
}

// ServiceOption configures a Service.
type ServiceOption func(*Service)

// WithClock replaces the service clock, so retention and activity ordering can
// be tested without sleeping.
func WithClock(now func() time.Time) ServiceOption {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithProgressSource attaches the derived-progress provider.
func WithProgressSource(source ProgressSource) ServiceOption {
	return func(s *Service) { s.progress = source }
}

// WithGenerator attaches the planner used for drafting and clarification.
func WithGenerator(generator *Generator) ServiceOption {
	return func(s *Service) { s.generator = generator }
}

// NewService returns a Plan lifecycle service over the given store.
func NewService(store Store, opts ...ServiceOption) *Service {
	service := &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// CreateInput is the request to start a new Plan.
type CreateInput struct {
	// Request is the initiating text. It is stored verbatim and separately
	// from any model-produced summary (FR-21).
	Request string
	// Title is optional; an empty title is derived from the request so the
	// Plans list never shows an unnamed row.
	Title string
	// Objective is optional at creation: a Plan may start as nothing but a
	// request and gain its objective from drafting.
	Objective string
	// Draft is optional starting content, for callers that already have
	// structure (for example an orchestration proposal).
	Draft PlanContent
	// Origin records what created the Plan (FR-4).
	Origin Origin
}

// Create starts a new Plan in draft. It creates no Tasks, writes no artifacts,
// and starts nothing: a Plan existing is not a Plan being approved (FR-20).
func (s *Service) Create(ctx context.Context, workspaceID string, input CreateInput) (*Plan, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: a plan requires an owning workspace", ErrValidation)
	}
	request := strings.TrimSpace(input.Request)
	if request == "" {
		return nil, fmt.Errorf("%w: a plan requires an initiating request", ErrValidation)
	}

	now := s.now()
	draft := input.Draft.Clone()
	if draft.Execution.Mode == "" {
		draft.Execution.Mode = ExecutionStepThrough
	}

	plan := &Plan{
		ID:              NewPlanID(),
		WorkspaceID:     workspaceID,
		Title:           deriveTitle(input.Title, request),
		OriginalRequest: request,
		Objective:       strings.TrimSpace(input.Objective),
		Status:          StatusDraft,
		Draft:           draft,
		Origin:          input.Origin,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastActivityAt:  now,
	}
	if err := s.store.CreatePlan(ctx, plan); err != nil {
		return nil, err
	}

	created := NewActivity(plan, ActivityCreated, sourceFor(input.Origin), input.Origin.Actor, "")
	created.To = StatusDraft
	created.CreatedAt = now
	if _, err := s.store.AppendActivity(ctx, created); err != nil {
		return nil, err
	}
	return s.Get(ctx, workspaceID, plan.ID)
}

// deriveTitle falls back to a trimmed first line of the request so a Plan is
// never listed without a name.
func deriveTitle(title, request string) string {
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		return trimmed
	}
	firstLine := request
	if index := strings.IndexAny(firstLine, "\r\n"); index >= 0 {
		firstLine = firstLine[:index]
	}
	firstLine = strings.TrimSpace(firstLine)
	const maxDerivedTitle = 80
	if len(firstLine) > maxDerivedTitle {
		firstLine = strings.TrimSpace(firstLine[:maxDerivedTitle]) + "…"
	}
	if firstLine == "" {
		return "Untitled plan"
	}
	return firstLine
}

// sourceFor maps a Plan's origin to the transition source recorded in its
// history. It never returns SourceUser for a non-user origin, because the
// approval gate keys off that value (FR-59).
func sourceFor(origin Origin) TransitionSource {
	switch origin.Kind {
	case OriginUser:
		return SourceUser
	case OriginOrchestration:
		return SourceService
	default:
		return SourceService
	}
}

// Get returns one Plan with derived progress attached.
func (s *Service) Get(ctx context.Context, workspaceID, planID string) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	s.attachProgress(ctx, plan)
	return plan, nil
}

// List returns the workspace's Plans for the Active or History section.
func (s *Service) List(ctx context.Context, workspaceID string, filter ListFilter) ([]*Plan, error) {
	plans, err := s.store.ListPlans(ctx, workspaceID, filter)
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		s.attachProgress(ctx, plan)
	}
	return plans, nil
}

// attachProgress fills in the derived read model. A progress failure is not a
// read failure: the Plan is still readable, just without a summary, because the
// authority for that data is the Task and Run records rather than this call
// (FR-12).
func (s *Service) attachProgress(ctx context.Context, plan *Plan) {
	if s.progress == nil || plan == nil {
		return
	}
	progress, err := s.progress.PlanProgress(ctx, plan)
	if err != nil {
		return
	}
	plan.Progress = &progress
}

// TransitionInput describes one requested lifecycle change.
type TransitionInput struct {
	To     Status
	Source TransitionSource
	Actor  string
	// ActorID is the stable ID of the acting principal, when there is one.
	ActorID string
	Reason  string
	Version int
	TaskID  string
	RunID   string
}

// Transition validates and applies one status change, recording it in the
// Plan's append-only history (FR-14, FR-15).
func (s *Service) Transition(ctx context.Context, workspaceID, planID string, input TransitionInput) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(plan.Status, input.To, input.Source); err != nil {
		return nil, err
	}

	change := NewStatusChange(plan, input.To, input.Source, input.Actor, input.Reason)
	change.ActorID = input.ActorID
	change.Version = input.Version
	change.TaskID = input.TaskID
	change.RunID = input.RunID
	change.CreatedAt = s.now()

	if err := s.store.SetPlanStatus(ctx, workspaceID, planID, input.To, change); err != nil {
		return nil, err
	}
	return s.Get(ctx, workspaceID, planID)
}

// Archive moves a Plan to History. It is a placement change and nothing more:
// versions, approvals, Task links, Run links, artifacts, and activity all stay
// exactly as they were (FR-16).
func (s *Service) Archive(ctx context.Context, workspaceID, planID, reason, actor string) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.ArchivedAt != nil {
		return plan, nil
	}

	now := s.now()
	if err := s.store.ArchivePlan(ctx, workspaceID, planID, reason, now); err != nil {
		return nil, err
	}
	entry := NewActivity(plan, ActivityArchived, SourceService, actor, reason)
	entry.CreatedAt = now
	if _, err := s.store.AppendActivity(ctx, entry); err != nil {
		return nil, err
	}
	return s.Get(ctx, workspaceID, planID)
}

// Reopen returns an archived Plan to the Active list.
//
// It is allowed only for a Plan whose lifecycle can still go somewhere. A
// completed, failed, cancelled, or superseded Plan stays in History: bringing
// one back would imply its old approval still authorizes work, and it does not
// (FR-38, FR-74).
func (s *Service) Reopen(ctx context.Context, workspaceID, planID, actor string) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.ArchivedAt == nil {
		return plan, nil
	}
	if plan.Status.Terminal() {
		return nil, fmt.Errorf("%w: a %s plan stays in history; revise it into a new plan instead",
			ErrInvalidTransition, plan.Status)
	}

	if err := s.store.ReopenPlan(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	entry := NewActivity(plan, ActivityReopened, SourceUser, actor, "")
	entry.CreatedAt = s.now()
	if _, err := s.store.AppendActivity(ctx, entry); err != nil {
		return nil, err
	}
	return s.Get(ctx, workspaceID, planID)
}

// Delete hard-deletes a Plan, and only when doing so destroys nothing. A Plan
// that was ever approved, or that has linked Tasks, Runs, or approval records,
// is refused here and archived instead — removing a Plan must never quietly
// remove the work it produced (FR-17).
//
// The eligibility rules live in the store so both implementations enforce them
// inside the same transaction that does the delete, closing the window where a
// Task could be linked between the check and the removal.
func (s *Service) Delete(ctx context.Context, workspaceID, planID string) error {
	return s.store.DeletePlan(ctx, workspaceID, planID)
}

// Activity returns a Plan's lifecycle history in order.
func (s *Service) Activity(ctx context.Context, workspaceID, planID string, limit int) ([]Activity, error) {
	return s.store.ListActivity(ctx, workspaceID, planID, limit)
}

// Store exposes the underlying store for the services layered on top of the
// lifecycle (drafting, review, materialization, execution). It is deliberately
// not a way for handlers to bypass the lifecycle rules above.
func (s *Service) Store() Store { return s.store }

// Now returns the service clock, so layered services share one notion of time.
func (s *Service) Now() time.Time { return s.now() }

// SetProgressSource attaches the derived-progress provider after construction,
// for callers whose task store only exists later in startup.
func (s *Service) SetProgressSource(source ProgressSource) { s.progress = source }
