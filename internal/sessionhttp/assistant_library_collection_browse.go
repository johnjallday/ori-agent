package sessionhttp

import (
	"net/http"
	"net/url"
	"strconv"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func collectionBrowseQuery(raw string) (string, int, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, err
	}
	pin, offset := "", 0
	for key, values := range query {
		if len(values) != 1 {
			return "", 0, projectlibrary.ErrConflict
		}
		switch key {
		case "context_pin":
			pin = values[0]
			if len(pin) != 64 {
				return "", 0, projectlibrary.ErrConflict
			}
		case "offset":
			offset, err = strconv.Atoi(values[0])
			if err != nil || offset < 0 {
				return "", 0, projectlibrary.ErrConflict
			}
		default:
			return "", 0, projectlibrary.ErrConflict
		}
	}
	return pin, offset, nil
}

// Owner-visible catalog metadata remains readable after provider removal.
// These routes never resolve or impersonate a Manager and never initialize.
func (h *Handler) GetAssistantLibraryCollections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	pin, offset, err := collectionBrowseQuery(r.URL.RawQuery)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid collection catalog request")
		return
	}
	page, err := h.assistantLibraryStore().CollectionCatalog(r.Context(), scope, pin, offset)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, page)
}
func (h *Handler) GetAssistantLibraryCollectionMembers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	pin, offset, err := collectionBrowseQuery(r.URL.RawQuery)
	if err != nil || pin == "" {
		_ = orihttp.RespondBadRequest(w, "Invalid collection membership request")
		return
	}
	page, err := h.assistantLibraryStore().CollectionMembers(r.Context(), scope, r.PathValue("collectionID"), pin, offset)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, page)
}
