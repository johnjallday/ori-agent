package personalhqhttp

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/followup"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The bridge from a follow-up to a ticket (PRD 760 FR-25, FR-26). It is a link,
// never ownership: closing either one leaves the other open.

// FollowUpLinker records a follow-up's ticket. *followup.Service implements it.
type FollowUpLinker interface {
	LinkTask(ctx context.Context, userID, id string, ref followup.TaskRef) (*followup.FollowUp, error)
}

// SetFollowUpTickets wires Make a ticket. tickets is a factory because workspace
// storage is wired after this handler during startup.
func (h *Handler) SetFollowUpTickets(linker FollowUpLinker, tickets func() *workspace.TicketService) {
	h.followUpLinker = linker
	h.followUpTickets = tickets
}

func (h *Handler) ticketService() *workspace.TicketService {
	if h == nil || h.followUpTickets == nil {
		return nil
	}
	return h.followUpTickets()
}

// relatedTicket is a follow-up's ticket as it stands now.
type relatedTicket struct {
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspace_id"`
	DisplayNumber string `json:"display_number"`
	State         string `json:"state"`
	StateLabel    string `json:"state_label"`
	Href          string `json:"href,omitempty"`
}

// followUpView is a follow-up with its ticket's current state beside it.
type followUpView struct {
	*followup.FollowUp
	RelatedTicket *relatedTicket `json:"related_ticket,omitempty"`
	// Href opens this follow-up where it is managed.
	Href string `json:"href,omitempty"`
}

func relatedTicketOf(ticket *workspace.Ticket) *relatedTicket {
	if ticket == nil {
		return nil
	}
	view := &relatedTicket{
		ID:            ticket.ID,
		WorkspaceID:   ticket.OwningWorkspaceID,
		DisplayNumber: ticket.DisplayNumber,
		State:         string(ticket.State),
		StateLabel:    ticket.State.Label(),
	}
	if ticket.OwningWorkspaceSlug != "" {
		view.Href = "/workspaces/" + url.PathEscape(ticket.OwningWorkspaceSlug) + "?ticket=" + url.QueryEscape(ticket.ID)
	}
	return view
}

// followUpViews adds each linked ticket's current state. A ticket that can no
// longer be read is left out: the follow-up still stands on its own.
func (h *Handler) followUpViews(items []*followup.FollowUp) []followUpView {
	service := h.ticketService()
	views := make([]followUpView, 0, len(items))
	for _, item := range items {
		view := followUpView{FollowUp: item}
		if service != nil && item != nil {
			if item.RelatedTask != nil {
				if ticket, err := service.Get(item.RelatedTask.WorkspaceID, item.RelatedTask.TaskID); err == nil {
					view.RelatedTicket = relatedTicketOf(ticket)
				}
			}
			if slug := service.WorkspaceSlug(item.WorkspaceID); slug != "" {
				view.Href = "/workspaces/" + url.PathEscape(slug) + "?follow_up=" + url.QueryEscape(item.ID)
			}
		}
		views = append(views, view)
	}
	return views
}

// GetFollowUp handles GET /api/personal-hq/followups/item?id=: one follow-up, for
// a ticket's detail to show what it came from.
func (h *Handler) GetFollowUp(w http.ResponseWriter, r *http.Request) {
	if !h.followUpsReady(w, r, http.MethodGet) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	item, err := h.followups.Get(r.Context(), userID, id)
	if err != nil {
		respondFollowUpError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"followup": h.followUpViews([]*followup.FollowUp{item})[0]})
}

