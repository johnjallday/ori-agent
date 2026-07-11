package sessionhttp

import (
	"encoding/json"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
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
	if h.reaperResolver == nil {
		// Plugins unavailable: report an unidentified, file-only-safe result so the
		// UI simply renders no REAPER surface rather than erroring.
		orihttp.WriteJSON(w, reapersetup.Readiness{LiveVerification: "not_checked", ProjectMode: "file_only"})
		return
	}
	readiness, err := h.reaperResolver.Resolve(workspaceID)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
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
		// Body is optional; decode leniently and default to no-confirm without
		// erroring on an empty/absent body.
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
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
