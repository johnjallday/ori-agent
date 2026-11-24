// Package namesearchhttp serves POST /api/name-search: the by-name search behind
// Home's Files view.
//
// The term is private. It travels in the request body, never in the address, the
// response is never cached, and nothing here writes it anywhere: errors carry
// fixed text, and the only thing logged is which kind could not be read (by the
// service) and never any part of the request.
package namesearchhttp

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/namesearch"
)

// Path is the route.
const Path = "/api/name-search"

// maxBody bounds a request: a 512-byte term, a scope and a few cursors.
const maxBody = 4096

// Handler serves the search. Unlike the filename index it is not limited to the
// Mac that runs Ori: it reads only Ori's own records, so a browser elsewhere gets
// the same answer, and the page says plainly that linked-folder filenames are
// searched only on that Mac.
type Handler struct {
	Service *namesearch.Service
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Before anything else, so no early return can be cached.
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	// The term belongs in the body; one in the address would end up in history,
	// logs and referrers.
	if r.URL.RawQuery != "" {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}
	// A page on another site must not be able to read Ori's names.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		http.Error(w, "Same-origin requests only.", http.StatusForbidden)
		return
	}
	if kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || kind != "application/json" {
		http.Error(w, "JSON required.", http.StatusUnsupportedMediaType)
		return
	}
	if h.Service == nil {
		http.Error(w, "Search is unavailable.", http.StatusServiceUnavailable)
		return
	}

	// Not orihttp.ParseJSONBody: it puts the decoder's error text, which can
	// quote the input, into the reply.
	var req namesearch.Request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}

	response, err := h.Service.Search(r.Context(), req)
	if err != nil {
		if errors.Is(err, namesearch.ErrInvalid) {
			http.Error(w, "Invalid request.", http.StatusBadRequest)
			return
		}
		http.Error(w, "Search is unavailable.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
