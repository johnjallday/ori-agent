package triggerhttp

import (
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/trigger"
)

// Handler serves the trigger management API and the webhook ingestion
// endpoint. Both share the same trigger.Service.
type Handler struct {
	service *trigger.Service
}

// NewHandler constructs a trigger HTTP handler.
func NewHandler(service *trigger.Service) *Handler {
	return &Handler{service: service}
}

// triggerView is the API representation of a trigger. It strips the webhook
// secret (write-only) and adds the computed webhook URL/path so the UI can
// offer a copy button and curl example (PRD #13, #27).
type triggerView struct {
	trigger.Trigger
	Secret      string `json:"secret,omitempty"` // always blanked on the way out
	HasSecret   bool   `json:"has_secret"`
	WebhookPath string `json:"webhook_path,omitempty"`
	WebhookURL  string `json:"webhook_url,omitempty"`
}

// toView builds the API representation for one trigger, computing the webhook
// URL from the request's scheme+host.
func toView(r *http.Request, t trigger.Trigger) triggerView {
	v := triggerView{Trigger: t}
	// Don't expose the queued (not-yet-executed) webhook payload through the
	// management API — it carries the raw inbound body. Completed fires are
	// summarized in FireHistory without the body.
	v.Trigger.PendingFire = nil
	if t.Webhook != nil {
		v.HasSecret = t.Webhook.Secret != ""
		// Never leak the secret.
		safe := *t.Webhook
		safe.Secret = ""
		v.Trigger.Webhook = &safe
		if t.Webhook.Token != "" {
			v.WebhookPath = "/api/hooks/" + t.Webhook.Token
			v.WebhookURL = baseURL(r) + v.WebhookPath
		}
	}
	return v
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// createTriggerRequest is the body for POST .../triggers.
type createTriggerRequest struct {
	Name      string                   `json:"name"`
	Type      trigger.Type             `json:"type"`
	Enabled   bool                     `json:"enabled"`
	Action    trigger.Action           `json:"action"`
	Webhook   *trigger.WebhookConfig   `json:"webhook,omitempty"`
	FileWatch *trigger.FileWatchConfig `json:"file_watch,omitempty"`
	Debounce  int                      `json:"debounce_seconds,omitempty"`
}

// updateTriggerRequest is the body for PUT .../triggers/{id}. Pointers
// distinguish "absent" from "set to zero".
type updateTriggerRequest struct {
	Name      *string                  `json:"name,omitempty"`
	Enabled   *bool                    `json:"enabled,omitempty"`
	Action    *trigger.Action          `json:"action,omitempty"`
	Webhook   *trigger.WebhookConfig   `json:"webhook,omitempty"`
	FileWatch *trigger.FileWatchConfig `json:"file_watch,omitempty"`
	Debounce  *int                     `json:"debounce_seconds,omitempty"`
}

// List handles GET /api/workspaces/{workspaceID}/triggers.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	wsID := workspaceID(w, r)
	if wsID == "" {
		return
	}
	triggers := h.service.List(wsID)
	views := make([]triggerView, 0, len(triggers))
	for _, t := range triggers {
		views = append(views, toView(r, t))
	}
	respond(w, http.StatusOK, map[string]any{"triggers": views})
}

// Create handles POST /api/workspaces/{workspaceID}/triggers.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	wsID := workspaceID(w, r)
	if wsID == "" {
		return
	}
	var req createTriggerRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	t := trigger.Trigger{
		WorkspaceID:     wsID,
		Name:            req.Name,
		Type:            req.Type,
		Enabled:         req.Enabled,
		Action:          req.Action,
		Webhook:         req.Webhook,
		FileWatch:       req.FileWatch,
		DebounceSeconds: req.Debounce,
	}
	created, err := h.service.Create(t)
	if err != nil {
		writeTriggerError(w, err)
		return
	}
	respond(w, http.StatusCreated, toView(r, created))
}

// Get handles GET /api/workspaces/{workspaceID}/triggers/{triggerID}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	wsID, tID := workspaceAndTriggerID(w, r)
	if wsID == "" || tID == "" {
		return
	}
	t, err := h.service.Get(wsID, tID)
	if err != nil {
		writeTriggerError(w, err)
		return
	}
	respond(w, http.StatusOK, toView(r, t))
}

