package sessionhttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func decodeCollectionCorrection(w http.ResponseWriter, r *http.Request) (projectlibrary.CollectionCorrectionRequest, error) {
	var out projectlibrary.CollectionCorrectionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return out, projectlibrary.ErrConflict
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return out, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return out, projectlibrary.ErrConflict
		}
		seen[name] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return out, err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return out, projectlibrary.ErrConflict
		}
		switch name {
		case "review_token":
			err = json.Unmarshal(raw, &out.ReviewToken)
		case "operation":
			err = json.Unmarshal(raw, &out.Operation)
		case "name":
			var value string
			err = json.Unmarshal(raw, &value)
			out.Name = &value
		case "exclude_ids":
			err = json.Unmarshal(raw, &out.ExcludeIDs)
		case "add_ids":
			err = json.Unmarshal(raw, &out.AddIDs)
		case "convert":
			err = json.Unmarshal(raw, &out.Convert)
		case "resolve_question":
			var value int
			err = json.Unmarshal(raw, &value)
			out.ResolveQuestion = &value
		case "resolution":
			err = json.Unmarshal(raw, &out.Resolution)
		case "release_unchanged":
			err = json.Unmarshal(raw, &out.ReleaseUnchanged)
			if err == nil && !out.ReleaseUnchanged {
				return out, projectlibrary.ErrConflict
			}
		default:
			return out, projectlibrary.ErrConflict
		}
		if err != nil {
			return out, err
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || !seen["review_token"] || !seen["operation"] {
		return out, projectlibrary.ErrConflict
	}
	if _, err = decoder.Token(); err != io.EOF {
		return out, projectlibrary.ErrConflict
	}
	return out, nil
}

func (h *Handler) CorrectAssistantLibraryCollectionProposal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	in, err := decodeCollectionCorrection(w, r)
	if err != nil || r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid collection correction")
		return
	}
	if h.collectionReview == nil {
		collectionReviewFailure(w, projectlibrary.ErrUnavailable)
		return
	}
	view, err := h.collectionReview.Correct(r.Context(), scope.OwnerUserID, scope.HomeID, r.PathValue("arrangementID"), in)
	if err != nil {
		collectionReviewFailure(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
