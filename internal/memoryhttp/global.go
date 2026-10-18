package memoryhttp

import (
	"context"
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// profileGlobalMemoryHref is where the user reviews global memory.
const profileGlobalMemoryHref = "/profile#personalHQKnowledge"

// globalMemorySetupHref is where a user without an assistant or Personal HQ
// sets one up (the hire and HQ flows both start on Home).
const globalMemorySetupHref = "/"

// GlobalMemory is the host seam to the user's global memory, the Personal HQ
// knowledge lifecycle. Without it every global-memory control is unavailable.
type GlobalMemory interface {
	// Workspace returns the Personal HQ workspace ID, or
	// personalassistant.ErrGlobalMemoryUnavailable when there is none.
	Workspace(ctx context.Context) (string, error)
	Promote(ctx context.Context, req personalassistant.WorkspacePromoteRequest) (personalassistant.WorkspacePromoteResult, personalassistant.KnowledgeItem, error)
}

// SetGlobalMemory wires the global-memory seam.
func (h *Handler) SetGlobalMemory(global GlobalMemory) {
	if h != nil {
		h.global = global
	}
}

type globalMemoryDTO struct {
	Available bool `json:"available"`
	// IsPersonalHQ means this workspace's own memory is global memory.
	IsPersonalHQ bool   `json:"is_personal_hq,omitempty"`
	Message      string `json:"message,omitempty"`
	SetupHref    string `json:"setup_href,omitempty"`
	ReviewHref   string `json:"review_href"`
}

func (h *Handler) globalMemoryStatus(ctx context.Context, workspaceID string) globalMemoryDTO {
	status := globalMemoryDTO{ReviewHref: profileGlobalMemoryHref}
	hq := ""
	err := personalassistant.ErrGlobalMemoryUnavailable
	if h.global != nil {
		hq, err = h.global.Workspace(ctx)
	}
	if err != nil {
		status.Message = personalassistant.GlobalMemoryUnavailableMessage
		status.SetupHref = globalMemorySetupHref
		return status
	}
	status.Available = true
	status.IsPersonalHQ = hq == workspaceID
	return status
}

type promoteRequest struct {
	Text     string `json:"text"`
	Category string `json:"category"`
}

type promoteResponse struct {
	Status  string `json:"status"`
	Created bool   `json:"created"`
	ItemID  string `json:"item_id,omitempty"`
	Href    string `json:"href"`
	Message string `json:"message"`
}

// PromoteEntry handles POST /api/workspaces/{workspaceID}/memory/entries/{index}/promote.
// It copies one ordinary entry into global memory as an approved fact; the
// workspace entry stays where it is.
func (h *Handler) PromoteEntry(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	index, ok := h.parseIndex(w, r)
	if !ok {
		return
	}
	var req promoteRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if h.global == nil {
		writeGlobalMemoryUnavailable(w)
		return
	}
	raw, err := h.memory.ReadRaw(workspaceID)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to read memory: "+err.Error())
		return
	}
	entries := workspace.ParseMemoryDocument(raw).Entries()
	if index >= len(entries) {
		_ = orihttp.RespondNotFound(w, workspace.ErrMemoryIndexOutOfRange.Error())
		return
	}
	entry := entries[index]
	if workspace.IsHQManagedProvenance(entry.Provenance) || workspace.IsGlobalCopyProvenance(entry.Provenance) {
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("memory_entry_not_promotable",
			"This entry already comes from global memory, so there is nothing to promote."))
		return
	}
	text := req.Text
	if text == "" {
		text = entry.Text
	}
	category := req.Category
	if category == "" {
		category = defaultPromoteCategory(entry.Type)
	}
	result, item, err := h.global.Promote(r.Context(), personalassistant.WorkspacePromoteRequest{
		WorkspaceID: workspaceID, Category: category, Text: text, Original: entry.Text,
	})
	switch {
	case errors.Is(err, personalassistant.ErrGlobalMemoryUnavailable):
		writeGlobalMemoryUnavailable(w)
		return
	case errors.Is(err, personalassistant.ErrValidation):
		_ = orihttp.RespondBadRequest(w, "Use one safe line of at most 500 characters and one of the listed categories. Keep secrets in the Vault.")
		return
	case errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalassistant.ErrKnowledgeLimit):
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("global_memory_changed",
			"Global memory changed or is full. Review it on your Profile page, then try again."))
		return
	case err != nil:
		logger.Debug("Promote to global memory failed", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Global memory is temporarily unavailable. Try again."))
		return
	}
	resp := promoteResponse{Status: string(result), ItemID: item.ID, Href: profileGlobalMemoryHref}
	switch result {
	case personalassistant.WorkspacePromoted:
		resp.Created = true
		resp.Message = "Promoted to global memory. Your assistant now knows it everywhere."
	case personalassistant.WorkspacePromoteAlreadyPending:
		resp.Message = "This fact is already waiting for your review on your Profile page."
	default:
		resp.Message = "Already in global memory."
	}
	if err := orihttp.RespondJSON(w, http.StatusOK, resp); err != nil {
		logger.Debug("Failed to write promote response", logger.Fields{"workspace_id": workspaceID, "error": err})
	}
}

// defaultPromoteCategory: user feedback is about how the user works; every
// other entry type most often describes their projects.
func defaultPromoteCategory(entryType workspace.MemoryEntryType) string {
	if entryType == workspace.MemoryTypeFeedback {
		return "how_you_work"
	}
	return "projects"
}

func writeGlobalMemoryUnavailable(w http.ResponseWriter) {
	_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("global_memory_unavailable",
		personalassistant.GlobalMemoryUnavailableMessage))
}