// Update handles PUT /api/workspaces/{workspaceID}/triggers/{triggerID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPut) {
		return
	}
	wsID, tID := workspaceAndTriggerID(w, r)
	if wsID == "" || tID == "" {
		return
	}
	var req updateTriggerRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	updated, err := h.service.Update(wsID, tID, func(t *trigger.Trigger) error {
		if req.Name != nil {
			t.Name = *req.Name
		}
		if req.Enabled != nil {
			t.Enabled = *req.Enabled
		}
		if req.Action != nil {
			t.Action = *req.Action
		}
		if req.Debounce != nil {
			t.DebounceSeconds = *req.Debounce
		}
		if req.FileWatch != nil {
			t.FileWatch = req.FileWatch
		}
		if req.Webhook != nil {
			// Only the secret is updatable here (empty clears it). The token is
			// never settable via the API — it is generated on create and
			// rotated only through regenerate-token — so a caller can't install
			// a weak, guessable token through this path.
			if t.Webhook == nil {
				t.Webhook = &trigger.WebhookConfig{}
			}
			t.Webhook.Secret = req.Webhook.Secret
		}
		return nil
	})
	if err != nil {
		writeTriggerError(w, err)
		return
	}
	respond(w, http.StatusOK, toView(r, updated))
}

// SetEnabled handles POST .../triggers/{triggerID}/enable and /disable.
func (h *Handler) SetEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !orihttp.RequireMethod(w, r, http.MethodPost) {
			return
		}
		wsID, tID := workspaceAndTriggerID(w, r)
		if wsID == "" || tID == "" {
			return
		}
		updated, err := h.service.SetEnabled(wsID, tID, enabled)
		if err != nil {
			writeTriggerError(w, err)
			return
		}
		respond(w, http.StatusOK, toView(r, updated))
	}
}

// RegenerateToken handles POST .../triggers/{triggerID}/regenerate-token.
func (h *Handler) RegenerateToken(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	wsID, tID := workspaceAndTriggerID(w, r)
	if wsID == "" || tID == "" {
		return
	}
	updated, err := h.service.RegenerateToken(wsID, tID)
	if err != nil {
		writeTriggerError(w, err)
		return
	}
	respond(w, http.StatusOK, toView(r, updated))
}

// TestFire handles POST .../triggers/{triggerID}/test-fire.
func (h *Handler) TestFire(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	wsID, tID := workspaceAndTriggerID(w, r)
	if wsID == "" || tID == "" {
		return
	}
	rec, err := h.service.TestFire(wsID, tID)
	if err != nil {
		writeTriggerError(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"fire": rec})
}

// Fires handles GET .../triggers/{triggerID}/fires (recent fire history).
func (h *Handler) Fires(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	wsID, tID := workspaceAndTriggerID(w, r)
	if wsID == "" || tID == "" {
		return
	}
	t, err := h.service.Get(wsID, tID)
	if err != nil {
		writeTriggerError(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"fires": t.FireHistory})
}

// Delete handles DELETE /api/workspaces/{workspaceID}/triggers/{triggerID}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodDelete) {
		return
	}
	wsID, tID := workspaceAndTriggerID(w, r)
	if wsID == "" || tID == "" {
		return
	}
	if err := h.service.Delete(wsID, tID); err != nil {
		writeTriggerError(w, err)
		return
	}
	orihttp.RespondNoContent(w)
}

// --- helpers ---

func workspaceID(w http.ResponseWriter, r *http.Request) string {
	id := strings.TrimSpace(r.PathValue("workspaceID"))
	if id == "" {
		orihttp.BadRequest(w, "workspaceID is required")
	}
	return id
}

func workspaceAndTriggerID(w http.ResponseWriter, r *http.Request) (string, string) {
	wsID := workspaceID(w, r)
	if wsID == "" {
		return "", ""
	}
	tID := strings.TrimSpace(r.PathValue("triggerID"))
	if tID == "" {
		orihttp.BadRequest(w, "triggerID is required")
		return "", ""
	}
	return wsID, tID
}

func respond(w http.ResponseWriter, status int, data any) {
	if err := orihttp.RespondJSON(w, status, data); err != nil {
		logger.Error("triggerhttp: encode response", logger.Fields{"error": err})
	}
}

func writeTriggerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, trigger.ErrNotFound):
		orihttp.NotFound(w, "trigger not found")
	default:
		// Validation and watch-path failures are user-fixable input errors.
		orihttp.BadRequest(w, err.Error())
	}
}
