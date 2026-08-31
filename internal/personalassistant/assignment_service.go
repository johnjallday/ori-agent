package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

// AssignmentPreviewStore is the atomic preview journal boundary.
type AssignmentPreviewStore interface {
	GetState(ctx context.Context, userID string) (*State, error)
	GetAssignment(ctx context.Context, userID, previewID string) (*Assignment, error)
	GetLatestAssignment(ctx context.Context, userID, assistantID string) (*Assignment, error)
	SupersedeAndCreateAssignment(ctx context.Context, assignment *Assignment, prior *Assignment, expectedStateVersion int64) (*Assignment, *State, error)
	BeginAssignmentApply(ctx context.Context, assignment *Assignment, expectedPreviewVersion, expectedStateVersion int64, requestID string) (*Assignment, *State, error)
	UpdateAssignment(ctx context.Context, assignment *Assignment, expectedVersion int64) (*Assignment, error)
	CompleteAssignmentApply(ctx context.Context, assignment *Assignment, expectedStateVersion int64) (*Assignment, *State, error)
}

// AssignmentPreviewResult binds one exact preview to the relationship version
// clients must use for the next mutation.
type AssignmentPreviewResult struct {
	Preview      *AssignmentPreview    `json:"preview"`
	StateVersion int64                 `json:"state_version"`
	Status       FirstAssignmentStatus `json:"first_assignment_status"`
}

// AssignmentPreviewConflictError carries bounded current versions so a client
// can replace stale editable state without guessing.
type AssignmentPreviewConflictError struct {
	StateVersion int64
	Preview      *AssignmentPreview
	Err          error
}

func (e *AssignmentPreviewConflictError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return ErrConflict.Error()
}
func (e *AssignmentPreviewConflictError) Unwrap() error { return ErrConflict }

// AssignmentService owns deterministic preview/apply orchestration. Apply is
// added separately; this first slice persists only immutable previews.
type AssignmentService struct {
	store  AssignmentPreviewStore
	writer AssignmentCanonicalWriter
	brief  AssignmentBriefService
	fault  AssignmentFaultInjector
	mu     sync.Mutex
}

func NewAssignmentService(store AssignmentPreviewStore) *AssignmentService {
	return &AssignmentService{store: store}
}

// AssignmentCanonicalWriter is the only route from apply orchestration into
// canonical product records.
// AssignmentBriefService is the existing Daily Brief lifecycle boundary.
type AssignmentBriefService interface {
	GetConfig(ctx context.Context, workspaceID string) (*dailybrief.Config, error)
	PlanFirstAssignmentBrief(ctx context.Context, workspaceID string) (*dailybrief.Config, dailybrief.Trigger, error)
	GenerateFirstAssignmentBrief(ctx context.Context, cfg dailybrief.Config, userID string, trigger dailybrief.Trigger, requestID string) (*dailybrief.GenerationRequest, *dailybrief.Revision, error)
	GetRevision(ctx context.Context, revisionID string) (*dailybrief.Revision, error)
}

type AssignmentCanonicalWriter interface {
	CreateTicket(workspaceID, assistantID, previewID string, item AssignmentPreviewItem) (CanonicalRef, bool, error)
	CreateFollowUp(ctx context.Context, userID, workspaceID, assistantID, previewID string, item AssignmentPreviewItem) (CanonicalRef, error)
}

// AssignmentFaultInjector is test-only fault placement around durable item
// boundaries. Production leaves it nil.
type AssignmentFaultInjector func(stage string, itemIndex int) error

const (
	AssignmentFaultAfterCanonical = "after_canonical"
	AssignmentFaultAfterRef       = "after_ref"
	AssignmentFaultBeforeComplete = "before_complete"
)

func (s *AssignmentService) SetCanonicalWriter(writer AssignmentCanonicalWriter) {
	if s != nil {
		s.writer = writer
	}
}

func (s *AssignmentService) SetBriefService(brief AssignmentBriefService) {
	if s != nil {
		s.brief = brief
	}
}

func (s *AssignmentService) SetFaultInjector(fault AssignmentFaultInjector) {
	if s != nil {
		s.fault = fault
	}
}

// AssignmentCurrentResult supports restart-safe preview/apply resume without
// exposing normalized payload JSON or internal failure causes.
type AssignmentCurrentResult struct {
	Preview        *AssignmentPreview    `json:"preview,omitempty"`
	StateVersion   int64                 `json:"state_version"`
	Status         AssignmentStatus      `json:"status,omitempty"`
	ApplyRequestID string                `json:"apply_request_id,omitempty"`
	Brief          *FirstBriefProjection `json:"brief,omitempty"`
}

