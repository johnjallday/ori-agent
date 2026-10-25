package agenthttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// RefreshFolderRecommendationHandler revalidates an existing held selection.
// It never scans, extends the selection TTL or creates a conversation/offer.
func (h *HomeAssistantAskHandler) RefreshFolderRecommendationHandler(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	var req struct {
		ConversationID string `json:"conversation_id"`
		DraftID        string `json:"draft_id"`
		Revision       string `json:"revision"`
		SelectionID    string `json:"selection_id"`
	}
	if !strictFolderBody(w, r, &req) {
		return
	}
	target, err := h.folderTarget(scope, req.ConversationID, req.DraftID)
	if err != nil || h.folderStore() == nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return
	}
	release, ok := h.folderRequests.enter(target)
	if !ok {
		writeFolderError(w, personalassistant.ErrFolderScanBusy)
		return
	}
	defer release()
	if _, err := h.folderExpected(r.Context(), target, req.Revision); err != nil {
		writeFolderError(w, err)
		return
	}
	observation, err := h.resolveFolderObservation(r.Context(), target, req.SelectionID)
	if err != nil {
		writeFolderError(w, err)
		return
	}
	proposal := h.folderStructure(r.Context(), target, observation)
	orihttp.WriteJSON(w, map[string]any{"observation_id": observation.ID, "structure": proposal.Structure,
		"declined": proposal.Declined, "structure_unavailable": proposal.Unavailable, "blocker": proposal.Blocker})
}
