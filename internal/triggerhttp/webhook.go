// Package triggerhttp exposes the HTTP surface for event triggers: the public
// webhook ingestion endpoint (POST /api/hooks/{token}) and the per-workspace
// trigger management API.
package triggerhttp

import (
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/trigger"
)

// maxWebhookBody bounds how much of a webhook request we read. One byte over
// the payload cap lets us distinguish "exactly at cap" from "too large" and
// return 413 rather than silently truncating.
const maxWebhookBody = trigger.MaxPayloadBytes + 1

// secretHeader is the header callers present when a webhook trigger defines a
// shared secret.
const secretHeader = "X-Ori-Webhook-Secret"

// HandleWebhook serves POST /api/hooks/{token}. It validates the content type
// and size, then hands off to the service, which resolves the token, checks
// the secret and rate limit, and queues the event. The response is a fast
// 202 (or an error status) — the run executes asynchronously (PRD #8–12, #25).
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		// No token means no route match in practice; respond like any other
		// unknown hook so we don't leak structure.
		http.NotFound(w, r)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if !trigger.AcceptedContentType(contentType) {
		orihttp.RespondError(w, http.StatusUnsupportedMediaType, "unsupported content type")
		return
	}

	limited := http.MaxBytesReader(w, r.Body, maxWebhookBody)
	body, err := io.ReadAll(limited)
	if err != nil {
		// MaxBytesReader signals an over-limit read via its error.
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			orihttp.RespondError(w, http.StatusRequestEntityTooLarge, "payload too large")
			return
		}
		orihttp.BadRequest(w, "failed to read request body")
		return
	}
	if len(body) > trigger.MaxPayloadBytes {
		orihttp.RespondError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}

	ev := trigger.WebhookEventFromRequest(contentType, clientIP(r), string(body))
	secret := r.Header.Get(secretHeader)

	fireID, result := h.service.IngestWebhook(token, secret, ev)
	status := trigger.StatusForIngest(result)

	if result != trigger.IngestAccepted {
		// Uniform bodies; 404 deliberately does not distinguish unknown from
		// disabled (PRD #9).
		orihttp.RespondError(w, status, http.StatusText(status))
		return
	}

	if err := orihttp.RespondJSON(w, http.StatusAccepted, acceptedResponse{
		Accepted: true,
		FireID:   fireID,
	}); err != nil {
		logger.Error("triggerhttp: encode webhook response", logger.Fields{"error": err})
	}
}

// acceptedResponse is the 202 body for an accepted webhook (PRD #11).
type acceptedResponse struct {
	Accepted bool   `json:"accepted"`
	FireID   string `json:"fire_id"`
}

// clientIP extracts a best-effort client address for the event summary. It
// trusts X-Forwarded-For's first hop when present (the user is behind their
// own tunnel/proxy), falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}
