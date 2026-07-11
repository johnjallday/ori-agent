package sessionhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

// handleReaperReadiness serves GET /api/workspaces/{id}/reaper-setup: the one
// normalized REAPER readiness result reused by the workspace UI, repair, and
// setup auto-start decisions. Each request recomputes from live plugin, binding,
// task, provider, and permission state — it never returns a cached ready result.
func (h *Handler) handleReaperReadiness(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	if h.reaperResolver == nil {
		// Plugins unavailable: report an unidentified, file-only-safe result so the
		// UI simply renders no REAPER surface rather than erroring.
		orihttp.WriteJSON(w, reapersetup.Readiness{LiveVerification: "not_checked", ProjectMode: "file_only"})
		return
	}
	readiness, err := h.reaperResolver.Resolve(workspaceID)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, readiness)
}

// GetReaperCreatePreview serves GET /api/reaper-setup/preview: the pre-create
// REAPER Setup card state for the Reaper Song template, computed from the same
// plugin store the readiness resolver reads. There is no workspace yet, so it
// reports plugin install/enable/would-attach only; agent and native-access
// decisions happen in-workspace after creation.
func (h *Handler) GetReaperCreatePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	preview, err := reapersetup.PreviewCreate(h.reaperPluginLister)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, preview)
}
