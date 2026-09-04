package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	assistantPortfolioMaxProjects   = 32
	assistantPortfolioMaxMilestones = 16
	assistantPortfolioMaxListItems  = 16
	assistantPortfolioMaxReceipts   = 32
	assistantPortfolioReviewTTL     = 10 * time.Minute
	assistantPortfolioMaxTicketRead = 5
)

var (
	ErrAssistantPortfolioInvalid       = errors.New("assistant portfolio request is invalid")
	ErrAssistantPortfolioLinkNotFound  = errors.New("assistant portfolio project link was not found")
	ErrAssistantPortfolioConflict      = errors.New("assistant portfolio state changed")
	ErrAssistantPortfolioReviewExpired = errors.New("assistant portfolio review expired")
	ErrAssistantPortfolioIdempotency   = errors.New("assistant portfolio idempotency conflict")
	assistantPortfolioMu               sync.Mutex
)

type AssistantPortfolioUpdate struct {
	Status             string                        `json:"status"`
	Priority           int                           `json:"priority,omitempty"`
	Milestones         []AssistantPortfolioMilestone `json:"milestones,omitempty"`
	SessionDate        string                        `json:"session_date,omitempty"`
	ReleaseDate        string                        `json:"release_date,omitempty"`
	Blockers           []string                      `json:"blockers,omitempty"`
	Deliverables       []string                      `json:"deliverables,omitempty"`
	ArchiveReviewState string                        `json:"archive_review_state"`
}

type AssistantPortfolioTicketSummary struct {
	Number int64       `json:"number,omitempty"`
	Title  string      `json:"title"`
	State  TicketState `json:"state"`
}

type AssistantPortfolioProjectProjection struct {
	LinkID             string                            `json:"link_id"`
	ProjectWorkspaceID string                            `json:"project_workspace_id"`
	ProjectName        string                            `json:"project_name"`
	StateRevision      int64                             `json:"state_revision"`
	Fields             AssistantPortfolioUpdate          `json:"fields"`
	OpenTicketCount    int                               `json:"open_ticket_count"`
	TicketSummaries    []AssistantPortfolioTicketSummary `json:"ticket_summaries,omitempty"`
	ArchiveGuidance    []string                          `json:"archive_guidance,omitempty"`
}

type AssistantPortfolioReview struct {
	Token     string                              `json:"token"`
	ExpiresAt time.Time                           `json:"expires_at"`
	Project   AssistantPortfolioProjectProjection `json:"project"`
}

type AssistantPortfolioReceipt struct {
	LinkID             string    `json:"link_id"`
	ProjectWorkspaceID string    `json:"project_workspace_id"`
	StateRevision      int64     `json:"state_revision"`
	RecordedAt         time.Time `json:"recorded_at"`
	Replayed           bool      `json:"replayed,omitempty"`
}

type AssistantPortfolioService struct {
	store Store
	now   func() time.Time
}

func NewAssistantPortfolioService(store Store) *AssistantPortfolioService {
	return &AssistantPortfolioService{store: store, now: time.Now}
}

func (service *AssistantPortfolioService) SetClock(now func() time.Time) {
	if service != nil && now != nil {
		service.now = now
	}
}

func (service *AssistantPortfolioService) List(stationID string) ([]AssistantPortfolioProjectProjection, error) {
	station, state, err := service.station(stationID)
	if err != nil {
		return nil, err
	}
	projections := make([]AssistantPortfolioProjectProjection, 0, len(state.LinkedProjectIDs))
	for _, projectID := range state.LinkedProjectIDs {
		project, getErr := service.store.Get(projectID)
		if getErr != nil || project == nil {
			continue
		}
		link := project.GetAssistantProjectLink()
		if link == nil || link.ID == "" || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() {
			continue
		}
		fields := defaultAssistantPortfolioUpdate()
		for _, entry := range state.Portfolio.Projects {
			if entry.LinkID == link.ID && entry.ProjectWorkspaceID == project.ID {
				fields = portfolioUpdateFromProject(entry)
				break
			}
		}
		projections = append(projections, service.projectProjection(project, link.ID, state.Portfolio.StateRevision, fields))
	}
	sort.Slice(projections, func(i, j int) bool {
		if projections[i].ProjectName == projections[j].ProjectName {
			return projections[i].LinkID < projections[j].LinkID
		}
		return projections[i].ProjectName < projections[j].ProjectName
	})
	return projections, nil
}

