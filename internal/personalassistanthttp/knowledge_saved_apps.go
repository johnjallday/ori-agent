package personalassistanthttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// SavedAppSuggestions is a server-owned, local-only source adapter. The
// browser supplies no evidence, app names, owner/HQ ID or candidate wording.
type SavedAppSuggestions interface {
	Check(ctx context.Context, userID string) ([]personalassistant.KnowledgeItem, error)
}

func (h *Handler) SetSavedAppSuggestions(source SavedAppSuggestions) {
	if h != nil {
		h.savedAppSuggestions = source
	}
}

func (h *Handler) SetJanitorSuggestions(source SavedAppSuggestions) {
	if h != nil {
		h.janitorSuggestions = source
	}
}

// CheckSavedAppSuggestions explicitly reconciles existing saved onboarding
// evidence. A GET never invokes this method; there is no detector/rescan or
// browser-defined proposal endpoint.
func (h *Handler) CheckSavedAppSuggestions(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		orihttp.ServiceUnavailable(w, "Saved app suggestions are unavailable")
		return
	}
	h.checkKnowledgeSuggestions(w, r, h.savedAppSuggestions, "Saved app")
}

// CheckJanitorSuggestions reconciles only previously approved File Janitor
// journal actions in the user's existing Daily Brief scope. It cannot apply,
// undo, scan, or classify a file and accepts no caller-supplied evidence.
func (h *Handler) CheckJanitorSuggestions(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		orihttp.ServiceUnavailable(w, "File Janitor suggestions are unavailable")
		return
	}
	h.checkKnowledgeSuggestions(w, r, h.janitorSuggestions, "File Janitor")
}

type admittedKnowledgeCandidate struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

func (h *Handler) checkKnowledgeSuggestions(w http.ResponseWriter, r *http.Request, source SavedAppSuggestions, label string) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if source == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, label+" suggestions are unavailable")
		return
	}
	if r.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32))
		if err != nil || strings.TrimSpace(string(body)) != "" {
			orihttp.BadRequest(w, "This action does not accept source data")
			return
		}
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	items, err := source.Check(r.Context(), userID)
	// Source checks do not serialize sidecar metadata, past revisions, hashes
	// or raw evidence. The bounded dossier GET separately projects the current
	// canonical value and safe provenance if the user wants to review it.
	candidates := make([]admittedKnowledgeCandidate, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, admittedKnowledgeCandidate{ID: item.ID, State: string(item.State)})
	}
	switch {
	case err == nil:
		orihttp.Success(w, map[string]any{"candidates": candidates})
	case errors.Is(err, personalassistant.ErrKnowledgeQuota):
		_ = orihttp.RespondJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":      "The review queue is full. Try again later or review existing suggestions.",
			"candidates": candidates,
		})
	case errors.Is(err, personalassistant.ErrKnowledgePaused):
		orihttp.Conflict(w, "Automatic suggestions are paused")
	case errors.Is(err, personalassistant.ErrNeedsHQ):
		orihttp.Conflict(w, "Build Personal HQ before checking suggestions")
	case errors.Is(err, personalassistant.ErrRepairNeeded), errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalassistant.ErrNotFound):
		orihttp.Conflict(w, "The assistant relationship changed. Refresh before checking suggestions")
	default:
		orihttp.ServiceUnavailable(w, label+" suggestions are temporarily unavailable")
	}
}
