package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Drafting ties the generator to the store and the lifecycle.
//
// The division of labour is the same one the whole package rests on: the
// generator proposes content, this file decides what happens to the Plan, and
// the store enforces what can be written. A model can move a Plan between
// draft and needs_input and nothing else — every other transition needs a user
// or compiled logic behind it (FR-14, FR-59).

// DraftingOptions configure one generation request.
type DraftingOptions struct {
	// Actor is the user on whose behalf generation runs. It is recorded in
	// activity; it does not grant the model any authority (FR-60).
	Actor string
	// AllowClarification lets the planner ask questions instead of drafting.
	// A revision of existing content sets it false: the user already answered
	// what was needed, and re-asking mid-revision loses their place (FR-23).
	AllowClarification bool
	// Guidance is the model-guidance half of Workspace Settings (FR-125).
	Guidance GuidanceInput
	// Validation carries the agents and capabilities that actually exist.
	Validation ValidationContext
	// WorkspaceContext is free-form context for the planner.
	WorkspaceContext string
	// Artifacts is the compiled output policy applied after generation. It is
	// separate from Guidance because paths and enabled writes are guarantees,
	// not suggestions to the model.
	Artifacts ArtifactPolicy
	// Sections limits a revision to named sections (FR-54).
	Sections []string
	// ExpectedRevision is the draft revision the caller last saw. It is
	// checked before the generated content is written, so generation started
	// against a stale draft cannot silently overwrite a newer edit (FR-30).
	ExpectedRevision int64
}

// SetGenerator attaches the planner. A Service with no generator still does
// everything that does not need a model: create, edit, review, approve, and
// execute all keep working (FR-58, FR-177).
func (s *Service) SetGenerator(generator *Generator) { s.generator = generator }

// GeneratorAvailable reports whether generation controls should be enabled.
func (s *Service) GeneratorAvailable() bool { return s.generator.Available() }

// Draft generates Plan content for an existing Plan.
//
// When the planner has enough to work with it writes a draft (FR-22). When
// material product information is missing and clarification is allowed, it
// records structured questions and moves the Plan to needs_input instead
// (FR-23). Either way nothing is approved, no Task is created, and nothing
// starts (FR-20).
func (s *Service) Draft(ctx context.Context, workspaceID, planID string, opts DraftingOptions) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if s.generator == nil || !s.generator.Available() {
		return nil, ErrModelUnavailable
	}
	// Generating into an approved or executing Plan would rewrite work that a
	// user already authorized. Revising approved work starts a new draft
	// instead (FR-38).
	if plan.Status != StatusDraft && plan.Status != StatusNeedsInput {
		return nil, fmt.Errorf("%w: a %s plan cannot be regenerated in place; create a revision",
			ErrInvalidTransition, plan.Status)
	}

	input := GenerationInput{
		Request:          plan.OriginalRequest,
		Objective:        plan.Objective,
		Content:          plan.Draft,
		Answers:          plan.Draft.Clarifications,
		Guidance:         opts.Guidance,
		Validation:       opts.Validation,
		WorkspaceContext: opts.WorkspaceContext,
		Sections:         opts.Sections,
	}

	// Ask first when the request has never been clarified and the planner is
	// allowed to ask. A Plan that already carries answers goes straight to
	// drafting rather than starting another round (FR-26).
	if opts.AllowClarification && needsClarification(plan) {
		questions, err := s.generator.GenerateClarifications(ctx, input)
		if err == nil && len(questions) > 0 {
			return s.recordClarificationRound(ctx, plan, questions, opts.Actor)
		}
		if err != nil && !errors.Is(err, ErrValidation) {
			// A model that could not be reached is a real failure; a model
			// that returned unusable questions just means we draft instead.
			return nil, err
		}
	}

	outcome, err := s.generator.GenerateDraft(ctx, input)
	if err != nil {
		return nil, &GenerationError{Err: err, Issues: outcome.Issues, Attempts: outcome.Attempts}
	}
	content, err := ApplyArtifactPolicy(plan, outcome.Content, opts.Artifacts)
	if err != nil {
		return nil, err
	}

	now := s.now()
	revision, err := s.store.UpdatePlanDraft(ctx, workspaceID, planID, opts.ExpectedRevision, DraftUpdate{
		Title:     plan.Title,
		Objective: outcome.Objective,
		Content:   content,
		Intent:    plan.DraftIntent,
		UpdatedAt: now,
	})
	if err != nil {
		return nil, err
	}
	_ = revision

	// A Plan that was waiting on answers returns to draft now that it has
	// content (FR-26).
	if plan.Status == StatusNeedsInput {
		change := NewStatusChange(plan, StatusDraft, SourceModel, opts.Actor, "plan drafted")
		change.CreatedAt = now
		if err := s.store.SetPlanStatus(ctx, workspaceID, planID, StatusDraft, change); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, workspaceID, planID)
}

