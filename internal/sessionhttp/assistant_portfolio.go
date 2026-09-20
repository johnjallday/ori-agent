package sessionhttp

import (
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type assistantPortfolioReviewRequest struct {
	LinkID     string                             `json:"link_id"`
	IfRevision int64                              `json:"if_revision"`
	Fields     workspace.AssistantPortfolioUpdate `json:"fields"`
}

type assistantPortfolioCommitRequest struct {
	ReviewToken    string                             `json:"review_token"`
	IdempotencyKey string                             `json:"idempotency_key"`
	Fields         workspace.AssistantPortfolioUpdate `json:"fields"`
}

type assistantHandoffReviewRequest struct {
	LinkID      string                `json:"link_id"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	State       workspace.TicketState `json:"state"`
}

type assistantHandoffCommitRequest struct {
	ReviewToken    string                `json:"review_token"`
	IdempotencyKey string                `json:"idempotency_key"`
	Title          string                `json:"title"`
	Description    string                `json:"description,omitempty"`
	State          workspace.TicketState `json:"state"`
}

func (h *Handler) GetAssistantPortfolio(w http.ResponseWriter, r *http.Request) {
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant Program Home not found")
		return
	}
	projects, err := workspace.NewAssistantPortfolioService(h.workspaceTaskStore).List(station.ID)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Assistant portfolio is unavailable")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"station_id": station.ID, "projects": projects})
}

func (h *Handler) ReviewAssistantPortfolio(w http.ResponseWriter, r *http.Request) {
	var request assistantPortfolioReviewRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant Program Home not found")
		return
	}
	if station, ok := h.requireAssistantWritable(w, station); !ok {
		return
	} else {
		review, reviewErr := workspace.NewAssistantPortfolioService(h.workspaceTaskStore).Review(station.ID, strings.TrimSpace(request.LinkID), request.IfRevision, request.Fields)
		if reviewErr != nil {
			respondAssistantPortfolioError(w, reviewErr)
			return
		}
		_ = orihttp.RespondSuccess(w, review)
	}
}

func (h *Handler) CommitAssistantPortfolio(w http.ResponseWriter, r *http.Request) {
	var request assistantPortfolioCommitRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant Program Home not found")
		return
	}
	if station, ok := h.requireAssistantWritable(w, station); !ok {
		return
	} else {
		receipt, commitErr := workspace.NewAssistantPortfolioService(h.workspaceTaskStore).Commit(station.ID, request.ReviewToken, request.IdempotencyKey, request.Fields)
		if commitErr != nil {
			respondAssistantPortfolioError(w, commitErr)
			return
		}
		_ = orihttp.RespondSuccess(w, receipt)
	}
}

func (h *Handler) ReviewAssistantHandoff(w http.ResponseWriter, r *http.Request) {
	var request assistantHandoffReviewRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant Program Home not found")
		return
	}
	if !h.requireAssistantHandoffWritable(w, station, strings.TrimSpace(request.LinkID)) {
		return
	}
	review, err := workspace.NewAssistantPortfolioService(h.workspaceTaskStore).ReviewHandoff(station.ID, strings.TrimSpace(request.LinkID), workspace.AssistantPortfolioHandoffInput{
		Title: request.Title, Description: request.Description, State: request.State,
	})
	if err != nil {
		respondAssistantPortfolioError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantHandoff(w http.ResponseWriter, r *http.Request) {
	var request assistantHandoffCommitRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant Program Home not found")
		return
	}
	linkID := ""
	if state := station.GetAssistantProgramState(); state != nil {
		for _, review := range state.Portfolio.HandoffReviewReceipts {
			if review.Token == request.ReviewToken {
				linkID = review.LinkID
				break
			}
		}
	}
	if !h.requireAssistantHandoffWritable(w, station, linkID) {
		return
	}
	receipt, err := workspace.NewAssistantPortfolioService(h.workspaceTaskStore).CommitHandoff(station.ID, request.ReviewToken, request.IdempotencyKey, workspace.AssistantPortfolioHandoffInput{
		Title: request.Title, Description: request.Description, State: request.State,
	})
	if err != nil {
		respondAssistantPortfolioError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, receipt)
}

func (h *Handler) requireAssistantHandoffWritable(w http.ResponseWriter, station *workspace.Workspace, linkID string) bool {
	var ok bool
	if station, ok = h.requireAssistantWritable(w, station); !ok {
		return false
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return false
	}
	homeOwner := state.HomeProvider
	if homeOwner == nil && state.GroupTemplate != nil {
		homeOwner = state.GroupTemplate.ProgramHomeOwner
	}
	if homeOwner == nil || strings.TrimSpace(linkID) == "" {
		return true
	}
	if h.installedPluginLister == nil {
		_ = orihttp.RespondConflict(w, "The project provider cannot be verified; existing data remains available")
		return false
	}
	projects, err := workspace.NewAssistantProgramStore(h.workspaceTaskStore).LinkedProjects(station.ID)
	if err != nil {
		_ = orihttp.RespondConflict(w, "The selected project link changed; review it again")
		return false
	}
	var projectOwner *workspace.AssistantProjectProviderOwner
	for _, project := range projects {
		link := project.GetAssistantProjectLink()
		if link != nil && link.ID == linkID {
			projectOwner = link.ProjectProvider
			break
		}
	}
	installed, err := h.installedPluginLister.List()
	if err != nil || !plugin.IndependentProviderEvidenceAvailable(installed, homeOwner, projectOwner) {
		_ = orihttp.RespondConflict(w, "The project provider is unavailable; Home portfolio data remains available")
		return false
	}
	return true
}

func respondAssistantPortfolioError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspace.ErrAssistantPortfolioInvalid):
		_ = orihttp.RespondBadRequest(w, "Invalid Assistant Program request")
	case errors.Is(err, workspace.ErrAssistantPortfolioLinkNotFound):
		_ = orihttp.RespondConflict(w, "The selected project link changed; review it again")
	case errors.Is(err, workspace.ErrAssistantPortfolioConflict),
		errors.Is(err, workspace.ErrAssistantPortfolioReviewExpired),
		errors.Is(err, workspace.ErrAssistantPortfolioIdempotency):
		_ = orihttp.RespondConflict(w, "Assistant Program state changed; review it again")
	default:
		_ = orihttp.RespondInternalError(w, "Assistant Program action could not be completed")
	}
}
