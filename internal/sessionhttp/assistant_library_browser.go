package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func browserReadHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
}

func browserQuery(r *http.Request, preview bool) (string, string, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", "", false
	}
	for key, values := range query {
		allowed := key == "path" || (preview && key == "version")
		if len(values) != 1 || !allowed {
			return "", "", false
		}
	}
	if preview && (query.Get("path") == "" || query.Get("version") == "") {
		return "", "", false
	}
	return query.Get("path"), query.Get("version"), true
}

func browserReadError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusForbidden, "denied", projectlibrary.ErrBrowserDenied.Error()
	switch {
	case errors.Is(err, context.Canceled):
		return // The client abandoned this read; never expose a buffered preview.
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusGatewayTimeout, "timeout", "The folder read timed out. Try again."
	case errors.Is(err, projectlibrary.ErrBrowserMissing):
		status, code, message = http.StatusNotFound, "missing", err.Error()
	case errors.Is(err, projectlibrary.ErrBrowserChanged):
		status, code, message = http.StatusConflict, "changed", err.Error()
	case errors.Is(err, projectlibrary.ErrBrowserLarge):
		status, code, message = http.StatusRequestEntityTooLarge, "oversized", err.Error()
	case errors.Is(err, projectlibrary.ErrBrowserUnsupported):
		status, code, message = http.StatusUnsupportedMediaType, "unsupported", err.Error()
	}
	_ = orihttp.RespondJSON(w, status, map[string]string{"error": message, "code": code})
}

func (h *Handler) ListAssistantLibraryFiles(w http.ResponseWriter, r *http.Request) {
	browserReadHeaders(w)
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	relative, _, ok := browserQuery(r, false)
	if !ok {
		_ = orihttp.RespondBadRequest(w, "Invalid project folder query")
		return
	}
	listing, err := h.assistantLibraryStore().BrowserList(r.Context(), scope, r.PathValue("entryID"), relative)
	if err != nil {
		browserReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, listing)
}

func (h *Handler) PreviewAssistantLibraryFile(w http.ResponseWriter, r *http.Request) {
	browserReadHeaders(w)
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	relative, version, ok := browserQuery(r, true)
	if !ok {
		_ = orihttp.RespondBadRequest(w, "Invalid project preview query")
		return
	}
	preview, err := h.assistantLibraryStore().BrowserPreview(r.Context(), scope, r.PathValue("entryID"), relative, version)
	if err != nil {
		browserReadError(w, err)
		return
	}
	w.Header().Set("Content-Type", preview.MediaType)
	if _, err := w.Write(preview.Bytes); err != nil {
		return
	}
}