// GenerationError carries the validation issues that survived the repair
// budget, so the editor can show what is wrong rather than only that something
// is (FR-45).
type GenerationError struct {
	Err      error
	Issues   []ValidationIssue
	Attempts int
}

func (e *GenerationError) Error() string {
	if e == nil || e.Err == nil {
		return "plan generation failed"
	}
	return e.Err.Error()
}

func (e *GenerationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// needsClarification reports whether this Plan has never been through a
// clarification round. Once questions exist, asking again is a decision the
// user makes, not one the planner repeats on every regeneration.
func needsClarification(plan *Plan) bool {
	return len(plan.Draft.Clarifications) == 0
}

// recordClarificationRound persists the questions and moves the Plan to
// needs_input (FR-23, FR-24).
func (s *Service) recordClarificationRound(ctx context.Context, plan *Plan, questions []Clarification, actor string) (*Plan, error) {
	now := s.now()
	round := nextRound(plan.Draft.Clarifications)
	for i := range questions {
		questions[i].Round = round
		if questions[i].CreatedAt.IsZero() {
			questions[i].CreatedAt = now
		}
	}

	// Existing questions are carried through so an answered question from an
	// earlier round is never dropped by a later one (FR-25).
	merged := append(append([]Clarification(nil), plan.Draft.Clarifications...), questions...)
	if err := s.store.PutClarifications(ctx, plan.WorkspaceID, plan.ID, merged); err != nil {
		return nil, err
	}

	if plan.Status != StatusNeedsInput {
		change := NewStatusChange(plan, StatusNeedsInput, SourceModel, actor,
			fmt.Sprintf("%d question(s) need answers", len(questions)))
		change.CreatedAt = now
		if err := s.store.SetPlanStatus(ctx, plan.WorkspaceID, plan.ID, StatusNeedsInput, change); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, plan.WorkspaceID, plan.ID)
}

func nextRound(existing []Clarification) int {
	highest := 0
	for _, question := range existing {
		if question.Round > highest {
			highest = question.Round
		}
	}
	return highest + 1
}

// AnswerInput is one user response to a clarification question.
type AnswerInput struct {
	// Answer is the user's authored text. It is stored exactly as written and
	// is never replaced by a later model summary (FR-25).
	Answer string
	// Skip records that the user chose not to answer. Only a non-required
	// question may be skipped, and skipping records an assumption (FR-28).
	Skip       bool
	SkipReason string
	Actor      string
}

// Answer persists one clarification response.
//
// Answering the last required question returns the Plan to draft so it can be
// regenerated or edited (FR-26). Skipping an optional question records the
// resulting assumption in the draft, so the user can see what was assumed on
// their behalf (FR-28).
func (s *Service) Answer(ctx context.Context, workspaceID, planID, clarificationID string, input AnswerInput) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}

	question, found := plan.Draft.Clarification(clarificationID)
	if !found {
		return nil, fmt.Errorf("%w: clarification %s", ErrPlanNotFound, clarificationID)
	}
	if input.Skip && question.Required {
		return nil, fmt.Errorf("%w: %q is required and cannot be skipped",
			ErrValidation, question.Prompt)
	}
	if !input.Skip && strings.TrimSpace(input.Answer) == "" {
		return nil, fmt.Errorf("%w: an answer cannot be empty; skip the question instead", ErrValidation)
	}

	now := s.now()
	if err := s.store.AnswerClarification(ctx, workspaceID, planID, clarificationID, ClarificationAnswer{
		Answered:   !input.Skip,
		Answer:     input.Answer,
		SkipReason: input.SkipReason,
		AnsweredBy: input.Actor,
		At:         now,
	}); err != nil {
		return nil, err
	}

	if input.Skip {
		if err := s.recordSkipAssumption(ctx, plan, question, input); err != nil {
			return nil, err
		}
	}

	// Reload so the required-question check sees the answer just written.
	updated, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if updated.Status == StatusNeedsInput && len(updated.Draft.UnansweredRequired()) == 0 {
		change := NewStatusChange(updated, StatusDraft, SourceUser, input.Actor, "all required questions answered")
		change.CreatedAt = now
		if err := s.store.SetPlanStatus(ctx, workspaceID, planID, StatusDraft, change); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, workspaceID, planID)
}