func (s *AssignmentService) Current(ctx context.Context, userID string) (*AssignmentCurrentResult, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("personal assistant: assignment service is not configured")
	}
	state, err := s.store.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := &AssignmentCurrentResult{StateVersion: state.StateVersion}
	assignment, err := s.store.GetLatestAssignment(ctx, state.UserID, state.AssistantID)
	if errors.Is(err, ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result.Preview, err = assignmentPreviewFromRecord(assignment)
	if err != nil {
		return nil, err
	}
	result.Status = assignment.Status
	result.ApplyRequestID = assignment.ApplyRequestID
	if assignment.BriefRequestID != "" {
		result.Brief = s.firstBriefProjection(ctx, assignment, state.HQWorkspaceID)
	}
	return result, nil
}

// Preview validates before persistence and atomically supersedes the previous
// unapplied preview while advancing relationship state.
func (s *AssignmentService) Preview(ctx context.Context, userID string, ifVersion int64, input AssignmentInput) (*AssignmentPreviewResult, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("personal assistant: assignment service is not configured")
	}
	if ifVersion < 1 {
		return nil, fmt.Errorf("%w: if_version must be positive", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.store.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	if state.Status != StatusActive || state.HQWorkspaceID == "" || state.HQEntryAgentInstanceID == "" {
		return nil, &AssignmentPreviewConflictError{StateVersion: state.StateVersion, Err: ErrConflict}
	}
	latest, err := s.store.GetLatestAssignment(ctx, state.UserID, state.AssistantID)
	if errors.Is(err, ErrNotFound) {
		latest = nil
	} else if err != nil {
		return nil, err
	}
	if state.StateVersion != ifVersion || state.FirstAssignmentStatus == FirstAssignmentCompleted ||
		(latest != nil && (latest.Status == AssignmentApplying || latest.Status == AssignmentCompleted)) {
		return nil, previewConflict(state, latest)
	}

	nextVersion := int64(1)
	if latest != nil {
		nextVersion = latest.AssignmentVersion + 1
	}
	preview, normalizedPayload, err := BuildAssignmentPreview(NewAssignmentPreviewID(), nextVersion, input)
	if err != nil {
		return nil, err
	}
	assignment := &Assignment{
		PreviewID: preview.PreviewID, UserID: state.UserID, AssistantID: state.AssistantID,
		AssignmentVersion: nextVersion, NormalizedPayload: normalizedPayload,
		NormalizedPayloadHash: preview.PayloadHash, Status: AssignmentPreviewed,
		CreatedCanonicalRefs: []CanonicalRef{},
	}
	created, updatedState, err := s.store.SupersedeAndCreateAssignment(ctx, assignment, latest, state.StateVersion)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			currentState, _ := s.store.GetState(ctx, state.UserID)
			currentLatest, _ := s.store.GetLatestAssignment(ctx, state.UserID, state.AssistantID)
			return nil, previewConflict(currentState, currentLatest)
		}
		return nil, err
	}
	createdPreview, err := assignmentPreviewFromRecord(created)
	if err != nil {
		return nil, err
	}
	return &AssignmentPreviewResult{
		Preview: createdPreview, StateVersion: updatedState.StateVersion,
		Status: updatedState.FirstAssignmentStatus,
	}, nil
}

// AssignmentApplyRequest identifies one exact reviewed preview and one stable
// client retry operation.
type AssignmentApplyRequest struct {
	PreviewID      string `json:"preview_id"`
	PreviewVersion int64  `json:"preview_version"`
	PayloadHash    string `json:"payload_hash"`
	IfVersion      int64  `json:"if_version"`
	ApplyRequestID string `json:"apply_request_id"`
}

// AssignmentApplyResult is safe to return for complete and partial attempts.
type FirstBriefTopItem struct {
	Title string               `json:"title"`
	Ref   dailybrief.SourceRef `json:"ref"`
}

type FirstBriefProjection struct {
	RequestID            string              `json:"request_id,omitempty"`
	RevisionID           string              `json:"revision_id,omitempty"`
	Status               string              `json:"status"`
	Route                string              `json:"route"`
	TopItems             []FirstBriefTopItem `json:"top_items"`
	NextScheduledCheckIn string              `json:"next_scheduled_check_in,omitempty"`
}

