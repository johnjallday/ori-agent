package sessionhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// unsatisfiedRequiredPlugins reports the template-declared plugins that are not
// installed (missing) and those installed but globally disabled (disabled).
//
// Product decision (overrides the earlier file-only-always default): a template
// that declares plugins requires them installed AND enabled before a workspace
// can be created, so the workspace never starts missing its required tools. The
// create flow blocks on any missing/disabled required plugin. Reads through the
// same plugin manager the Plugins API uses. Fails open (returns nothing) when
// the plugin manager is unavailable or its store can't be read, so a transient
// read error never permanently blocks creation.
func (h *Handler) unsatisfiedRequiredPlugins(tools projecttemplates.ToolDefaults) (missing, disabled []string) {
	if len(tools.Plugins) == 0 || h.reaperPluginLister == nil {
		return nil, nil
	}
	installed, err := h.reaperPluginLister.List()
	if err != nil {
		return nil, nil
	}
	enabledByName := make(map[string]bool, len(installed))
	present := make(map[string]bool, len(installed))
	for _, p := range installed {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		present[key] = true
		enabledByName[key] = p.Enabled
	}
	for _, name := range tools.Plugins {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		switch {
		case !present[key]:
			missing = append(missing, name)
		case !enabledByName[key]:
			disabled = append(disabled, name)
		}
	}
	return missing, disabled
}

// handleReaperReadiness serves GET /api/workspaces/{id}/reaper-setup: the one
// normalized REAPER readiness result reused by the workspace UI, repair, and
// setup auto-start decisions. Each request recomputes from live plugin, binding,
// task, provider, and permission state — it never returns a cached ready result.
func (h *Handler) handleReaperReadiness(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	if !h.reaperWorkspaceOwned(workspaceID) {
		orihttp.NotFound(w, "workspace not found")
		return
	}
	readiness := reapersetup.Readiness{LiveVerification: "not_checked", ProjectMode: "file_only"}
	if h.reaperResolver != nil {
		resolved, err := h.reaperResolver.Resolve(workspaceID)
		if err != nil {
			orihttp.InternalError(w, "REAPER setup could not be checked")
			return
		}
		readiness = resolved
	}
	if h.reaperRuntime != nil {
		status, err := h.reaperRuntime.Status(r.Context(), workspaceID)
		if err == nil && status.Applicable {
			readiness.Runtime = &status
		}
	}
	orihttp.WriteJSON(w, readiness)
}

// handleReaperRepair serves the existing-workspace repair endpoint at
// /api/workspaces/{id}/reaper-setup/repair. GET previews the changes; POST
// applies them (body {"confirm_enable": bool} to permit enabling a disabled
// plugin). Repair only runs for conservatively identified REAPER workspaces and
// never enables native access, mutates an agent, creates a task, or touches an
// .rpp file. After a mutation, the response carries the refreshed readiness
// status so the UI updates in place.
func (h *Handler) handleReaperRepair(w http.ResponseWriter, r *http.Request, workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	if !h.reaperWorkspaceOwned(workspaceID) {
		orihttp.NotFound(w, "workspace not found")
		return
	}
	if h.reaperRepairer == nil {
		orihttp.WriteJSON(w, reapersetup.RepairPlan{})
		return
	}
	switch r.Method {
	case http.MethodGet:
		plan, err := h.reaperRepairer.Preview(workspaceID)
		if err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, plan)
	case http.MethodPost:
		var req struct {
			ConfirmEnable bool `json:"confirm_enable"`
		}
		if !decodeOptionalClosedReaperBody(w, r, &req) {
			return
		}
		result, err := h.reaperRepairer.Apply(workspaceID, req.ConfirmEnable)
		if err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, result)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleReaperRuntimeTransition preserves the legacy REAPER route while newer
// callers migrate to /runtime-capabilities. It accepts no caller-supplied path,
// port, command, script, or project and delegates to the same compiled runtime
// service as the generalized endpoint.
func (h *Handler) handleReaperRuntimeTransition(w http.ResponseWriter, r *http.Request, workspaceID string, verify bool) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	if !decodeOptionalClosedReaperBody(w, r, &struct{}{}) {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || !h.reaperWorkspaceOwned(workspaceID) {
		orihttp.NotFound(w, "workspace not found")
		return
	}
	if h.reaperRuntime == nil {
		orihttp.ServiceUnavailable(w, "REAPER runtime checks are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var (
		status runtimecapability.Status
		err    error
	)
	if verify {
		status, err = h.reaperRuntime.Verify(ctx, workspaceID, reapersetup.ReaperLiveControlCapability)
	} else {
		status, err = h.reaperRuntime.Recheck(ctx, workspaceID)
	}
	if err != nil {
		orihttp.Conflict(w, "REAPER runtime check did not complete. Check the current setup status and try its next action.")
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true, "runtime": status})
}

func (h *Handler) reaperWorkspaceOwned(workspaceID string) bool {
	if h == nil || h.workspaceTaskStore == nil {
		return false
	}
	ws, err := h.workspaceTaskStore.Get(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return false
	}
	owner := strings.TrimSpace(ws.OwnerUserID)
	return owner == "" || strings.EqualFold(owner, userprofile.LocalUserID)
}

func decodeOptionalClosedReaperBody(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return false
	}
	if strings.TrimSpace(string(body)) == "" {
		return true
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		orihttp.BadRequest(w, "invalid request body")
		return false
	}
	return true
}

// GetReaperCreatePreview serves GET /api/reaper-setup/preview: the pre-create
// REAPER Setup card state for the Reaper Song template, computed from the same
// plugin store the readiness resolver reads. There is no workspace yet, so it
// reports plugin install/enable/would-attach only; agent and native-access
// decisions happen in-workspace after creation.
func (h *Handler) GetReaperCreatePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	preview, err := reapersetup.PreviewCreate(h.reaperPluginLister)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, preview)
}