// recordSkipAssumption writes the assumption a skip implies into the draft, so
// the consequence of skipping is visible in the Plan rather than only in the
// question's status (FR-28).
func (s *Service) recordSkipAssumption(ctx context.Context, plan *Plan, question Clarification, input AnswerInput) error {
	statement := strings.TrimSpace(input.SkipReason)
	if statement == "" {
		statement = fmt.Sprintf("Unanswered: %s", question.Prompt)
	} else {
		statement = fmt.Sprintf("%s — %s", question.Prompt, statement)
	}

	content := plan.Draft.Clone()
	// One assumption per skipped question: answering and re-skipping must not
	// stack duplicates.
	for i, existing := range content.Assumptions {
		if existing.ClarificationID == question.ID {
			content.Assumptions[i].Statement = statement
			return s.writeDraftContent(ctx, plan, content)
		}
	}
	content.Assumptions = append(content.Assumptions, Assumption{
		ID:              NewAssumptionID(),
		Statement:       statement,
		Author:          AuthorApp,
		ClarificationID: question.ID,
	})
	return s.writeDraftContent(ctx, plan, content)
}

// writeDraftContent saves draft content on the caller's behalf using the
// revision it just read, so a concurrent edit still loses the race rather than
// being clobbered (FR-30).
func (s *Service) writeDraftContent(ctx context.Context, plan *Plan, content PlanContent) error {
	_, err := s.store.UpdatePlanDraft(ctx, plan.WorkspaceID, plan.ID, plan.DraftRevision, DraftUpdate{
		Title:     plan.Title,
		Objective: plan.Objective,
		Content:   content,
		Intent:    plan.DraftIntent,
		UpdatedAt: s.now(),
	})
	return err
}

// EditInput is one user edit to the working draft.
type EditInput struct {
	Title     string
	Objective string
	Content   PlanContent
	Intent    RevisionIntent
	Actor     string
	// ExpectedRevision is the revision the editor loaded. A stale value is
	// refused rather than overwriting a newer save (FR-30).
	ExpectedRevision int64
	// Snapshot records a recovery point before the write. Autosaves set it;
	// an explicit save does not need one.
	Snapshot bool
	// Validation is optional. Manual edits are validated for structure but a
	// user may deliberately save an incomplete draft: only moving to review
	// requires a fully valid Plan (FR-29, FR-31).
	Validation ValidationContext
}

// Edit saves a user's changes to the working draft.
//
// Saving grants no approval and creates no Tasks: a draft is editable
// precisely because nothing has been committed to yet (FR-29).
//
// User-authored content is marked as such so version provenance can show which
// parts a person wrote and which a model produced (FR-57).
func (s *Service) Edit(ctx context.Context, workspaceID, planID string, input EditInput) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.Status.Terminal() {
		return nil, fmt.Errorf("%w: a %s plan cannot be edited", ErrInvalidTransition, plan.Status)
	}
	// A version under review is read-only until the reviewer chooses to edit
	// it, and choosing to edit means requesting changes — which retains the
	// reviewed version and returns the plan to draft. Allowing a quiet edit
	// here would let the draft drift away from the version someone is looking
	// at (FR-37, FR-152).
	if plan.Status == StatusInReview {
		return nil, fmt.Errorf(
			"%w: version %d is under review. Request changes first — the reviewed version is kept",
			ErrInvalidTransition, plan.CurrentVersion)
	}

	content := markUserEdits(plan.Draft, input.Content)
	// An unspecified execution mode means "leave it as it is", not "reset it".
	// A user editing only the objective must not have to restate policy they
	// did not touch, and must not silently change it either.
	if content.Execution.Mode == "" {
		content.Execution.Mode = plan.Draft.Execution.Mode
	}
	if content.Execution.Mode == "" {
		content.Execution.Mode = ExecutionStepThrough
	}

	// Structural problems that would make the draft unusable are refused even
	// while drafting: a dependency cycle is not a work-in-progress state, it is
	// a broken graph.
	if result := validateDraftStructure(content); !result.OK() {
		return nil, result.Error()
	}

	if input.Snapshot {
		if err := s.snapshotDraft(ctx, plan); err != nil {
			return nil, err
		}
	}

	if _, err := s.store.UpdatePlanDraft(ctx, workspaceID, planID, input.ExpectedRevision, DraftUpdate{
		Title:     strings.TrimSpace(orDefault(input.Title, plan.Title)),
		Objective: strings.TrimSpace(input.Objective),
		Content:   content,
		Intent:    orDefaultIntent(input.Intent, plan.DraftIntent),
		UpdatedAt: s.now(),
	}); err != nil {
		return nil, err
	}

	// The question set may have been reordered or reworded by the editor, but
	// the answers stay where the user put them (FR-25).
	if len(input.Content.Clarifications) > 0 {
		if err := s.store.PutClarifications(ctx, workspaceID, planID, input.Content.Clarifications); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, workspaceID, planID)
}

