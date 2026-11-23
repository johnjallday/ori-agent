package folderagenthttp

import (
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"net/http"
)

func (h *Handler) organizationUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if connectionshttp.NewOriginGuard().Check(r) != nil {
		fail(w, http.StatusForbidden, "Local access required")
		return "", false
	}
	return h.user(w, r)
}
func (h *Handler) OrganizationSource(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.organizationUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Review string `json:"review"`
		Source string `json:"source"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := h.service.InspectOrganizationSource(r.Context(), owner, r.PathValue("workspaceID"), req.Review, req.Source); err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"opened": true})
}
func (h *Handler) OrganizationView(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.organizationUser(w, r)
	if !ok {
		return
	}
	var req folderagent.ViewChange
	if !decode(w, r, &req) {
		return
	}
	v, err := h.service.ChangeOrganizationView(owner, r.PathValue("workspaceID"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"review": v})
}
func (h *Handler) OrganizationReviews(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if connectionshttp.NewOriginGuard().Check(r) != nil {
		fail(w, http.StatusForbidden, "Local access required")
		return
	}
	owner, ok := h.user(w, r)
	if !ok {
		return
	}
	id := r.PathValue("workspaceID")
	if r.Method == http.MethodGet {
		rows, err := h.service.OrganizationResults(owner, id)
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{"reviews": rows})
		return
	}
	var req struct {
		Approval  string                      `json:"approval"`
		Selection folderagent.ReviewSelection `json:"selection"`
	}
	if !decode(w, r, &req) {
		return
	}
	result, err := h.service.ApproveOrganization(r.Context(), owner, id, req.Approval, req.Selection)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"review": result})
}
