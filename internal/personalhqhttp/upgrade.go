package personalhqhttp

import (
	"context"
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalhq"
)

// UpgradePreview handles GET /api/personal-hq/upgrade/preview. It returns the
// pure upgrade plan for the user's designated HQ (version diff, additions,
// conflicts, preserved customizations, blockers, retryable prior failure) so the
// UI can show exactly what an apply would change before confirmation (task 2.10,
// 2.11). It never mutates anything.
func (h *Handler) UpgradePreview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.upgrade == nil {
		orihttp.ServiceUnavailable(w, "personal hq upgrade is unavailable")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	workspaceID, ok := h.designatedHQWorkspaceID(r.Context(), userID, w)
	if !ok {
		return
	}
	plan, err := h.upgrade.PlanFor(r.Context(), userID, workspaceID)
	if err != nil {
		respondUpgradeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"plan": plan})
}

// UpgradeApply handles POST /api/personal-hq/upgrade/apply. It applies the
// planned upgrade to the user's designated HQ: idempotent and retryable, it adds
// missing specialist roles and stamps the provisioning version. A blocked plan
// or a stale designation is a client-actionable conflict, not a 500.
func (h *Handler) UpgradeApply(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.upgrade == nil {
		orihttp.ServiceUnavailable(w, "personal hq upgrade is unavailable")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	workspaceID, ok := h.designatedHQWorkspaceID(r.Context(), userID, w)
	if !ok {
		return
	}
	result, err := h.upgrade.ApplyUpgrade(r.Context(), userID, workspaceID)
	if err != nil {
		// A partial failure still carries a result describing what was added and
		// that the outcome was recorded as retryable; surface it with the error.
		if result != nil {
			orihttp.Conflict(w, "Upgrade did not fully complete and can be retried: "+err.Error())
			return
		}
		respondUpgradeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"result": result})
}

// resolveUser resolves the current user, writing a 500 and returning false on
// failure so callers can early-return.
func (h *Handler) resolveUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return "", false
	}
	return userID, true
}

// designatedHQWorkspaceID resolves the user's current, valid designated HQ.
// When there is no valid HQ it writes a client error and returns false, so the
// upgrade endpoints never operate on a stale/absent designation.
func (h *Handler) designatedHQWorkspaceID(ctx context.Context, userID string, w http.ResponseWriter) (string, bool) {
	status, err := h.service.Status(ctx, userID)
	if err != nil {
		orihttp.InternalError(w, "Failed to load personal hq status: "+err.Error())
		return "", false
	}
	if status == nil || !status.Valid {
		orihttp.Conflict(w, "No valid Personal HQ is designated to upgrade")
		return "", false
	}
	return status.WorkspaceID, true
}

func respondUpgradeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalhq.ErrNotDesignatedHQ):
		orihttp.Conflict(w, "That workspace is not your designated Personal HQ")
	case errors.Is(err, personalhq.ErrUpgradeBlocked):
		orihttp.Conflict(w, err.Error())
	case errors.Is(err, personalhq.ErrWorkspaceIDRequired):
		orihttp.BadRequest(w, "A designated Personal HQ is required")
	default:
		orihttp.InternalError(w, "Failed to process the Personal HQ upgrade: "+err.Error())
	}
}