// validateDraftStructure runs the subset of validation that applies to a draft
// still being written. Missing objectives and empty groups are expected while
// drafting; broken references and over-limit content are not.
func validateDraftStructure(content PlanContent) ValidationResult {
	full := ValidatePlanContent("placeholder-objective", content, ValidationContext{})
	var kept ValidationResult
	for _, issue := range full.Issues {
		switch issue.Code {
		case IssueNoGroups, IssueEmptyGroup, IssueMissingTitle, IssueMissingDescription:
			// Work in progress, not a defect.
			continue
		default:
			kept.Issues = append(kept.Issues, issue)
		}
	}
	return kept
}

// markUserEdits stamps AuthorUser on content a person changed, leaving
// untouched model-authored elements marked as the model's (FR-57).
func markUserEdits(previous, next PlanContent) PlanContent {
	out := next.Clone()

	priorGroups := map[string]TaskGroup{}
	priorItems := map[string]TaskItem{}
	for _, group := range previous.Groups {
		priorGroups[group.ID] = group
		for _, item := range group.Items {
			priorItems[item.ID] = item
		}
	}

	for gi := range out.Groups {
		group := &out.Groups[gi]
		prior, existed := priorGroups[group.ID]
		if !existed || groupTextChanged(prior, *group) {
			group.Author = AuthorUser
		} else {
			group.Author = prior.Author
		}
		for ii := range group.Items {
			item := &group.Items[ii]
			priorItem, itemExisted := priorItems[item.ID]
			if !itemExisted || itemTextChanged(priorItem, *item) {
				item.Author = AuthorUser
				continue
			}
			item.Author = priorItem.Author
		}
	}
	return out
}

func groupTextChanged(prior, next TaskGroup) bool {
	return prior.Title != next.Title || prior.Outcome != next.Outcome || prior.Notes != next.Notes
}

func itemTextChanged(prior, next TaskItem) bool {
	return prior.Description != next.Description ||
		prior.Details != next.Details ||
		prior.Assignee != next.Assignee ||
		prior.ExpectedResult != next.ExpectedResult
}

// snapshotDraft records a recovery point and prunes to the retained count.
// Snapshots are never review versions (FR-30).
func (s *Service) snapshotDraft(ctx context.Context, plan *Plan) error {
	return s.store.PutDraftSnapshot(ctx, &DraftSnapshot{
		PlanID:        plan.ID,
		WorkspaceID:   plan.WorkspaceID,
		DraftRevision: plan.DraftRevision,
		Title:         plan.Title,
		Objective:     plan.Objective,
		Content:       plan.Draft,
		CreatedAt:     s.now(),
	}, MaxDraftSnapshots)
}

// MaxDraftSnapshots is how many autosave recovery points a Plan retains
// (FR-30).
const MaxDraftSnapshots = 10

// DraftSnapshotTTL is how long a recovery point is kept (FR-30).
const DraftSnapshotTTL = 30 * 24 * time.Hour

// Snapshots returns the Plan's recovery points, newest first.
func (s *Service) Snapshots(ctx context.Context, workspaceID, planID string) ([]*DraftSnapshot, error) {
	return s.store.ListDraftSnapshots(ctx, workspaceID, planID)
}

// RecoverSnapshot restores a recovery point into the working draft.
//
// Recovery is an ordinary draft write: it takes the current revision, so
// recovering does not silently beat a concurrent edit, and the recovered
// content can itself be recovered from later (FR-30).
func (s *Service) RecoverSnapshot(ctx context.Context, workspaceID, planID, snapshotID, actor string) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	snapshots, err := s.store.ListDraftSnapshots(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}

	for _, snapshot := range snapshots {
		if snapshot.ID != snapshotID {
			continue
		}
		// Snapshot the current draft first, so recovering is itself undoable.
		if err := s.snapshotDraft(ctx, plan); err != nil {
			return nil, err
		}
		if _, err := s.store.UpdatePlanDraft(ctx, workspaceID, planID, plan.DraftRevision, DraftUpdate{
			Title:     snapshot.Title,
			Objective: snapshot.Objective,
			Content:   snapshot.Content,
			Intent:    plan.DraftIntent,
			UpdatedAt: s.now(),
		}); err != nil {
			return nil, err
		}
		entry := NewActivity(plan, ActivityDraftRecovered, SourceUser, actor,
			"recovered an autosave snapshot")
		entry.CreatedAt = s.now()
		if _, err := s.store.AppendActivity(ctx, entry); err != nil {
			return nil, err
		}
		return s.Get(ctx, workspaceID, planID)
	}
	return nil, fmt.Errorf("%w: snapshot %s", ErrPlanNotFound, snapshotID)
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func orDefaultIntent(value, fallback RevisionIntent) RevisionIntent {
	if value != "" {
		return value
	}
	return fallback
}
