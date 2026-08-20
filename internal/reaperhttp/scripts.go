package reaperhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/reaper"
)

const maxScriptRequestBody = (1 << 20) + (16 << 10)

func (h *Handler) ListScripts(w http.ResponseWriter, r *http.Request) {
	if !h.resolveScriptWorkspace(w, r) {
		return
	}
	scripts, err := h.scriptLibrary.List()
	if err != nil {
		h.respondScriptError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, scripts)
}

func (h *Handler) GetScript(w http.ResponseWriter, r *http.Request) {
	if !h.resolveScriptWorkspace(w, r) {
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
	if !h.resolveScriptWorkspace(w, r) {
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
	if !h.resolveScriptWorkspace(w, r) {
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
	if !h.resolveScriptWorkspace(w, r) {
		return
	}
	if err := h.scriptLibrary.Delete(r.PathValue("scriptID")); err != nil {
		h.respondScriptError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"deleted": true})
}

func (h *Handler) resolveScriptWorkspace(w http.ResponseWriter, r *http.Request) bool {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return false
	}
	if h.scriptLibrary == nil {
		h.respondUnavailable(w)
		return false
	}
	_, applies, err := h.projectSource(ws.ID)
	if err != nil {
		h.respondUnavailable(w)
		return false
	}
	if !applies {
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("reaper_not_selected", "Live REAPER control is not selected for this workspace."))
		return false
	}
	return true
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
