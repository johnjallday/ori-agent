package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

func (h *Handler) ReviewFolderPrerequisite(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil {
		orihttp.ServiceUnavailable(w, "Plugin review is unavailable")
		return
	}
	service, ok := h.folderDigest.(interface {
		Prerequisite(context.Context, string, string, personalassistant.FolderPrerequisiteInput) (personalassistant.FolderPrerequisiteReview, error)
	})
	if !ok {
		orihttp.ServiceUnavailable(w, "Plugin review is unavailable")
		return
	}
	var input personalassistant.FolderPrerequisiteInput
	if !decodeFolderDigestBody(w, r, &input, "kind", "confirm", "request_id", "reviewed_version", "digest", "run_id", "revision", "review_token", "action") {
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	review, err := service.Prerequisite(r.Context(), userID, strings.TrimSpace(r.PathValue("offerID")), input)
	var failure *setupjourney.Failure
	var upgrade *plugin.HomeUpgradeRequiredError
	switch {
	case errors.As(err, &failure):
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError(string(failure.ReasonCode), failure.Guidance))
	case errors.As(err, &upgrade):
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("home_upgrade_required", "Upgrade the existing Home from its own page before updating this plugin."))
	case errors.Is(err, personalassistant.ErrFolderPlanChanged):
		orihttp.Conflict(w, "The reviewed plugin or destination changed. Check its current state and review again.")
	case errors.Is(err, personalassistant.ErrFolderOutcomeUnavailable):
		orihttp.ServiceUnavailable(w, "The plugin change could not be completed. Check its current state; completed installation work is kept.")
	case err != nil:
		writeFolderDigestError(w, err)
	default:
		orihttp.Success(w, map[string]any{"prerequisite": review})
	}
}
