package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AssistantPortfolioHandoffInput struct {
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	State       TicketState `json:"state"`
}

type AssistantPortfolioHandoffProjection struct {
	LinkID             string      `json:"link_id"`
	LinkRevision       int64       `json:"link_revision"`
	ProjectWorkspaceID string      `json:"project_workspace_id"`
	ProjectName        string      `json:"project_name"`
	Title              string      `json:"title"`
	Description        string      `json:"description,omitempty"`
	State              TicketState `json:"state"`
	Assignment         string      `json:"assignment"`
	SuggestedAssignee  string      `json:"suggested_assignee,omitempty"`
	AuthorityBoundary  string      `json:"authority_boundary"`
}

type AssistantPortfolioHandoffReview struct {
	Token     string                              `json:"token"`
	ExpiresAt time.Time                           `json:"expires_at"`
	Handoff   AssistantPortfolioHandoffProjection `json:"handoff"`
}

type AssistantPortfolioHandoffReceipt struct {
	LinkID             string    `json:"link_id"`
	ProjectWorkspaceID string    `json:"project_workspace_id"`
	TicketID           string    `json:"ticket_id"`
	TicketNumber       int64     `json:"ticket_number,omitempty"`
	RecordedAt         time.Time `json:"recorded_at"`
	Replayed           bool      `json:"replayed,omitempty"`
}

func (service *AssistantPortfolioService) ReviewHandoff(stationID, linkID string, input AssistantPortfolioHandoffInput) (*AssistantPortfolioHandoffReview, error) {
	assistantPortfolioMu.Lock()
	defer assistantPortfolioMu.Unlock()
	normalized, err := normalizeAssistantPortfolioHandoff(input)
	if err != nil {
		return nil, err
	}
	station, state, err := service.station(stationID)
	if err != nil {
		return nil, err
	}
	project, link, err := service.linkedProject(station, state, linkID)
	if err != nil {
		return nil, err
	}
	digest := assistantPortfolioHandoffDigest(linkID, normalized)
	now := service.now().UTC()
	receipt := AssistantPortfolioHandoffReviewReceipt{
		Token: uuid.NewString(), LinkID: linkID, InputDigest: digest,
		LinkRevision: link.StateRevision, ExpiresAt: now.Add(assistantPortfolioReviewTTL),
	}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.Key.Normalize() != state.Key.Normalize() {
			return ErrAssistantPortfolioConflict
		}
		currentState.Portfolio.HandoffReviewReceipts = appendBoundedHandoffReview(currentState.Portfolio.HandoffReviewReceipts, receipt, now)
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		return nil, ErrAssistantPortfolioConflict
	}
	return &AssistantPortfolioHandoffReview{
		Token: receipt.Token, ExpiresAt: receipt.ExpiresAt,
		Handoff: service.handoffProjection(project, link, state, normalized),
	}, nil
}

func (service *AssistantPortfolioService) CommitHandoff(stationID, token, idempotencyKey string, input AssistantPortfolioHandoffInput) (*AssistantPortfolioHandoffReceipt, error) {
	assistantPortfolioMu.Lock()
	defer assistantPortfolioMu.Unlock()
	token = strings.TrimSpace(token)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if token == "" || idempotencyKey == "" || len(token) > 160 || len(idempotencyKey) > 160 {
		return nil, ErrAssistantPortfolioInvalid
	}
	normalized, err := normalizeAssistantPortfolioHandoff(input)
	if err != nil {
		return nil, err
	}
	station, state, err := service.station(stationID)
	if err != nil {
		return nil, err
	}
	var review *AssistantPortfolioHandoffReviewReceipt
	for index := range state.Portfolio.HandoffReviewReceipts {
		candidate := &state.Portfolio.HandoffReviewReceipts[index]
		if candidate.Token == token {
			review = candidate
			break
		}
	}
	if review == nil || review.InputDigest != assistantPortfolioHandoffDigest(review.LinkID, normalized) {
		return nil, ErrAssistantPortfolioReviewExpired
	}
	for _, prior := range state.Portfolio.HandoffOperationReceipts {
		if prior.IdempotencyKey != idempotencyKey {
			continue
		}
		if prior.InputDigest != review.InputDigest || prior.LinkID != review.LinkID {
			return nil, ErrAssistantPortfolioIdempotency
		}
		return &AssistantPortfolioHandoffReceipt{
			LinkID: prior.LinkID, ProjectWorkspaceID: prior.ProjectWorkspaceID,
			TicketID: prior.TicketID, TicketNumber: prior.TicketNumber,
			RecordedAt: prior.RecordedAt, Replayed: true,
		}, nil
	}
	now := service.now().UTC()
	if review.ConsumedAt != nil || !review.ExpiresAt.After(now) {
		return nil, ErrAssistantPortfolioReviewExpired
	}
	project, link, err := service.linkedProject(station, state, review.LinkID)
	if err != nil || link.StateRevision != review.LinkRevision {
		return nil, ErrAssistantPortfolioLinkNotFound
	}
	// The child owns the Ticket. The Home contributes only a stable source key
	// and bounded content after confirmation; it receives no child agent, tool,
	// filesystem, runtime, prompt, memory, or grant authority.
	ticket, _, err := NewTicketService(service.store).CreateIdempotent(TicketCreateInput{
		WorkspaceID: project.ID, State: normalized.State,
		Title: normalized.Title, Description: normalized.Description,
		Source:   TicketSourceAssistant,
		SourceID: assistantPortfolioHandoffSourceID(station.ID, review.LinkID, idempotencyKey),
		Actor:    "Assistant Program handoff", ActorID: state.Key.OwnerUserID,
	})
	if err != nil {
		return nil, ErrAssistantPortfolioConflict
	}
	operation := AssistantPortfolioHandoffOperationReceipt{
		IdempotencyKey: idempotencyKey, InputDigest: review.InputDigest, LinkID: review.LinkID,
		ProjectWorkspaceID: project.ID, TicketID: ticket.ID, TicketNumber: ticket.Number, RecordedAt: now,
	}
	liveProject, getErr := service.store.Get(project.ID)
	if getErr != nil {
		return nil, ErrAssistantPortfolioLinkNotFound
	}
	liveLink := liveProject.GetAssistantProjectLink()
	if liveLink == nil || liveLink.ID != review.LinkID || liveLink.StateRevision != review.LinkRevision || liveLink.StationWorkspaceID != station.ID {
		return nil, ErrAssistantPortfolioLinkNotFound
	}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.Key.Normalize() != state.Key.Normalize() {
			return ErrAssistantPortfolioConflict
		}
		var persistedReview *AssistantPortfolioHandoffReviewReceipt
		for index := range currentState.Portfolio.HandoffReviewReceipts {
			if currentState.Portfolio.HandoffReviewReceipts[index].Token == token {
				persistedReview = &currentState.Portfolio.HandoffReviewReceipts[index]
				break
			}
		}
		if persistedReview == nil || persistedReview.ConsumedAt != nil || persistedReview.InputDigest != review.InputDigest || !persistedReview.ExpiresAt.After(now) {
			return ErrAssistantPortfolioReviewExpired
		}
		persistedReview.ConsumedAt = &now
		currentState.Portfolio.HandoffOperationReceipts = appendBoundedHandoffOperation(currentState.Portfolio.HandoffOperationReceipts, operation)
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		return nil, err
	}
	return &AssistantPortfolioHandoffReceipt{
		LinkID: operation.LinkID, ProjectWorkspaceID: operation.ProjectWorkspaceID,
		TicketID: operation.TicketID, TicketNumber: operation.TicketNumber, RecordedAt: operation.RecordedAt,
	}, nil
}

