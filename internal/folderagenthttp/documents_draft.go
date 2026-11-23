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
		var folderErr *personalassistant.SecondBrainFolderError
		switch {
		case errors.As(err, &folderErr):
			fail(w, 409, folderErr.Message)
		case errors.Is(err, personalassistant.ErrSecondBrainDestinationChanged):
			fail(w, 409, "The Notes location or setup decision changed. Open Notes to check it before continuing.")
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
func (h *Handler) ReviewDocumentsSetup(w http.ResponseWriter, r *http.Request) {
	h.documentsSetup(w, r, false)
}
func (h *Handler) SetupDocumentsDraft(w http.ResponseWriter, r *http.Request) {
	h.documentsSetup(w, r, true)
}
func (h *Handler) documentsSetup(w http.ResponseWriter, r *http.Request, confirm bool) {
	var in folderagent.DocumentsDraftInput
	owner, ok := h.draftRequest(w, r, &in)
	if !ok {
		return
	}
	v, err := h.service.SetupDocumentsDraft(r.Context(), owner, r.PathValue("workspaceID"), in, confirm)
	draftResult(w, v, err)
}
func (h *Handler) ChooseDocumentsVault(w http.ResponseWriter, r *http.Request) {
	var in folderagent.DocumentsDraftInput
	owner, ok := h.draftRequest(w, r, &in)
	if !ok {
		return
	}
	token, err := h.service.ChooseDocumentsVault(r.Context(), owner, r.PathValue("workspaceID"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"selection_token": token})
}

func (h *Handler) DismissDocuments(w http.ResponseWriter, r *http.Request) {
	var in folderagent.DocumentsDraftInput
	owner, ok := h.draftRequest(w, r, &in)
	if !ok {
		return
	}
	if err := h.service.DismissDocuments(r.Context(), owner, r.PathValue("workspaceID"), in); err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"dismissed": true})
}
func (h *Handler) OpenDocumentsDraft(w http.ResponseWriter, r *http.Request) {
	h.documentsResult(w, r, false)
}
func (h *Handler) RecoverDocumentsResult(w http.ResponseWriter, r *http.Request) {
	h.documentsResult(w, r, true)
}
func (h *Handler) documentsResult(w http.ResponseWriter, r *http.Request, recover bool) {
	var in struct {
		HQ      string `json:"hq"`
		Attempt string `json:"attempt"`
		Request string `json:"request_id"`
	}
	owner, ok := h.draftRequest(w, r, &in)
	if !ok {
		return
	}
	result := h.service.OpenDocumentsDraft
	if recover {
		result = h.service.RecoverDocumentsResult
	}
	v, err := result(r.Context(), owner, r.PathValue("workspaceID"), in.HQ, in.Attempt, in.Request)
	draftResult(w, v, err)
}
