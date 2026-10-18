package sessionhttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func (h *Handler) SetCollectionReviewService(service *projectlibrary.CollectionReviewService) {
	h.collectionReview = service
}

// decodeCollectionChoice accepts only the exact small flat host request. Case
// aliases, duplicates, nulls, nested values and trailing JSON never imply consent.
func decodeCollectionChoice(w http.ResponseWriter, r *http.Request, commit bool) (map[string]any, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, projectlibrary.ErrConflict
	}
	result := map[string]any{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, projectlibrary.ErrConflict
		}
		if _, exists := result[name]; exists {
			return nil, projectlibrary.ErrConflict
		}
		value, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if commit {
			switch name {
			case "review_token", "idempotency_key":
				text, ok := value.(string)
				if !ok || len(text) == 0 || len(text) > 160 {
					return nil, projectlibrary.ErrConflict
				}
			case "confirm":
				confirmed, ok := value.(bool)
				if !ok || !confirmed {
					return nil, projectlibrary.ErrConflict
				}
			default:
				return nil, projectlibrary.ErrConflict
			}
		} else {
			if name != "expected_revision" && name != "expected_record_revision" {
				return nil, projectlibrary.ErrConflict
			}
			number, ok := value.(json.Number)
			if !ok {
				return nil, projectlibrary.ErrConflict
			}
			n, err := number.Int64()
			if err != nil || n < 1 {
				return nil, projectlibrary.ErrConflict
			}
			value = n
		}
		result[name] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, projectlibrary.ErrConflict
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, projectlibrary.ErrConflict
	}
	expected := 2
	if commit {
		expected = 3
	}
	if len(result) != expected {
		return nil, projectlibrary.ErrConflict
	}
	return result, nil
}

func collectionReviewFailure(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	if errors.Is(err, projectlibrary.ErrConflict) || errors.Is(err, projectlibrary.ErrUnavailable) {
		status = http.StatusConflict
	}
	if errors.Is(err, projectlibrary.ErrLimit) {
		status = http.StatusRequestEntityTooLarge
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "Collection review or its saved outcome could not be confirmed. Reopen saved history before another request. No source-file changes, workspace creation or project activation is performed here; preparing a queue still requires independent per-project review."})
}

func (h *Handler) ReviewAssistantLibraryCollections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	in, err := decodeCollectionChoice(w, r, false)
	if err != nil || r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid collection review request")
		return
	}
	if h.collectionReview == nil {
		collectionReviewFailure(w, projectlibrary.ErrUnavailable)
		return
	}
	view, err := h.collectionReview.Review(r.Context(), scope.OwnerUserID, scope.HomeID, r.PathValue("arrangementID"), in["expected_revision"].(int64), in["expected_record_revision"].(int64))
	if err != nil {
		collectionReviewFailure(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
func (h *Handler) GetAssistantLibraryCollectionReview(w http.ResponseWriter, r *http.Request) {
	h.getAssistantLibraryCollectionPage(w, r, false)
}
func (h *Handler) GetAssistantLibraryCollectionCandidates(w http.ResponseWriter, r *http.Request) {
	h.getAssistantLibraryCollectionPage(w, r, true)
}
func (h *Handler) getAssistantLibraryCollectionPage(w http.ResponseWriter, r *http.Request, candidates bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	operation, offset := 0, 0
	search := ""
	for key, values := range query {
		allowed := key == "operation" || key == "offset" || (candidates && key == "query")
		if len(values) != 1 || !allowed {
			err = projectlibrary.ErrConflict
			break
		}
		if key == "query" {
			search = values[0]
			continue
		}
		n, e := strconv.Atoi(values[0])
		if e != nil || n < 0 {
			err = projectlibrary.ErrConflict
			break
		}
		if key == "operation" {
			operation = n
		} else {
			offset = n
		}
	}
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid collection review page")
		return
	}
	if h.collectionReview == nil {
		collectionReviewFailure(w, projectlibrary.ErrUnavailable)
		return
	}
	if candidates {
		view, err := h.collectionReview.Candidates(r.Context(), scope.OwnerUserID, scope.HomeID, r.PathValue("arrangementID"), r.PathValue("reviewToken"), operation, offset, search)
		if err != nil {
			collectionReviewFailure(w, err)
			return
		}
		_ = orihttp.RespondSuccess(w, view)
		return
	}
	view, err := h.collectionReview.Page(r.Context(), scope.OwnerUserID, scope.HomeID, r.PathValue("arrangementID"), r.PathValue("reviewToken"), operation, offset)
	if err != nil {
		collectionReviewFailure(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
func (h *Handler) CommitAssistantLibraryCollections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	in, err := decodeCollectionChoice(w, r, true)
	if err != nil || r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid collection confirmation")
		return
	}
	if h.collectionReview == nil {
		collectionReviewFailure(w, projectlibrary.ErrUnavailable)
		return
	}
	view, err := h.collectionReview.Commit(r.Context(), scope.OwnerUserID, scope.HomeID, r.PathValue("arrangementID"), in["review_token"].(string), in["idempotency_key"].(string), true)
	if err != nil {
		collectionReviewFailure(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
