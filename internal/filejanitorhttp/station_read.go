package filejanitorhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
)

func (h *Handler) stationReadRequest(w http.ResponseWriter, r *http.Request, body any) (string, bool) {
	if connectionshttp.NewOriginGuard().Check(r) != nil {
		_ = orihttp.RespondForbidden(w, "Local access required.")
		return "", false
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil || owner == "" {
		_ = orihttp.RespondForbidden(w, "Your workspace identity is unavailable.")
		return "", false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(body) != nil || dec.Decode(&struct{}{}) != io.EOF {
		_ = orihttp.RespondBadRequest(w, "Review the loose files in this picture.")
		return "", false
	}
	return owner, true
}

func (h *Handler) StationReadReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req filejanitor.StationReadRequest
	owner, ok := h.stationReadRequest(w, r, &req)
	if !ok {
		return
	}
	review, err := h.service.PreviewStationRead(r.Context(), owner, id, r.PathValue("batchID"), req)
	if err != nil {
		h.respondPictureError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"review": review})
}

func (h *Handler) StationReadLines(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Approval string `json:"approval"`
	}
	owner, ok := h.stationReadRequest(w, r, &req)
	if !ok {
		return
	}
	result, err := h.service.ReadStationLines(r.Context(), owner, id, r.PathValue("batchID"), req.Approval)
	if err != nil {
		// Never expose the provider's raw error, request or completion.
		code, msg := "read_unavailable", "No suggestions were applied. Check the folder and processor, then review the read again."
		var p *llm.ProviderError
		if errors.As(err, &p) {
			code = "processor_" + string(p.Category)
			msg = "The approved processor could not complete this read. No suggestions were applied."
		} else if errors.Is(err, filejanitor.ErrStationReplyInvalid) {
			code = "unverified_suggestions"
			msg = "The reply did not identify valid files and folders. No suggestions were applied."
		}
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError(code, msg))
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"result": result})
}
