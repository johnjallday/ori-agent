package folderagenthttp

import (
	"errors"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
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
func (h *Handler) OrganizationConvention(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.organizationUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision string `json:"revision"`
		Text     string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, err := h.service.SaveConvention(owner, r.PathValue("workspaceID"), req.Revision, req.Text)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"convention": c})
}
func (h *Handler) OrganizationFiling(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.organizationUser(w, r)
	if !ok {
		return
	}
	var req folderagent.FilingSelection
	if !decode(w, r, &req) {
		return
	}
	projection, err := h.service.CheckOrganizationFiling(r.Context(), owner, r.PathValue("workspaceID"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"selection": projection})
}

// OrganizationFilingPreview shows what filing the chosen files into a folder
// would do. It moves nothing and writes nothing.
func (h *Handler) OrganizationFilingPreview(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.organizationUser(w, r)
	if !ok {
		return
	}
	var req folderagent.FilingPreviewRequest
	if !decode(w, r, &req) {
		return
	}
	preview, err := h.service.PreviewOrganizationFiling(r.Context(), owner, r.PathValue("workspaceID"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"preview": preview})
}

// OrganizationFile files the previewed selection on a fresh, explicit press. A
// press whose plan changed, or whose key was used for something else, is a 409
// with a code the page can act on; nothing has moved in either case.
func (h *Handler) OrganizationFile(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.organizationUser(w, r)
	if !ok {
		return
	}
	var req folderagent.FilingRequest
	if !decode(w, r, &req) {
		return
	}
	result, err := h.service.FileOrganizationSelection(r.Context(), owner, r.PathValue("workspaceID"), req)
	respondFiling(w, result, err)
}

// respondFiling turns a press's outcome into the answer the page acts on.
func respondFiling(w http.ResponseWriter, result folderagent.FilingResult, err error) {
	switch {
	case errors.Is(err, filejanitor.ErrReviewPlanChanged):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "These files or the folder changed since you looked. Nothing was moved. Check the new plan.",
			"code":  "filing_changed", "preview": result.Preview,
		})
	case errors.Is(err, filejanitor.ErrFilingKeyReused):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "That press was already used for a different filing. Nothing was moved. Preview it again.",
			"code":  "filing_key_reused",
		})
	case errors.Is(err, filejanitor.ErrMoveUnsupported):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": "Ori cannot file into this folder safely on this computer. Nothing was moved.",
			"code":  "filing_unsupported",
		})
	case err != nil:
		writeError(w, err)
	default:
		orihttp.Success(w, map[string]any{"filing": result})
	}
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