type AssignmentApplyResult struct {
	PreviewID            string                `json:"preview_id"`
	AssignmentVersion    int64                 `json:"assignment_version"`
	StateVersion         int64                 `json:"state_version"`
	Status               AssignmentStatus      `json:"status"`
	AppliedCount         int                   `json:"applied_count"`
	TotalCount           int                   `json:"total_count"`
	CreatedCanonicalRefs []CanonicalRef        `json:"created_canonical_refs"`
	Retryable            bool                  `json:"retryable"`
	Outcome              string                `json:"outcome"`
	Brief                *FirstBriefProjection `json:"brief,omitempty"`
}

// PartialAssignmentError means visible canonical records were preserved and a
// retry with the same request identity may continue.
type PartialAssignmentError struct {
	Result *AssignmentApplyResult
	Err    error
}

func (e *PartialAssignmentError) Error() string {
	return "personal assistant: first assignment is partially applied"
}
func (e *PartialAssignmentError) Unwrap() error { return e.Err }

// Apply materializes the exact persisted preview through idempotent canonical
// services, journaling each stable reference before moving to the next item.
func (s *AssignmentService) Apply(ctx context.Context, userID string, request AssignmentApplyRequest) (*AssignmentApplyResult, error) {
	if s == nil || s.store == nil || s.writer == nil || s.brief == nil {
		return nil, errors.New("personal assistant: assignment apply is not configured")
	}
	previewID, err := validateOpaqueID("preview id", request.PreviewID, true)
	if err != nil || request.PreviewVersion < 1 || request.IfVersion < 1 {
		return nil, fmt.Errorf("%w: invalid apply version or preview identity", ErrValidation)
	}
	requestID, err := validateOpaqueID("apply request id", request.ApplyRequestID, true)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid apply request identity", ErrValidation)
	}
	payloadHash, err := validateOpaqueID("assignment payload hash", request.PayloadHash, true)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid assignment payload hash", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.store.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	assignment, err := s.store.GetAssignment(ctx, userID, previewID)
	if err != nil {
		return nil, err
	}
	if state.Status != StatusActive || state.AssistantID != assignment.AssistantID ||
		assignment.NormalizedPayloadHash != payloadHash || assignment.Status == AssignmentSuperseded {
		return nil, previewConflict(state, assignment)
	}
	isReplay := (assignment.Status == AssignmentApplying || assignment.Status == AssignmentCompleted) && assignment.ApplyRequestID == requestID
	if !isReplay && (state.StateVersion != request.IfVersion || assignment.AssignmentVersion != request.PreviewVersion || assignment.Status != AssignmentPreviewed) {
		return nil, previewConflict(state, assignment)
	}
	assignment, state, err = s.store.BeginAssignmentApply(ctx, assignment, request.PreviewVersion, state.StateVersion, requestID)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			currentState, _ := s.store.GetState(ctx, userID)
			current, _ := s.store.GetAssignment(ctx, userID, previewID)
			return nil, previewConflict(currentState, current)
		}
		return nil, err
	}
	preview, err := assignmentPreviewFromRecord(assignment)
	if err != nil {
		return nil, partialAssignmentResult(assignment, state, nil, err)
	}
	if assignment.Status == AssignmentCompleted {
		result := assignmentApplyResult(assignment, state, preview.Count, false)
		result.Brief = s.firstBriefProjection(ctx, assignment, state.HQWorkspaceID)
		return result, nil
	}

	for index, item := range preview.Items {
		var ref CanonicalRef
		switch item.RecordType {
		case AssignmentRecordTicket:
			ref, _, err = s.writer.CreateTicket(state.HQWorkspaceID, state.AssistantID, assignment.PreviewID, item)
		case AssignmentRecordFollowUp:
			ref, err = s.writer.CreateFollowUp(ctx, state.UserID, state.HQWorkspaceID, state.AssistantID, assignment.PreviewID, item)
		default:
			err = fmt.Errorf("%w: unsupported preview record type", ErrValidation)
		}
		if err != nil {
			return nil, partialAssignmentResult(assignment, state, preview, err)
		}
		if s.fault != nil {
			if err = s.fault(AssignmentFaultAfterCanonical, index); err != nil {
				return nil, partialAssignmentResult(assignment, state, preview, err)
			}
		}
		if !containsCanonicalRef(assignment.CreatedCanonicalRefs, ref) {
			next := assignment.Clone()
			next.CreatedCanonicalRefs = append(next.CreatedCanonicalRefs, ref)
			updated, updateErr := s.store.UpdateAssignment(ctx, next, assignment.AssignmentVersion)
			if updateErr != nil {
				return nil, partialAssignmentResult(assignment, state, preview, updateErr)
			}
			assignment = updated
		}
		if s.fault != nil {
			if err = s.fault(AssignmentFaultAfterRef, index); err != nil {
				return nil, partialAssignmentResult(assignment, state, preview, err)
			}
		}
	}
	assignment, briefProjection, err := s.generateFirstBrief(ctx, assignment, state)
	if err != nil {
		partial := partialAssignmentResult(assignment, state, preview, err)
		partial.Result.Outcome = "records_saved_brief_failed"
		partial.Result.Brief = briefProjection
		return nil, partial
	}
	if s.fault != nil {
		if err = s.fault(AssignmentFaultBeforeComplete, len(preview.Items)); err != nil {
			partial := partialAssignmentResult(assignment, state, preview, err)
			partial.Result.Brief = briefProjection
			return nil, partial
		}
	}
	completedAssignment, completedState, err := s.store.CompleteAssignmentApply(ctx, assignment, state.StateVersion)
	if err != nil {
		return nil, partialAssignmentResult(assignment, state, preview, err)
	}
	result := assignmentApplyResult(completedAssignment, completedState, preview.Count, false)
	result.Brief = briefProjection
	if preview.Count == 0 {
		result.Outcome = "complete_empty"
	}
	return result, nil
}

