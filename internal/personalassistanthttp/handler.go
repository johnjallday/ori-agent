// Package personalassistanthttp exposes the bounded user-scoped personal
// assistant API. Consequential hire/assignment mutations are added separately;
// this handler begins with the structurally read-only state projection.
package personalassistanthttp

import (
	"context"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// StateReader is the read-only service contract used by the HTTP layer.
type StateReader interface {
	Get(ctx context.Context, userID string) (*personalassistant.Projection, error)
}

// Handler serves /api/personal-assistant.
type Handler struct {
	service  StateReader
	provider userprofile.UserProvider
}

// NewHandler constructs a personal-assistant HTTP handler.
func NewHandler(service StateReader, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, provider: provider}
}

// GetState handles GET /api/personal-assistant.
func (h *Handler) GetState(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.service == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant service is unavailable")
		return
	}
	userID, err := h.provider.CurrentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user")
		return
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	projection, err := h.service.Get(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "personal assistant state is temporarily unavailable")
		return
	}
	orihttp.Success(w, map[string]any{"personal_assistant": projection})
}