func (service *AssistantPortfolioService) handoffProjection(project *Workspace, link *AssistantProjectLink, state *AssistantProgramState, input AssistantPortfolioHandoffInput) AssistantPortfolioHandoffProjection {
	projection := AssistantPortfolioHandoffProjection{
		LinkID: link.ID, LinkRevision: link.StateRevision,
		ProjectWorkspaceID: project.ID, ProjectName: project.Name,
		Title: input.Title, Description: input.Description, State: input.State,
		Assignment:        "Unassigned in the child for explicit project-team triage",
		AuthorityBoundary: "Creates one child-owned Ticket only; the Home receives no child tools or project access.",
	}
	var primaryRoleID string
	for _, role := range state.Declaration.Roles {
		if role.Scope == AssistantRoleScopeProject && role.Primary {
			primaryRoleID = role.ID
			break
		}
	}
	for _, binding := range link.ProjectBindings.Bindings {
		if binding.RoleID == primaryRoleID {
			projection.SuggestedAssignee = binding.AgentName
			break
		}
	}
	return projection
}

func normalizeAssistantPortfolioHandoff(input AssistantPortfolioHandoffInput) (AssistantPortfolioHandoffInput, error) {
	var err error
	input.Title, err = NormalizeTicketTitle(input.Title)
	if err != nil {
		return AssistantPortfolioHandoffInput{}, ErrAssistantPortfolioInvalid
	}
	input.Description, err = NormalizeTicketDescription(input.Description)
	if err != nil {
		return AssistantPortfolioHandoffInput{}, ErrAssistantPortfolioInvalid
	}
	if input.State != TicketStateBacklog && input.State != TicketStateReady {
		return AssistantPortfolioHandoffInput{}, ErrAssistantPortfolioInvalid
	}
	return input, nil
}

func assistantPortfolioHandoffDigest(linkID string, input AssistantPortfolioHandoffInput) string {
	encoded, _ := json.Marshal(struct {
		LinkID string
		Input  AssistantPortfolioHandoffInput
	}{LinkID: linkID, Input: input})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func assistantPortfolioHandoffSourceID(stationID, linkID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(stationID + "\x00" + linkID + "\x00" + idempotencyKey))
	return "assistant-handoff-" + hex.EncodeToString(digest[:16])
}

func appendBoundedHandoffReview(receipts []AssistantPortfolioHandoffReviewReceipt, receipt AssistantPortfolioHandoffReviewReceipt, now time.Time) []AssistantPortfolioHandoffReviewReceipt {
	out := make([]AssistantPortfolioHandoffReviewReceipt, 0, assistantPortfolioMaxReceipts)
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

func appendBoundedHandoffOperation(receipts []AssistantPortfolioHandoffOperationReceipt, receipt AssistantPortfolioHandoffOperationReceipt) []AssistantPortfolioHandoffOperationReceipt {
	out := append(append([]AssistantPortfolioHandoffOperationReceipt(nil), receipts...), receipt)
	if len(out) > assistantPortfolioMaxReceipts {
		out = out[len(out)-assistantPortfolioMaxReceipts:]
	}
	return out
}