func (s *AssignmentService) generateFirstBrief(ctx context.Context, assignment *Assignment, state *State) (*Assignment, *FirstBriefProjection, error) {
	if assignment.BriefRevisionID != "" &&
		(assignment.BriefStatus == string(dailybrief.GenerationSucceeded) || assignment.BriefStatus == string(dailybrief.GenerationPartial)) {
		return assignment, s.firstBriefProjection(ctx, assignment, state.HQWorkspaceID), nil
	}
	var cfg *dailybrief.Config
	var trigger dailybrief.Trigger
	var err error
	if assignment.BriefRequestID == "" || assignment.BriefStatus == string(dailybrief.GenerationFailed) {
		cfg, trigger, err = s.brief.PlanFirstAssignmentBrief(ctx, state.HQWorkspaceID)
		if err != nil {
			return assignment, firstBriefProjectionFromAssignment(assignment), err
		}
		next := assignment.Clone()
		next.BriefRequestID = "first-brief-" + uuid.NewString()
		next.BriefRevisionID = ""
		next.BriefStatus = string(dailybrief.GenerationPending)
		next.BriefTrigger = string(trigger)
		assignment, err = s.store.UpdateAssignment(ctx, next, assignment.AssignmentVersion)
		if err != nil {
			return next, firstBriefProjectionFromAssignment(next), err
		}
	} else {
		cfg, err = s.brief.GetConfig(ctx, state.HQWorkspaceID)
		if err != nil {
			return assignment, firstBriefProjectionFromAssignment(assignment), err
		}
		trigger = dailybrief.Trigger(assignment.BriefTrigger)
		if trigger != dailybrief.TriggerFirstOpen && trigger != dailybrief.TriggerManual {
			return assignment, firstBriefProjectionFromAssignment(assignment), errors.New("personal assistant: invalid persisted first brief trigger")
		}
	}

	claim, revision, generationErr := s.brief.GenerateFirstAssignmentBrief(
		ctx, *cfg, state.UserID, trigger, assignment.BriefRequestID,
	)
	next := assignment.Clone()
	if claim != nil {
		next.BriefStatus = string(claim.Status)
		next.BriefRevisionID = claim.RevisionID
	} else {
		next.BriefStatus = string(dailybrief.GenerationFailed)
	}
	if revision != nil {
		next.BriefRevisionID = revision.ID
		next.BriefStatus = string(revision.Status)
	}
	updated, updateErr := s.store.UpdateAssignment(ctx, next, assignment.AssignmentVersion)
	if updateErr != nil {
		return assignment, firstBriefProjectionFromAssignment(next), updateErr
	}
	projection := buildFirstBriefProjection(updated, cfg, revision)
	if generationErr != nil {
		return updated, projection, generationErr
	}
	if revision == nil || (revision.Status != dailybrief.GenerationSucceeded && revision.Status != dailybrief.GenerationPartial) {
		return updated, projection, errors.New("personal assistant: first Daily Brief did not complete")
	}
	return updated, projection, nil
}

func (s *AssignmentService) firstBriefProjection(ctx context.Context, assignment *Assignment, workspaceID string) *FirstBriefProjection {
	if assignment == nil {
		return nil
	}
	cfg, _ := s.brief.GetConfig(ctx, workspaceID)
	var revision *dailybrief.Revision
	if assignment.BriefRevisionID != "" {
		revision, _ = s.brief.GetRevision(ctx, assignment.BriefRevisionID)
	}
	return buildFirstBriefProjection(assignment, cfg, revision)
}

