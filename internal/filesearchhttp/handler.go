// Package filesearchhttp exposes local browser actions, not agent tools.
package filesearchhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/learningmissionshttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

const Prefix = "/api/file-search/"

type Handler struct {
	Service *filesearch.Service
	Users   userprofile.UserProvider
	// Accepted records first-open landing only, never on cancel or a failed grant.
	Accepted func() error
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	peer, _, e := net.SplitHostPort(r.RemoteAddr)
	site := r.Header.Get("Sec-Fetch-Site")
	if e != nil || !net.ParseIP(peer).IsLoopback() || (site != "" && site != "same-origin") || r.URL.RawQuery != "" {
		http.Error(w, "Local search only.", http.StatusForbidden)
		return
	}
	op := strings.TrimPrefix(r.URL.Path, Prefix)
	read := op == "sources"
	if (read && r.Method != "GET") || (!read && r.Method != "POST") {
		w.WriteHeader(405)
		return
	}
	owner, e := learningmissionshttp.LocalOwner(r, h.Users, !read)
	if e != nil || (read && r.Header.Get("X-Ori-Search") != "1") {
		http.Error(w, "Local search only.", http.StatusForbidden)
		return
	}
	if h.Service == nil {
		http.Error(w, "Folder search is unavailable.", http.StatusServiceUnavailable)
		return
	}
	decode := func(v any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		d.DisallowUnknownFields()
		if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
			http.Error(w, "Invalid request.", http.StatusBadRequest)
			return false
		}
		return true
	}
	var out any
	switch op {
	case "sources":
		out, e = h.Service.Sources(r.Context(), owner)
	case "begin":
		var in struct {
			Kind string `json:"kind"`
		}
		if !decode(&in) {
			return
		}
		var id string
		id, e = h.Service.Begin(owner, in.Kind)
		out = map[string]string{"intent": id}
	case "select":
		var in struct {
			Intent string `json:"intent"`
		}
		if !decode(&in) {
			return
		}
		source, err := h.Service.Select(r.Context(), owner, in.Intent)
		out, e = source, err
		if err == nil && source != nil && h.Accepted != nil && h.Accepted() != nil {
			http.Error(w, "Folder added, but the starting page could not be saved. Reload to check.", http.StatusConflict)
			return
		}
	case "query":
		var in struct {
			Term   string `json:"term"`
			Cursor string `json:"cursor"`
			Limit  int    `json:"limit"`
		}
		if !decode(&in) {
			return
		}
		out, e = h.Service.Query(r.Context(), owner, in.Term, in.Cursor, in.Limit)
	case "remove":
		var in struct {
			ID string `json:"id"`
		}
		if !decode(&in) {
			return
		}
		e = h.Service.Remove(r.Context(), owner, in.ID)
	case "open", "reveal":
		var in struct {
			ID         string `json:"id"`
			Generation int    `json:"generation"`
		}
		if !decode(&in) {
			return
		}
		e = h.Service.Act(r.Context(), owner, in.ID, in.Generation, op == "reveal")
	case "refresh":
		var in struct {
			ID         string `json:"id"`
			Generation int    `json:"generation"`
		}
		if !decode(&in) {
			return
		}
		e = h.Service.Refresh(r.Context(), owner, in.ID, in.Generation)
	default:
		w.WriteHeader(404)
		return
	}
	if e != nil {
		status := 409
		switch {
		case errors.Is(e, filesearch.ErrRequest):
			status = 400
		case errors.Is(e, filesearch.ErrUnavailable):
			status = 503
		}
		message := e.Error()
		http.Error(w, strings.ToUpper(message[:1])+message[1:]+".", status)
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}
