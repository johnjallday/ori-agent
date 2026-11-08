package personalassistanthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type folderHomeStaffingReviewer interface {
	ReviewHomeStaffing(context.Context, string, string, personalassistant.FolderHomeStaffingInput) (personalassistant.FolderOfferView, error)
}

// Reuses the existing review/adjust route. This action cannot accept paths,
// prompts, source proofs, ownership or setup-execution parameters.
func (h *Handler) changeFolderHomeStaffing(w http.ResponseWriter, r *http.Request, userID, offerID string, request folderSoftwareReviewRequest, adjust bool) {
	var input personalassistant.FolderHomeStaffingInput
	decoder := json.NewDecoder(bytes.NewReader(request.HomeStaffing))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || input.RequestID != "" || request.WorkspaceName != nil || request.AssistantName != nil {
		orihttp.BadRequest(w, "Home staffing accepts only an explicit public role choice or recommendation action")
		return
	}
	if (!adjust && input.Action != "recommend_staffing") || (adjust && input.Action != "choose_staffing" && input.Action != "clear_staffing") {
		orihttp.BadRequest(w, "Use review to recommend, or adjust to choose or clear a Home role")
		return
	}
	owner, ok := h.folderDigest.(folderHomeStaffingReviewer)
	if !ok {
		orihttp.ServiceUnavailable(w, "Home staffing review is unavailable")
		return
	}
	input.RequestID = request.RequestID
	offer, err := owner.ReviewHomeStaffing(r.Context(), userID, offerID, input)
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"offer": offer})
}