func firstBriefProjectionFromAssignment(assignment *Assignment) *FirstBriefProjection {
	return buildFirstBriefProjection(assignment, nil, nil)
}

func buildFirstBriefProjection(assignment *Assignment, cfg *dailybrief.Config, revision *dailybrief.Revision) *FirstBriefProjection {
	if assignment == nil || (assignment.BriefRequestID == "" && assignment.BriefStatus == "") {
		return nil
	}
	projection := &FirstBriefProjection{
		RequestID: assignment.BriefRequestID, RevisionID: assignment.BriefRevisionID,
		Status: assignment.BriefStatus, Route: "/api/personal-hq/brief/current", TopItems: []FirstBriefTopItem{},
	}
	if revision != nil {
		projection.RevisionID = revision.ID
		projection.Status = string(revision.Status)
		var content dailybrief.BriefContent
		if json.Unmarshal([]byte(revision.ContentJSON), &content) == nil {
			seen := map[string]bool{}
			appendTitle := func(title string, ref dailybrief.SourceRef) {
				title = strings.TrimSpace(title)
				key := ref.Key()
				if title == "" || key == "::" || seen[key] || len(projection.TopItems) >= 3 {
					return
				}
				seen[key] = true
				projection.TopItems = append(projection.TopItems, FirstBriefTopItem{
					Title: truncateRunes(title, 200), Ref: ref,
				})
			}
			for _, item := range content.NeedsAttention {
				appendTitle(item.Title, item.Ref)
			}
			for _, item := range content.TodaysPlan {
				appendTitle(item.Title, item.Ref)
			}
			for _, item := range content.SinceLastBrief {
				appendTitle(item.Title, item.Ref)
			}
		}
	}
	if cfg != nil {
		if next, ok, err := dailybrief.NextOccurrence(*cfg, time.Now()); err == nil && ok {
			projection.NextScheduledCheckIn = next.Format(time.RFC3339)
		}
	}
	return projection
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func containsCanonicalRef(refs []CanonicalRef, target CanonicalRef) bool {
	for _, ref := range refs {
		if ref.Kind == target.Kind && ref.WorkspaceID == target.WorkspaceID && ref.ID == target.ID {
			return true
		}
	}
	return false
}

func assignmentApplyResult(assignment *Assignment, state *State, total int, retryable bool) *AssignmentApplyResult {
	outcome := "complete"
	if retryable {
		outcome = "records_saved_apply_partial"
	}
	result := &AssignmentApplyResult{TotalCount: total, Retryable: retryable, Outcome: outcome}
	if assignment != nil {
		result.PreviewID = assignment.PreviewID
		result.AssignmentVersion = assignment.AssignmentVersion
		result.Status = assignment.Status
		result.CreatedCanonicalRefs = append([]CanonicalRef(nil), assignment.CreatedCanonicalRefs...)
		result.AppliedCount = len(result.CreatedCanonicalRefs)
	}
	if result.CreatedCanonicalRefs == nil {
		result.CreatedCanonicalRefs = []CanonicalRef{}
	}
	if state != nil {
		result.StateVersion = state.StateVersion
	}
	return result
}

func partialAssignmentResult(assignment *Assignment, state *State, preview *AssignmentPreview, err error) *PartialAssignmentError {
	total := 0
	if preview != nil {
		total = preview.Count
	}
	return &PartialAssignmentError{Result: assignmentApplyResult(assignment, state, total, true), Err: err}
}

func previewConflict(state *State, assignment *Assignment) *AssignmentPreviewConflictError {
	conflict := &AssignmentPreviewConflictError{Err: ErrConflict}
	if state != nil {
		conflict.StateVersion = state.StateVersion
	}
	if assignment != nil {
		conflict.Preview, _ = assignmentPreviewFromRecord(assignment)
	}
	return conflict
}

func assignmentPreviewFromRecord(assignment *Assignment) (*AssignmentPreview, error) {
	if assignment == nil {
		return nil, nil
	}
	var payload struct {
		PreviewID string                  `json:"preview_id"`
		Items     []AssignmentPreviewItem `json:"items"`
	}
	if err := json.Unmarshal(assignment.NormalizedPayload, &payload); err != nil {
		return nil, fmt.Errorf("personal assistant: decode assignment preview: %w", err)
	}
	if payload.PreviewID != assignment.PreviewID {
		return nil, errors.New("personal assistant: assignment preview identity mismatch")
	}
	return &AssignmentPreview{
		PreviewID: assignment.PreviewID, AssignmentVersion: assignment.AssignmentVersion,
		PayloadHash: assignment.NormalizedPayloadHash, Items: payload.Items, Count: len(payload.Items),
	}, nil
}
