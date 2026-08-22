package reaperhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxScriptRequestBody = (1 << 20) + (16 << 10)

// ScriptListResponse augments the raw script catalog with this workspace's
// pin state so the console can render the pinned quick-action band and the
// full library without a second round trip (task 1.5). PinnedScriptIDs is
// filtered to IDs that still resolve in scriptLibrary.List() — this is the
// read-time prune described in workspace.ReaperPinService's doc comment: a
// pin whose script was deleted from the shared library is simply skipped
// here, not removed from the persisted workspace record.
type ScriptListResponse struct {
	Scripts         []reaper.Script `json:"scripts"`
	PinnedScriptIDs []string        `json:"pinned_script_ids"`
}

func (h *Handler) ListScripts(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveScriptWorkspace(w, r)
	if !ok {
		return
	}
	scripts, err := h.scriptLibrary.List()
	if err != nil {
		h.respondScriptError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, ScriptListResponse{
		Scripts:         scripts,
		PinnedScriptIDs: prunePinnedScriptIDs(ws.PinnedReaperScripts, scripts),
	})
}

// prunePinnedScriptIDs drops any pinned ID that no longer resolves to a
// script in the live library, preserving the pinned order otherwise.
func prunePinnedScriptIDs(pinned []string, scripts []reaper.Script) []string {
	if len(pinned) == 0 {
		return []string{}
	}
	live := make(map[string]bool, len(scripts))
	for _, script := range scripts {
		live[script.ID] = true
	}
	out := make([]string, 0, len(pinned))
	for _, id := range pinned {
		if live[id] {
			out = append(out, id)
		}
	}
	return out
}

func (h *Handler) GetScript(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolveScriptWorkspace(w, r); !ok {
		return
	}
	script, err := h.scriptLibrary.Read(r.PathValue("scriptID"))
	if err != nil {
		h.respondScriptError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, script)
}

func (h *Handler) CreateScript(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolveScriptWorkspace(w, r); !ok {
		return
	}
	input, ok := decodeScriptInput(w, r)
	if !ok {
		return
	}
	script, err := h.scriptLibrary.Create(input)
	if err != nil {
		h.respondScriptError(w, err)
		return
	}
	_ = orihttp.RespondCreated(w, script)
}

func (h *Handler) UpdateScript(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolveScriptWorkspace(w, r); !ok {
		return
	}
	input, ok := decodeScriptInput(w, r)
	if !ok {
		return
	}
	script, err := h.scriptLibrary.Update(r.PathValue("scriptID"), input)
	if err != nil {
		h.respondScriptError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, script)
}

func (h *Handler) DeleteScript(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolveScriptWorkspace(w, r); !ok {
		return
	}
	if err := h.scriptLibrary.Delete(r.PathValue("scriptID")); err != nil {
		h.respondScriptError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"deleted": true})
}

// PinScript marks a script as a pinned quick action for this workspace.
func (h *Handler) PinScript(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveScriptWorkspace(w, r)
	if !ok {
		return
	}
	if h.pins == nil {
		h.respondUnavailable(w)
		return
	}
	scriptID := r.PathValue("scriptID")
	if _, err := h.scriptLibrary.Read(scriptID); err != nil {
		h.respondScriptError(w, err)
		return
	}
	if err := h.pins.Pin(ws.ID, scriptID); err != nil {
		h.respondPinError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"pinned": true})
}

// UnpinScript removes a script from this workspace's pinned quick actions.
func (h *Handler) UnpinScript(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveScriptWorkspace(w, r)
	if !ok {
		return
	}
	if h.pins == nil {
		h.respondUnavailable(w)
		return
	}
	if err := h.pins.Unpin(ws.ID, r.PathValue("scriptID")); err != nil {
		h.respondPinError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"pinned": false})
}

type reorderPinnedScriptsRequest struct {
	OrderedScriptIDs []string `json:"ordered_script_ids"`
}

// ReorderPinnedScripts persists a new drag-to-reorder order for this
// workspace's pinned quick actions.
func (h *Handler) ReorderPinnedScripts(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveScriptWorkspace(w, r)
	if !ok {
		return
	}
	if h.pins == nil {
		h.respondUnavailable(w)
		return
	}
	var input reorderPinnedScriptsRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxScriptRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid reorder request")
		return
	}
	if err := h.pins.Reorder(ws.ID, slices.Clone(input.OrderedScriptIDs)); err != nil {
		h.respondPinError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"reordered": true})
}

func (h *Handler) respondPinError(w http.ResponseWriter, err error) {
	// Reorder's membership/duplicate guard (workspace.ReaperPinService.Reorder)
	// is the only source of errors here beyond store unavailability, and it is
	// always a client-supplied stale/incorrect order — 409, not 500.
	_ = orihttp.RespondAPIError(w, http.StatusConflict,
		orihttp.NewAPIError("reaper_pin_conflict", err.Error()))
}

func (h *Handler) resolveScriptWorkspace(w http.ResponseWriter, r *http.Request) (*workspace.Workspace, bool) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return nil, false
	}
	if h.scriptLibrary == nil {
		h.respondUnavailable(w)
		return nil, false
	}
	_, applies, err := h.projectSource(ws.ID)
	if err != nil {
		h.respondUnavailable(w)
		return nil, false
	}
	if !applies {
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("reaper_not_selected", "Live REAPER control is not selected for this workspace."))
		return nil, false
	}
	return ws, true
}

func decodeScriptInput(w http.ResponseWriter, r *http.Request) (reaper.ScriptInput, bool) {
	var input reaper.ScriptInput
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxScriptRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid REAPER script")
		return reaper.ScriptInput{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		_ = orihttp.RespondBadRequest(w, "invalid REAPER script")
		return reaper.ScriptInput{}, false
	}
	return input, true
}

func (h *Handler) respondScriptError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "reaper_script_unavailable"
	message := "The REAPER script library is unavailable."
	switch {
	case errors.Is(err, reaper.ErrScriptInvalid):
		status = http.StatusBadRequest
		code = "invalid_reaper_script"
		message = "The REAPER script or filename is invalid."
	case errors.Is(err, reaper.ErrScriptExists):
		status = http.StatusConflict
		code = "reaper_script_exists"
		message = "A REAPER script with that filename already exists."
	case errors.Is(err, reaper.ErrScriptNotFound):
		status = http.StatusNotFound
		code = "reaper_script_not_found"
		message = "The REAPER script was not found."
	case errors.Is(err, reaper.ErrLibraryUnsafe):
		status = http.StatusServiceUnavailable
		code = "reaper_script_library_unsafe"
		message = "The REAPER script library path is not safe to use."
	}
	_ = orihttp.RespondAPIError(w, status, orihttp.NewAPIError(code, strings.TrimSpace(message)))
}
