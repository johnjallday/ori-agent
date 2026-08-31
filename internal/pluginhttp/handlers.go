package pluginhttp

import (
	"net/http"
	"path/filepath"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// Handler serves plugin operations and owns the plugin Manager wired to Ori's
// live MCP config/registry and skills directory.
type Handler struct {
	mgr     *plugin.Manager
	updates *plugin.UpdateChecker
}

// NewHandler builds the plugin manager over Ori's MCP config manager + runtime
// registry and the given skills directory, and returns an HTTP handler for it.
// pluginsDir is the managed directory for the installed-plugins registry and
// git clones.
func NewHandler(config *mcp.ConfigManager, registry *mcp.Registry, skillsDir, pluginsDir string) *Handler {
	mgr := plugin.NewManager(
		newMCPRegistrar(config, registry),
		newSkillDirInstaller(skillsDir),
		pluginsDir,
		filepath.Join(pluginsDir, "src"),
	)
	return newHandlerWithManager(mgr)
}

// newHandlerWithManager wraps an existing manager (used by tests).
func newHandlerWithManager(mgr *plugin.Manager) *Handler {
	return &Handler{mgr: mgr, updates: plugin.NewUpdateChecker(mgr)}
}

// Manager returns the underlying plugin manager so other subsystems (template
// application and runtime providers inspect installed plugins and reconcile
// workspace bindings through the same configured store the Plugins API uses,
// rather than a separate hard-coded plugins/ lookup.
func (h *Handler) Manager() *plugin.Manager { return h.mgr }

// UpdateChecker returns the process-local checker owned by this handler. Server
// lifecycle code starts and stops it; direct handler construction stays idle.
func (h *Handler) UpdateChecker() *plugin.UpdateChecker { return h.updates }

// UpdateStatusHandler returns only the cached availability snapshot. It never
// resolves plugin sources or performs Git/filesystem I/O.
func (h *Handler) UpdateStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}
	orihttp.WriteJSON(w, h.updates.Snapshot())
}

// ListHandler handles GET /api/plugins.
func (h *Handler) ListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}
	list, err := h.mgr.List()
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, map[string]any{"plugins": list})
}

// InstallHandler handles POST /api/plugins/install. With confirm=false it
// returns the trust report (a no-op disclosure preview); with confirm=true it
// performs the install.
func (h *Handler) InstallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req struct {
		Source  string `json:"source"`
		Format  string `json:"format"`
		Confirm bool   `json:"confirm"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.Source == "" {
		orihttp.BadRequest(w, "source is required")
		return
	}
	prefer := plugin.SourceFormat(req.Format)

	if !req.Confirm {
		report, err := h.mgr.Preview(req.Source, prefer)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, map[string]any{"installed": false, "trust": report})
		return
	}

	installed, err := h.mgr.Install(req.Source, prefer, func(plugin.TrustReport) bool { return true })
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	h.updates.Invalidate(installed.Name)
	orihttp.WriteJSON(w, map[string]any{"installed": true, "plugin": installed})
}

// UninstallHandler handles DELETE /api/plugins/{name}.
func (h *Handler) UninstallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		orihttp.BadRequest(w, "plugin name is required")
		return
	}
	if err := h.mgr.Uninstall(name); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	h.updates.Invalidate(name)
	orihttp.WriteJSON(w, map[string]any{"uninstalled": name})
}

// SetEnabledHandler returns a handler that enables or disables a plugin
// (POST /api/plugins/{name}/enable and .../disable).
func (h *Handler) SetEnabledHandler(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			orihttp.MethodNotAllowed(w)
			return
		}
		name := r.PathValue("name")
		if name == "" {
			orihttp.BadRequest(w, "plugin name is required")
			return
		}
		if err := h.mgr.SetEnabled(name, enabled); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, map[string]any{"name": name, "enabled": enabled})
	}
}

// MarketplacesHandler lists (GET) or adds (POST) marketplaces at
// /api/plugins/marketplaces.
func (h *Handler) MarketplacesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := h.mgr.Marketplaces()
		if err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}
		added := false
		for _, mp := range list {
			if mp.Name == plugin.OfficialMarketplaceName || mp.Source == plugin.OfficialMarketplaceSource {
				added = true
				break
			}
		}
		orihttp.WriteJSON(w, map[string]any{
			"marketplaces": list,
			// official tells the UI the backend-held source for the one-click
			// "Add official marketplace" button, and whether it's already added.
			"official": map[string]any{
				"name":   plugin.OfficialMarketplaceName,
				"source": plugin.OfficialMarketplaceSource,
				"added":  added,
			},
		})
	case http.MethodPost:
		var req struct {
			Source string `json:"source"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
		if req.Source == "" {
			orihttp.BadRequest(w, "source is required")
			return
		}
		mp, err := h.mgr.AddMarketplace(req.Source)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, map[string]any{"marketplace": mp})
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// MarketplaceInstallHandler installs a plugin from an added marketplace at
// POST /api/plugins/marketplaces/install. confirm=false returns the trust
// disclosure; confirm=true installs.
func (h *Handler) MarketplaceInstallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req struct {
		Marketplace string `json:"marketplace"`
		Plugin      string `json:"plugin"`
		Format      string `json:"format"`
		Confirm     bool   `json:"confirm"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.Marketplace == "" || req.Plugin == "" {
		orihttp.BadRequest(w, "marketplace and plugin are required")
		return
	}
	prefer := plugin.SourceFormat(req.Format)
	if !req.Confirm {
		report, err := h.mgr.PreviewFromMarketplace(req.Marketplace, req.Plugin, prefer)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, map[string]any{"installed": false, "trust": report})
		return
	}
	installed, err := h.mgr.InstallFromMarketplace(req.Marketplace, req.Plugin, prefer, func(plugin.TrustReport) bool { return true })
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	h.updates.Invalidate(installed.Name)
	orihttp.WriteJSON(w, map[string]any{"installed": true, "plugin": installed})
}

// UpdateHandler updates a plugin from its recorded source at
// POST /api/plugins/{name}/update. confirm=false returns the trust disclosure
// plus whether the registered component set changed; confirm=true updates.
func (h *Handler) UpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		orihttp.BadRequest(w, "plugin name is required")
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !req.Confirm {
		report, changed, err := h.mgr.UpdatePreview(name)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, map[string]any{"updated": false, "changed": changed, "trust": report})
		return
	}
	updated, err := h.mgr.Update(name, func(plugin.TrustReport) bool { return true })
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	h.updates.Invalidate(name)
	if updated.Name != name {
		h.updates.Invalidate(updated.Name)
	}
	orihttp.WriteJSON(w, map[string]any{"updated": true, "plugin": updated})
}
