package sessionhttp

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

type libraryArrangementRequest struct {
	Goal                 string `json:"goal"`
	ExpectedRevision     int64  `json:"expected_revision"`
	ShareCatalogMetadata bool   `json:"share_catalog_metadata"`
	AllowUpgrade         bool   `json:"allow_upgrade"`
	ExpectedContext      string `json:"expected_context,omitempty"`
}

func (r *libraryArrangementRequest) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return workspacearrangement.ErrInvalid
	}
	var out libraryArrangementRequest
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return workspacearrangement.ErrInvalid
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return workspacearrangement.ErrInvalid
		}
		seen[name] = true
		value, err := decoder.Token()
		if err != nil {
			return workspacearrangement.ErrInvalid
		}
		switch name {
		case "goal":
			out.Goal, ok = value.(string)
		case "expected_revision":
			var number json.Number
			number, ok = value.(json.Number)
			if ok {
				out.ExpectedRevision, err = number.Int64()
				ok = err == nil
			}
		case "expected_context":
			out.ExpectedContext, ok = value.(string)
			if ok {
				_, decodeErr := hex.DecodeString(out.ExpectedContext)
				ok = len(out.ExpectedContext) == 64 && decodeErr == nil
			}
		case "share_catalog_metadata":
			out.ShareCatalogMetadata, ok = value.(bool)
		case "allow_upgrade":
			out.AllowUpgrade, ok = value.(bool)
		default:
			ok = false
		}
		if !ok {
			return workspacearrangement.ErrInvalid
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return workspacearrangement.ErrInvalid
	}
	*r = out
	return nil
}

func (h *Handler) SetArrangementPlanning(run func(context.Context, projectlibrary.ArrangementPlanningRequest) (projectlibrary.ArrangementPlanningResult, error)) {
	h.arrangementPlanning = run
}

func (h *Handler) SetArrangementContext(read func(context.Context, string, string) (projectlibrary.ArrangementPlanningContext, error)) {
	h.arrangementContext = read
}

// GetAssistantLibraryArrangementContext discloses the current exact Manager
// runtime without a model call, catalog initialization or storage migration.
func (h *Handler) GetAssistantLibraryArrangementContext(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid arrangement context request")
		return
	}
	if h.arrangementContext == nil {
		writeArrangementFailure(w, projectlibrary.ErrUnavailable, true)
		return
	}
	view, err := h.arrangementContext(r.Context(), scope.OwnerUserID, scope.HomeID)
	if err != nil {
		writeArrangementFailure(w, err, true)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

// PlanAssistantLibraryArrangement is explicit metadata sharing, not activation,
// discovery or a scan-completed event. Organize library sends explicit choices.
func (h *Handler) PlanAssistantLibraryArrangement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid arrangement request")
		return
	}
	var in libraryArrangementRequest
	if !orihttp.ParseJSONBody(w, r, &in) {
		return
	}
	if !in.ShareCatalogMetadata || !workspacearrangement.ValidGoal(in.Goal) || in.ExpectedRevision < 1 {
		_ = orihttp.RespondBadRequest(w, "A current catalog revision, organizing goal and explicit metadata-sharing consent are required")
		return
	}
	if h.arrangementPlanning == nil {
		writeArrangementPlanningFailure(w, projectlibrary.ErrUnavailable)
		return
	}
	result, err := h.arrangementPlanning(r.Context(), projectlibrary.ArrangementPlanningRequest{Owner: scope.OwnerUserID, HomeID: scope.HomeID, Goal: in.Goal, ExpectedRevision: in.ExpectedRevision, ShareCatalogMetadata: in.ShareCatalogMetadata, AllowUpgrade: in.AllowUpgrade, ExpectedContext: in.ExpectedContext})
	if err != nil {
		writeArrangementPlanningFailure(w, err)
		return
	}
	if result.History == nil {
		writeArrangementPlanningFailure(w, projectlibrary.ErrUnavailable)
		return
	}
	_ = orihttp.RespondSuccess(w, result.History)
}

func writeArrangementPlanningFailure(w http.ResponseWriter, err error) {
	writeArrangementFailure(w, err, false)
}

func writeArrangementFailure(w http.ResponseWriter, err error, readOnly bool) {
	status, code, message := http.StatusConflict, "unavailable", "Home planning is unavailable. Keep your goal and use the existing manual setup."
	switch {
	case errors.Is(err, projectlibrary.ErrArrangementUpgradeRequired):
		code, message = "upgrade_required", "Saving this planning turn upgrades the Home library; older Ori versions cannot open it. Explicit upgrade consent is required."
	case errors.Is(err, projectlibrary.ErrNoManagerModel):
		code, message = "model_unavailable", "Choose a supported API model for this Home's bound Manager; no provider was switched."
	case errors.Is(err, workspacearrangement.ErrBusy):
		code, message = "busy", "A planning turn is already running. Wait or cancel it."
	case errors.Is(err, workspacearrangement.ErrStale), errors.Is(err, projectlibrary.ErrConflict):
		code, message = "stale_context", "The Home, catalog or runtime changed. Reopen history and request a fresh plan."
	case errors.Is(err, context.Canceled):
		code, message = "cancelled", "Planning was cancelled. Reopen saved history before retrying."
	case errors.Is(err, context.DeadlineExceeded):
		code, message = "timed_out", "Planning timed out. Reopen saved history before retrying with a narrower goal."
	case errors.Is(err, workspacearrangement.ErrLimit), errors.Is(err, projectlibrary.ErrLimit):
		code, message = "limit_reached", "A planning or storage limit was reached. Keep your goal and narrow the request."
	case errors.Is(err, workspacearrangement.ErrInvalid), errors.Is(err, projectlibrary.ErrCorrupt):
		code, message = "invalid_result", "The planning result could not be accepted. No unsaved proposal is offered."
	default:
		if !errors.Is(err, projectlibrary.ErrUnavailable) && !errors.Is(err, projectlibrary.ErrNotInitialized) {
			status, code, message = http.StatusServiceUnavailable, "planning_failed", "Planning or its canonical save failed. Reopen history before retrying; no unsaved proposal is offered."
		}
	}
	if readOnly {
		message = "The current Home planning context could not be checked. No model was called, library initialized or upgraded, or planning record saved."
	} else {
		message += " No arrangement was applied and no source files were changed."
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	orihttp.WriteJSON(w, map[string]any{"arrangement_error": map[string]string{"code": code, "message": message}})
}
