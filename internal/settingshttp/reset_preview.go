package settingshttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
)

// SetPreviewPlanner attaches runtime owners during construction, before serving
// requests. The same planner must be used by the staged execution coordinator.
func (h *ResetHandler) SetPreviewPlanner(planner *settingsreset.Planner) {
	h.planner = planner
}

func (h *ResetHandler) GetResetPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	intent, categories, err := resetPreviewSelection(r.URL.RawQuery)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	if h.planner == nil {
		_ = orihttp.RespondServiceUnavailable(w, "Reset preview unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	preview, err := h.planner.Create(ctx, intent, categories)
	if err != nil {
		if errors.Is(err, settingsreset.ErrPreviewLimit) {
			// No preview token is evicted early to make room for another tab.
			w.Header().Set("Retry-After", "600")
			_ = orihttp.RespondError(w, http.StatusTooManyRequests, "Reset preview capacity or size limit reached; wait or reduce the scope")
		} else if errors.Is(err, settingsreset.ErrInvalidSelection) {
			_ = orihttp.RespondBadRequest(w, err.Error())
		} else {
			_ = orihttp.RespondServiceUnavailable(w, "Reset preview unavailable; no reset was performed")
		}
		return
	}
	orihttp.WriteJSON(w, preview)
}

// New callers use intent=selected_data&category=agents&category=app_records.
// Old boolean options normalize only to their original families. Mixing forms,
// duplicate parameters, unknown keys and arbitrary target paths is rejected.
func resetPreviewSelection(raw string) (settingsreset.Intent, []settingsreset.CategoryID, error) {
	if len(raw) > maxResetRequestSize {
		return "", nil, settingsreset.ErrInvalidSelection
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return "", nil, settingsreset.ErrInvalidSelection
	}
	legacy := map[string]settingsreset.CategoryID{
		"settings": settingsreset.CategorySettings, "agents": settingsreset.CategoryAgents,
		"sessions": settingsreset.CategoryAppRecords, "onboarding": settingsreset.CategorySetupSteps,
	}
	intent := settingsreset.Intent(query.Get("intent"))
	var categories []settingsreset.CategoryID
	hasLegacy := false
	for key, values := range query {
		switch key {
		case "intent":
			if len(values) != 1 {
				return "", nil, settingsreset.ErrInvalidSelection
			}
		case "category":
			for _, value := range values {
				categories = append(categories, settingsreset.CategoryID(value))
			}
		default:
			id, ok := legacy[key]
			if !ok || len(values) != 1 || (values[0] != "true" && values[0] != "false") {
				return "", nil, settingsreset.ErrInvalidSelection
			}
			hasLegacy = true
			if values[0] == "true" {
				categories = append(categories, id)
			}
		}
	}
	if hasLegacy {
		if query.Has("category") {
			return "", nil, settingsreset.ErrInvalidSelection
		}
		if !query.Has("intent") {
			intent = settingsreset.IntentSelectedData
		}
	}
	selected, err := settingsreset.Selection(intent, categories)
	if err == nil && intent == settingsreset.IntentStartFresh {
		// The planner expands this server-owned intent. Passing the canonical list
		// back as if it came from the client would intentionally be rejected.
		return intent, nil, nil
	}
	return intent, selected, err
}
