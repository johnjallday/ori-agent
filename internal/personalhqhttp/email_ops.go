package personalhqhttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/followup"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// EmailOpsStatus is the HQ email station portal's data source: whether the user
// has an Email Ops workspace, and if so its id, name, and open follow-up count
// (the portal badge, per the Mail spin-off open-Q2).
type EmailOpsStatus struct {
	Exists            bool   `json:"exists"`
	WorkspaceID       string `json:"workspace_id,omitempty"`
	WorkspaceName     string `json:"workspace_name,omitempty"`
	OpenFollowupCount int    `json:"open_followup_count"`
}

// EmailOpsStatusHandler handles GET /api/personal-hq/email-ops. It is
// user-scoped (not HQ-gated): it reports the requesting user's Email Ops
// workspace regardless of which workspace the HQ page is showing, so the email
// station can render its portal state.
func (h *Handler) EmailOpsStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}

	workspaceID := h.resolveEmailOpsWorkspaceID(userID)
	if workspaceID == "" {
		orihttp.Success(w, map[string]any{"status": EmailOpsStatus{Exists: false}})
		return
	}

	status := EmailOpsStatus{Exists: true, WorkspaceID: workspaceID}

	// Workspace name for the portal label, resolved through the same source the
	// resolver used. Best-effort: a missing name never fails the portal.
	if h.watchtowerSources != nil {
		if src := h.watchtowerSources().Workspaces; src != nil {
			if ws, err := src.Get(workspaceID); err == nil && ws != nil {
				status.WorkspaceName = ws.Name
			}
		}
	}

	// Open follow-up count owned by the Email Ops workspace — the badge metric.
	if h.followups != nil {
		items, err := h.followups.List(r.Context(), followup.Filter{
			UserID: userID, WorkspaceID: workspaceID, OpenOnly: true,
		})
		if err == nil {
			status.OpenFollowupCount = len(items)
		}
	}

	orihttp.Success(w, map[string]any{"status": status})
}
