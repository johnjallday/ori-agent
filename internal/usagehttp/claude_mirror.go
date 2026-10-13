package usagehttp

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/usagelimits"
)

type mirrorInstaller interface {
	Status() usagelimits.MirrorStatus
	Install() error
	Remove() error
}

func (h *Handler) SetMirrorInstaller(installer mirrorInstaller) { h.mirror = installer }

// ClaudeMirror changes the local status-line command only on an explicit JSON
// POST. Cross-origin/rebinding requests must never reach the filesystem writer.
func (h *Handler) ClaudeMirror(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethods(w, r, http.MethodPost) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if connectionshttp.NewOriginGuard().Check(r) != nil || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) || u.User != nil {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || (request.Action != "install" && request.Action != "remove") {
		orihttp.BadRequest(w, "Expected one install or remove action")
		return
	}
	if h.mirror == nil {
		http.Error(w, "Status-line mirror is unavailable", http.StatusServiceUnavailable)
		return
	}
	if request.Action == "install" {
		err = h.mirror.Install()
	} else {
		err = h.mirror.Remove()
	}
	if err != nil {
		// Installer errors are fixed, human-facing explanations: never raw IO
		// errors, previous commands, settings contents, or credential paths.
		message := err.Error()
		if message != "" {
			message = strings.ToUpper(message[:1]) + message[1:]
		}
		orihttp.BadRequest(w, message)
		return
	}
	orihttp.WriteJSON(w, h.mirror.Status())
}
