package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// SetupLearningService is what the setup beat's card reads and dismisses: the
// candidates the user's approved filing raised in the review queue. Answering one
// is the Profile page's own approve and reject; only the batch dismissal lives
// here.
type SetupLearningService interface {
	List(ctx context.Context, userID string) (personalassistant.SetupLearningView, error)
	Dismiss(ctx context.Context, userID, requestID string) (int, error)
}

// SetSetupLearning adds the setup beat's card. It shares the second brain's
// origin guard: the card is drawn by Ori's own page and by nothing else.
func (h *Handler) SetSetupLearning(service SetupLearningService) {
	if h != nil {
		h.setupLearning = service
	}
}

func (h *Handler) setupLearningReady(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.setupLearning == nil || h.secondBrainOrigin == nil {
		orihttp.ServiceUnavailable(w, "What Ori learned is unavailable")
		return "", false
	}
	if err := h.secondBrainOrigin(r); err != nil {
		orihttp.Forbidden(w, "This request did not come from Ori's own page")
		return "", false
	}
	return h.currentUserID(w, r)
}

// GetSetupLearning handles GET /api/personal-assistant/setup-learning: the
// learnings waiting for an answer, each with the version and id the approve and
// reject actions take. It reads the queue and writes nothing.
func (h *Handler) GetSetupLearning(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.setupLearningReady(w, r)
	if !ok {
		return
	}
	view, err := h.setupLearning.List(r.Context(), userID)
	if err != nil {
		writeSetupLearningError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"learning": view})
}

// DismissSetupLearning handles POST /api/personal-assistant/setup-learning/dismiss:
// every learning still waiting is turned away and does not come back.
func (h *Handler) DismissSetupLearning(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	userID, ok := h.setupLearningReady(w, r)
	if !ok {
		return
	}
	var req secondBrainRequestOnly
	if !decodeClosedBody(w, r, maxSecondBrainBodyBytes, &req, "request_id") {
		return
	}
	dismissed, err := h.setupLearning.Dismiss(r.Context(), userID, strings.TrimSpace(req.RequestID))
	if err != nil {
		writeSetupLearningError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"dismissed": dismissed})
}

func writeSetupLearningError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalassistant.ErrValidation):
		orihttp.BadRequest(w, "The request is not valid")
	case errors.Is(err, personalassistant.ErrKnowledgePaused):
		orihttp.Conflict(w, "Suggestions are paused")
	default:
		orihttp.ServiceUnavailable(w, "What Ori learned is unavailable")
	}
}
