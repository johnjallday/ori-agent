package filejanitorhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// maxFileRequestBytes bounds the one File request: five short fields.
const maxFileRequestBytes = 4 * 1024

// fileRequestBody is the closed shape of one press. Unknown fields are refused.
type fileRequestBody struct {
	ExpectedRevision      string `json:"expected_revision"`
	ExpectedGroupRevision string `json:"expected_group_revision"`
	PlanDigest            string `json:"plan_digest"`
	TrashAcknowledged     int    `json:"trash_acknowledged"`
	ClientKey             string `json:"client_key"`
}

// FileReviewPlan serves the one File request: it files exactly the plan the
// picture last showed, or nothing. A refusal because the plan changed carries the
// fresh plan, so the picture can redraw and the person can press again.
func (h *Handler) FileReviewPlan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("layout") == "shelves" {
		h.fileShelfPicture(w, r, workspaceID)
		return
	}
	if r.ContentLength > maxFileRequestBytes {
		_ = orihttp.RespondBadRequest(w, "That request is too large.")
		return
	}
	var body fileRequestBody
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFileRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		_ = orihttp.RespondBadRequest(w, "That filing request is not valid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		_ = orihttp.RespondBadRequest(w, "Send exactly one filing request.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	result, err := h.service.FileReviewPlan(r.Context(), owner, workspaceID, r.PathValue("batchID"), filejanitor.FileRequest{
		ExpectedRevision: body.ExpectedRevision, ExpectedGroupRevision: body.ExpectedGroupRevision,
		PlanDigest: body.PlanDigest, TrashAcknowledged: body.TrashAcknowledged, ClientKey: body.ClientKey,
	})
	if err != nil {
		h.respondFilingError(w, err, result)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "filing": result})
}

// CheckShelfName is a metadata-only probe; the name is not stored, reserved,
// or made by checking it. Never take the parent as a path.
func (h *Handler) CheckShelfName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondForbidden(w, "Your workspace identity is unavailable.")
		return
	}
	var b struct {
		Revision string `json:"revision"`
		Parent   string `json:"parent"`
		Name     string `json:"name"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	dec.DisallowUnknownFields()
	if dec.Decode(&b) != nil || dec.Decode(&struct{}{}) != io.EOF {
		_ = orihttp.RespondBadRequest(w, "Send one folder name.")
		return
	}
	if err = h.service.CheckShelfName(r.Context(), owner, id, r.PathValue("batchID"), b.Revision, b.Parent, b.Name); err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("folder_unavailable", "That name is not available here. Choose a different name or reload the picture."))
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"available": true})
}

func (h *Handler) fileShelfPicture(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Revision   string                       `json:"revision"`
		Key        string                       `json:"client_key"`
		Placements []filejanitor.ShelfPlacement `json:"placements"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 384<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		_ = orihttp.RespondBadRequest(w, "Send one reviewed folder picture.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondForbidden(w, "Your workspace identity is unavailable.")
		return
	}
	result, err := h.service.FileShelfPicture(r.Context(), owner, id, r.PathValue("batchID"), body.Revision, body.Key, body.Placements)
	if err != nil {
		h.respondFilingError(w, err, filejanitor.FilingResult{})
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "filing": result})
}

func (h *Handler) respondFilingError(w http.ResponseWriter, err error, result filejanitor.FilingResult) {
	switch {
	case errors.Is(err, filejanitor.ErrReviewPlanChanged):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("plan_changed", "The folder changed since you looked. Nothing was filed. Look at the picture again, then press File.").
				WithDetails(map[string]any{"plan": result.Plan}))
	case errors.Is(err, filejanitor.ErrTrashNotAcknowledged):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("trash_not_acknowledged", "Files are going to the Trash and that was not confirmed. Nothing was filed.").
				WithDetails(map[string]any{"plan": result.Plan}))
	case errors.Is(err, filejanitor.ErrReviewBounds):
		_ = orihttp.RespondAPIError(w, http.StatusRequestEntityTooLarge,
			orihttp.NewAPIError("review_bounds", "Ori files up to 1,000 files at a time. Untick a folder to continue."))
	case errors.Is(err, filejanitor.ErrReviewConflict):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_conflict", "This review or its saved choices changed. Reload before filing; nothing was filed."))
	case errors.Is(err, filejanitor.ErrBatchNotFound):
		_ = orihttp.RespondNotFound(w, "batch not found")
	case errors.Is(err, filejanitor.ErrInvalidAction):
		_ = orihttp.RespondBadRequest(w, "That filing request is not one Ori can carry out.")
	default:
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_unavailable", "This saved review is unavailable. Check Filing setup and reload the current review."))
	}
}