func (service *AssistantPortfolioService) Review(stationID, linkID string, expectedRevision int64, update AssistantPortfolioUpdate) (*AssistantPortfolioReview, error) {
	assistantPortfolioMu.Lock()
	defer assistantPortfolioMu.Unlock()
	if expectedRevision < 0 {
		return nil, ErrAssistantPortfolioInvalid
	}
	normalized, err := normalizeAssistantPortfolioUpdate(update)
	if err != nil {
		return nil, err
	}
	station, state, err := service.station(stationID)
	if err != nil || state.Portfolio.StateRevision != expectedRevision {
		return nil, ErrAssistantPortfolioConflict
	}
	project, _, err := service.linkedProject(station, state, linkID)
	if err != nil {
		return nil, err
	}
	digest := assistantPortfolioDigest(linkID, normalized)
	now := service.now().UTC()
	receipt := AssistantPortfolioReviewReceipt{
		Token: uuid.NewString(), LinkID: linkID, InputDigest: digest,
		StateRevision: expectedRevision, ExpiresAt: now.Add(assistantPortfolioReviewTTL),
	}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.Portfolio.StateRevision != expectedRevision {
			return ErrAssistantPortfolioConflict
		}
		currentState.Portfolio.ReviewReceipts = appendBoundedPortfolioReview(currentState.Portfolio.ReviewReceipts, receipt, now)
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		return nil, ErrAssistantPortfolioConflict
	}
	return &AssistantPortfolioReview{Token: receipt.Token, ExpiresAt: receipt.ExpiresAt, Project: service.projectProjection(project, linkID, expectedRevision, normalized)}, nil
}