// MakeTicketFromFollowUp handles POST /api/personal-hq/followups/ticket {id,
// workspace_id}. It creates a Backlog ticket with source followup and links it.
// Repeating it returns the existing ticket.
func (h *Handler) MakeTicketFromFollowUp(w http.ResponseWriter, r *http.Request) {
	if !h.followUpsReady(w, r, http.MethodPost) {
		return
	}
	service := h.ticketService()
	if service == nil || h.followUpLinker == nil {
		orihttp.ServiceUnavailable(w, "tickets are unavailable")
		return
	}
	var req struct {
		ID          string `json:"id"`
		WorkspaceID string `json:"workspace_id"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	item, err := h.followups.Get(r.Context(), userID, id)
	if err != nil {
		respondFollowUpError(w, err)
		return
	}
	if item.RelatedTask != nil {
		if existing, err := service.Get(item.RelatedTask.WorkspaceID, item.RelatedTask.TaskID); err == nil {
			respondFollowUpTicket(w, http.StatusOK, item, existing, false)
			return
		}
	}

	target := strings.TrimSpace(req.WorkspaceID)
	if target == "" && h.ownsWork(r.Context(), userID, item.WorkspaceID) {
		target = strings.TrimSpace(item.WorkspaceID)
	}
	if target == "" {
		// HQ holds commitments, not workspace work, so the person picks.
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
			orihttp.NewAPIError(orihttp.ErrCodeValidation, "Choose a workspace for this ticket").
				WithDetails(map[string]string{"field": "workspace_id"}))
		return
	}
	if !service.AcceptsTicketsFrom(target, userID) {
		orihttp.NotFound(w, "Workspace not found")
		return
	}

	ticket, created, err := service.CreateWithKey(workspace.TicketCreateInput{
		WorkspaceID: target,
		State:       workspace.TicketStateBacklog,
		Title:       item.Title,
		Description: item.Detail,
		DueDate:     item.DueAt,
		Source:      workspace.TicketSourceFollowUp,
		SourceID:    item.ID,
		Actor:       workspace.TicketActorUser,
	})
	if err != nil {
		if validation, isValidation := workspace.IsTicketValidationError(err); isValidation {
			orihttp.BadRequest(w, validation.Message)
			return
		}
		// The workspace was checked above, so this is the store, not a missing
		// workspace; say so rather than sending the person looking for one.
		orihttp.InternalError(w, "The ticket could not be saved. Try again.")
		return
	}
	linked, err := h.followUpLinker.LinkTask(r.Context(), userID, item.ID, followup.TaskRef{
		WorkspaceID: ticket.OwningWorkspaceID,
		TaskID:      ticket.ID,
	})
	if err != nil {
		// The key makes a retry find this ticket and link it.
		respondFollowUpError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	respondFollowUpTicket(w, status, linked, ticket, created)
}

// ownsWork says whether a follow-up's own workspace can take its ticket: it is
// not Personal HQ, which holds commitments rather than workspace work. When HQ
// cannot be told apart (no HQ service, or its status unreadable), the person
// chooses rather than the ticket landing in HQ by default.
func (h *Handler) ownsWork(ctx context.Context, userID, workspaceID string) bool {
	if h == nil || strings.TrimSpace(workspaceID) == "" {
		return false
	}
	hq, known := h.personalHQWorkspace(ctx, userID)
	return known && hq != workspaceID
}

// personalHQWorkspace is the user's Personal HQ workspace ID, "" when they have
// none; known is false when that cannot be read.
func (h *Handler) personalHQWorkspace(ctx context.Context, userID string) (string, bool) {
	if h.hqWorkspace != nil {
		return h.hqWorkspace(ctx, userID)
	}
	if h.service == nil {
		return "", false
	}
	status, err := h.service.Status(ctx, userID)
	if err != nil || status == nil {
		return "", false
	}
	if !status.HasDesignation() {
		return "", true
	}
	return status.WorkspaceID, true
}

func respondFollowUpTicket(w http.ResponseWriter, status int, item *followup.FollowUp, ticket *workspace.Ticket, created bool) {
	_ = orihttp.RespondJSON(w, status, map[string]any{
		"followup": followUpView{FollowUp: item, RelatedTicket: relatedTicketOf(ticket)},
		"ticket":   relatedTicketOf(ticket),
		"created":  created,
	})
}
