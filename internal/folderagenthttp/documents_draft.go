package folderagenthttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func (h *Handler) draftRequest(w http.ResponseWriter, r *http.Request, body any) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if connectionshttp.NewOriginGuard().Check(r) != nil {
		fail(w, 403, "Local access required")
		return "", false
	}
	user, ok := h.user(w, r)
	if !ok {
		return "", false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	d.DisallowUnknownFields()
	if d.Decode(body) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "Review this draft again.")
		return "", false
	}
	return user, true
}
func draftResult(w http.ResponseWriter, v folderagent.DocumentsDraftView, err error) {
	if err != nil {
		switch {
		case errors.Is(err, personalassistant.ErrSecondBrainNotConnected), errors.Is(err, personalassistant.ErrSecondBrainFillInNotAllowed):
			fail(w, 409, "Writable Notes are not connected. Open Notes to check the connection.")
		default:
			writeError(w, err)
		}
		return
	}
	orihttp.Success(w, map[string]any{"draft": v})
}

func (h *Handler) ReviewDocumentsDraft(w http.ResponseWriter, r *http.Request) {
	h.documentsDraft(w, r, false)
}
func (h *Handler) SaveDocumentsDraft(w http.ResponseWriter, r *http.Request) {
	h.documentsDraft(w, r, true)
}
func (h *Handler) documentsDraft(w http.ResponseWriter, r *http.Request, save bool) {
	var in folderagent.DocumentsDraftInput
	owner, ok := h.draftRequest(w, r, &in)
	if !ok {
		return
	}
	v, err := h.service.DocumentsDraft(r.Context(), owner, r.PathValue("workspaceID"), in, save)
	draftResult(w, v, err)
}
func (h *Handler) OpenDocumentsDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HQ      string `json:"hq"`
		Attempt string `json:"attempt"`
		Request string `json:"request_id"`
	}
	owner, ok := h.draftRequest(w, r, &in)
	if !ok {
		return
	}
	v, err := h.service.OpenDocumentsDraft(r.Context(), owner, r.PathValue("workspaceID"), in.HQ, in.Attempt, in.Request)
	draftResult(w, v, err)
}