func (service *AssistantPortfolioService) Commit(stationID, token, idempotencyKey string, update AssistantPortfolioUpdate) (*AssistantPortfolioReceipt, error) {
	assistantPortfolioMu.Lock()
	defer assistantPortfolioMu.Unlock()
	token = strings.TrimSpace(token)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if token == "" || idempotencyKey == "" || len(token) > 160 || len(idempotencyKey) > 160 {
		return nil, ErrAssistantPortfolioInvalid
	}
	normalized, err := normalizeAssistantPortfolioUpdate(update)
	if err != nil {
		return nil, err
	}
	station, state, err := service.station(stationID)
	if err != nil {
		return nil, err
	}
	var review *AssistantPortfolioReviewReceipt
	for index := range state.Portfolio.ReviewReceipts {
		candidate := &state.Portfolio.ReviewReceipts[index]
		if candidate.Token == token {
			review = candidate
			break
		}
	}
	if review == nil || review.InputDigest != assistantPortfolioDigest(review.LinkID, normalized) {
		return nil, ErrAssistantPortfolioReviewExpired
	}
	for _, prior := range state.Portfolio.OperationReceipts {
		if prior.IdempotencyKey != idempotencyKey {
			continue
		}
		if prior.InputDigest != review.InputDigest || prior.LinkID != review.LinkID {
			return nil, ErrAssistantPortfolioIdempotency
		}
		return &AssistantPortfolioReceipt{LinkID: prior.LinkID, ProjectWorkspaceID: prior.ProjectWorkspaceID, StateRevision: prior.StateRevision, RecordedAt: prior.RecordedAt, Replayed: true}, nil
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(service.now().UTC()) {
		return nil, ErrAssistantPortfolioReviewExpired
	}
	project, _, err := service.linkedProject(station, state, review.LinkID)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	var result AssistantPortfolioReceipt
	err = service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.Portfolio.StateRevision != review.StateRevision {
			return ErrAssistantPortfolioConflict
		}
		var persistedReview *AssistantPortfolioReviewReceipt
		for index := range currentState.Portfolio.ReviewReceipts {
			if currentState.Portfolio.ReviewReceipts[index].Token == token {
				persistedReview = &currentState.Portfolio.ReviewReceipts[index]
				break
			}
		}
		if persistedReview == nil || persistedReview.ConsumedAt != nil || !persistedReview.ExpiresAt.After(now) || persistedReview.InputDigest != review.InputDigest {
			return ErrAssistantPortfolioReviewExpired
		}
		entry := AssistantPortfolioProject{
			LinkID: review.LinkID, ProjectWorkspaceID: project.ID,
			Status: normalized.Status, Priority: normalized.Priority,
			Milestones:  append([]AssistantPortfolioMilestone(nil), normalized.Milestones...),
			SessionDate: normalized.SessionDate, ReleaseDate: normalized.ReleaseDate,
			Blockers: append([]string(nil), normalized.Blockers...), Deliverables: append([]string(nil), normalized.Deliverables...),
			ArchiveReviewState: normalized.ArchiveReviewState, UpdatedAt: now,
		}
		replaced := false
		for index := range currentState.Portfolio.Projects {
			if currentState.Portfolio.Projects[index].LinkID == review.LinkID {
				currentState.Portfolio.Projects[index] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			if len(currentState.Portfolio.Projects) >= assistantPortfolioMaxProjects {
				return ErrAssistantPortfolioInvalid
			}
			currentState.Portfolio.Projects = append(currentState.Portfolio.Projects, entry)
		}
		currentState.Portfolio.StateRevision++
		persistedReview.ConsumedAt = &now
		op := AssistantPortfolioOperationReceipt{
			IdempotencyKey: idempotencyKey, InputDigest: review.InputDigest, LinkID: review.LinkID,
			ProjectWorkspaceID: project.ID, StateRevision: currentState.Portfolio.StateRevision, RecordedAt: now,
		}
		currentState.Portfolio.OperationReceipts = appendBoundedPortfolioOperation(currentState.Portfolio.OperationReceipts, op)
		current.SetAssistantProgramState(currentState)
		result = AssistantPortfolioReceipt{LinkID: review.LinkID, ProjectWorkspaceID: project.ID, StateRevision: op.StateRevision, RecordedAt: now}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (service *AssistantPortfolioService) station(stationID string) (*Workspace, *AssistantProgramState, error) {
	if service == nil || service.store == nil || strings.TrimSpace(stationID) == "" {
		return nil, nil, ErrAssistantPortfolioInvalid
	}
	station, err := service.store.Get(strings.TrimSpace(stationID))
	if err != nil || station == nil {
		return nil, nil, ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.SchemaVersion < AssistantProgramStateSchemaVersion {
		return nil, nil, ErrAssistantProgramUnavailable
	}
	return station, state, nil
}

func (service *AssistantPortfolioService) linkedProject(station *Workspace, state *AssistantProgramState, linkID string) (*Workspace, *AssistantProjectLink, error) {
	linkID = strings.TrimSpace(linkID)
	if linkID == "" || len(linkID) > 160 {
		return nil, nil, ErrAssistantPortfolioInvalid
	}
	var foundProject *Workspace
	var foundLink *AssistantProjectLink
	for _, projectID := range state.LinkedProjectIDs {
		project, err := service.store.Get(projectID)
		if err != nil || project == nil {
			continue
		}
		link := project.GetAssistantProjectLink()
		if link == nil || link.ID != linkID || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() {
			continue
		}
		if foundProject != nil {
			return nil, nil, ErrAssistantPortfolioConflict
		}
		foundProject, foundLink = project, link
	}
	if foundProject == nil {
		return nil, nil, ErrAssistantPortfolioLinkNotFound
	}
	return foundProject, foundLink, nil
}

func (service *AssistantPortfolioService) projectProjection(project *Workspace, linkID string, revision int64, fields AssistantPortfolioUpdate) AssistantPortfolioProjectProjection {
	projection := AssistantPortfolioProjectProjection{
		LinkID: linkID, ProjectWorkspaceID: project.ID, ProjectName: project.Name,
		StateRevision: revision, Fields: fields,
	}
	tickets, err := NewTicketService(service.store).List(TicketQuery{WorkspaceID: project.ID, IncludeSubtickets: false, Limit: assistantPortfolioMaxTicketRead})
	if err == nil {
		for _, ticket := range tickets {
			if !ticket.State.Terminal() {
				projection.OpenTicketCount++
			}
			if len(projection.TicketSummaries) < assistantPortfolioMaxTicketRead {
				projection.TicketSummaries = append(projection.TicketSummaries, AssistantPortfolioTicketSummary{Number: ticket.Number, Title: ticket.Title, State: ticket.State})
			}
		}
	}
	projection.ArchiveGuidance = assistantArchiveGuidance(fields, projection.OpenTicketCount)
	return projection
}

func assistantArchiveGuidance(fields AssistantPortfolioUpdate, openTickets int) []string {
	guidance := make([]string, 0, 4)
	if openTickets > 0 {
		guidance = append(guidance, "Review open project Tickets before archiving.")
	}
	if len(fields.Blockers) > 0 {
		guidance = append(guidance, "Resolve or explicitly retain the listed blockers.")
	}
	if len(fields.Deliverables) == 0 {
		guidance = append(guidance, "Record expected deliverables before the archive review.")
	}
	if fields.ArchiveReviewState == AssistantArchiveReviewReady {
		guidance = append(guidance, "The project is marked ready for a user-led archive review; no files will move automatically.")
	}
	if len(guidance) == 0 {
		guidance = append(guidance, "No deterministic archive preparation issue is currently recorded.")
	}
	return guidance
}

func defaultAssistantPortfolioUpdate() AssistantPortfolioUpdate {
	return AssistantPortfolioUpdate{Status: AssistantPortfolioStatusPlanning, ArchiveReviewState: AssistantArchiveReviewNotReady}
}

func portfolioUpdateFromProject(project AssistantPortfolioProject) AssistantPortfolioUpdate {
	return AssistantPortfolioUpdate{
		Status: project.Status, Priority: project.Priority,
		Milestones:  append([]AssistantPortfolioMilestone(nil), project.Milestones...),
		SessionDate: project.SessionDate, ReleaseDate: project.ReleaseDate,
		Blockers: append([]string(nil), project.Blockers...), Deliverables: append([]string(nil), project.Deliverables...),
		ArchiveReviewState: project.ArchiveReviewState,
	}
}

func normalizeAssistantPortfolioUpdate(input AssistantPortfolioUpdate) (AssistantPortfolioUpdate, error) {
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	input.ArchiveReviewState = strings.ToLower(strings.TrimSpace(input.ArchiveReviewState))
	if !validPortfolioStatus(input.Status) || !validArchiveReviewState(input.ArchiveReviewState) || input.Priority < 0 || input.Priority > 5 ||
		len(input.Milestones) > assistantPortfolioMaxMilestones || len(input.Blockers) > assistantPortfolioMaxListItems || len(input.Deliverables) > assistantPortfolioMaxListItems ||
		!validPortfolioDate(input.SessionDate) || !validPortfolioDate(input.ReleaseDate) {
		return AssistantPortfolioUpdate{}, ErrAssistantPortfolioInvalid
	}
	seenMilestones := make(map[string]struct{}, len(input.Milestones))
	for index := range input.Milestones {
		milestone := &input.Milestones[index]
		milestone.ID = strings.ToLower(strings.TrimSpace(milestone.ID))
		milestone.Label = strings.TrimSpace(milestone.Label)
		milestone.DueDate = strings.TrimSpace(milestone.DueDate)
		if !validPortfolioText(milestone.ID, 80) || !validPortfolioText(milestone.Label, 240) || !validPortfolioDate(milestone.DueDate) {
			return AssistantPortfolioUpdate{}, ErrAssistantPortfolioInvalid
		}
		if _, duplicate := seenMilestones[milestone.ID]; duplicate {
			return AssistantPortfolioUpdate{}, ErrAssistantPortfolioInvalid
		}
		seenMilestones[milestone.ID] = struct{}{}
	}
	var err error
	if input.Blockers, err = normalizePortfolioTextList(input.Blockers); err != nil {
		return AssistantPortfolioUpdate{}, err
	}
	if input.Deliverables, err = normalizePortfolioTextList(input.Deliverables); err != nil {
		return AssistantPortfolioUpdate{}, err
	}
	input.SessionDate = strings.TrimSpace(input.SessionDate)
	input.ReleaseDate = strings.TrimSpace(input.ReleaseDate)
	return input, nil
}

func normalizePortfolioTextList(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validPortfolioText(value, 240) {
			return nil, ErrAssistantPortfolioInvalid
		}
		key := strings.ToLower(value)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func validPortfolioText(value string, max int) bool {
	return value != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func validPortfolioDate(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func validPortfolioStatus(value string) bool {
	switch value {
	case AssistantPortfolioStatusPlanning, AssistantPortfolioStatusActive, AssistantPortfolioStatusOnHold, AssistantPortfolioStatusComplete, AssistantPortfolioStatusArchived:
		return true
	default:
		return false
	}
}

func validArchiveReviewState(value string) bool {
	switch value {
	case AssistantArchiveReviewNotReady, AssistantArchiveReviewReady, AssistantArchiveReviewReviewed:
		return true
	default:
		return false
	}
}

func assistantPortfolioDigest(linkID string, input AssistantPortfolioUpdate) string {
	encoded, _ := json.Marshal(struct {
		LinkID string
		Input  AssistantPortfolioUpdate
	}{LinkID: linkID, Input: input})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func appendBoundedPortfolioReview(receipts []AssistantPortfolioReviewReceipt, receipt AssistantPortfolioReviewReceipt, now time.Time) []AssistantPortfolioReviewReceipt {
	out := make([]AssistantPortfolioReviewReceipt, 0, assistantPortfolioMaxReceipts)
	for _, candidate := range receipts {
		if candidate.Token == receipt.Token || (!candidate.ExpiresAt.After(now) && candidate.ConsumedAt == nil) {
			continue
		}
		out = append(out, candidate)
	}
	out = append(out, receipt)
	if len(out) > assistantPortfolioMaxReceipts {
		out = out[len(out)-assistantPortfolioMaxReceipts:]
	}
	return out
}

func appendBoundedPortfolioOperation(receipts []AssistantPortfolioOperationReceipt, receipt AssistantPortfolioOperationReceipt) []AssistantPortfolioOperationReceipt {
	out := append(append([]AssistantPortfolioOperationReceipt(nil), receipts...), receipt)
	if len(out) > assistantPortfolioMaxReceipts {
		out = out[len(out)-assistantPortfolioMaxReceipts:]
	}
	return out
}
