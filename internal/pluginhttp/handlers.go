package pluginhttp

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// ReviewedUpdate is the host's answer for one installed plugin's Update. It has
// three outcomes, and only the zero value is the ordinary one:
//
//   - zero value: the host has no opinion. The plugin follows its recorded
//     source, as every unreviewed plugin does. An exact-commit install with
//     nothing newer also answers this way: its recorded source is an immutable
//     commit, so following it is harmless.
//   - Source (and Format) set: replace from exactly that source.
//   - Refuse set: the plugin is reviewed and its recorded source is mutable, so
//     following it would install an unreviewed branch head. The update is
//     refused. Refuse wins over Source, and nothing may read the recorded source.
type ReviewedUpdate struct {
	Source string
	Format plugin.SourceFormat
	Refuse bool
}

// ReviewedReplacement maps an installed plugin to the host's ReviewedUpdate.
type ReviewedReplacement func(ctx context.Context, installed plugin.InstalledPlugin) ReviewedUpdate

// ReviewedReleaseCurrentCode is the stable error code of a refused update.
const ReviewedReleaseCurrentCode = "reviewed_release_current"

// Handler serves plugin operations and owns the plugin Manager wired to Ori's
// live MCP config/registry.
type Handler struct {
	mgr     *plugin.Manager
	updates *plugin.UpdateChecker
	list    workspaceList

	replacementMu       sync.RWMutex
	reviewedReplacement ReviewedReplacement
}

// NewHandler builds the plugin manager over Ori's MCP config manager + runtime
// registry and returns an HTTP handler for it. pluginsDir is the managed
// directory for the installed-plugins registry and git clones.
func NewHandler(config *mcp.ConfigManager, registry *mcp.Registry, pluginsDir string) *Handler {
	mgr := plugin.NewManager(
		newMCPRegistrar(config, registry),
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

// skillNameTakenMessage turns a refused skill name into the sentence the user
// sees, naming the clashing skill: `A skill named "x" is already ...`.
func skillNameTakenMessage(err error) string {
	detail := strings.TrimPrefix(err.Error(), plugin.ErrSkillNameTaken.Error()+": ")
	if detail == err.Error() || detail == "" {
		return "One of this plugin's skills has the same name as a skill you already have."
	}
	return strings.ToUpper(detail[:1]) + detail[1:] + "."
}

func respondPluginMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugin.ErrSkillNameTaken):
		orihttp.Conflict(w, skillNameTakenMessage(err))
	case errors.Is(err, plugin.ErrSourceChanged):
		orihttp.Conflict(w, "The plugin source changed while it was being installed; try again")
	default:
		orihttp.InternalError(w, err.Error())
	}
}

// SetReviewedReplacement installs the hook UpdateHandler consults before
// following a plugin's recorded source. A nil hook restores recorded-source
// updates only.
func (h *Handler) SetReviewedReplacement(hook ReviewedReplacement) {
	h.replacementMu.Lock()
	defer h.replacementMu.Unlock()
	h.reviewedReplacement = hook
}

// replacementFor reports the host's ReviewedUpdate for one installed plugin. A
// missing hook or plugin, or a replacement with no source, is the zero value.
func (h *Handler) replacementFor(ctx context.Context, name string) (ReviewedUpdate, error) {
	h.replacementMu.RLock()
	hook := h.reviewedReplacement
	h.replacementMu.RUnlock()
	if hook == nil {
		return ReviewedUpdate{}, nil
	}
	installed, err := h.mgr.List()
	if err != nil {
		return ReviewedUpdate{}, err
	}
	for _, candidate := range installed {
		if candidate.Name == name {
			update := hook(ctx, candidate)
			if update.Refuse {
				return ReviewedUpdate{Refuse: true}, nil
			}
			if update.Source == "" {
				return ReviewedUpdate{}, nil
			}
			return update, nil
		}
	}
	return ReviewedUpdate{}, nil
}

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
		respondPluginMutationError(w, err)
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
		respondPluginMutationError(w, err)
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
		respondPluginMutationError(w, err)
		return
	}
	h.updates.Invalidate(installed.Name)
	orihttp.WriteJSON(w, map[string]any{"installed": true, "plugin": installed})
}

// UpdateHandler updates a plugin at POST /api/plugins/{name}/update, from the
// host's reviewed replacement source when one is offered and otherwise from its
// recorded source. confirm=false returns the trust disclosure plus whether the
// registered component set changed; confirm=true updates. A plugin the host
// refuses to update (a reviewed install recorded against a mutable source with
// no newer reviewed release it can offer) answers 409 reviewed_release_current for both, before
// any source is read, and its cached availability is left as it was.
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
		// FromList switches the plugin to the version the Workspace
		// Directory's plugin list names. The source is read here, never
		// taken from the request, and goes through the same review.
		FromList bool `json:"from_list,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	var replacement ReviewedUpdate
	var err error
	if req.FromList {
		entry, listed := h.listedEntry(name)
		if !listed {
			orihttp.Conflict(w, "The plugin list no longer lists this plugin.")
			return
		}
		replacement = ReviewedUpdate{Source: entry.Source, Format: entry.Format}
	} else if replacement, err = h.replacementFor(r.Context(), name); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	if replacement.Refuse {
		if err := orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError(
			ReviewedReleaseCurrentCode,
			"No newer reviewed release is available to install. Ori does not update this plugin from its development branch.",
		)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	reviewed := replacement.Source != ""
	source, format := replacement.Source, replacement.Format
	if !req.Confirm {
		var report plugin.TrustReport
		var changed bool
		if reviewed {
			report, changed, err = h.mgr.PreviewReplacement(name, source, format)
		} else {
			report, changed, err = h.mgr.UpdatePreview(name)
		}
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		response := map[string]any{"updated": false, "changed": changed, "trust": report}
		if req.FromList {
			// The review of a switch names exactly what it would install.
			response["source"] = source
		}
		orihttp.WriteJSON(w, response)
		return
	}
	confirm := func(plugin.TrustReport) bool { return true }
	var updated plugin.InstalledPlugin
	if reviewed {
		updated, err = h.mgr.UpdateFromSource(name, source, format, confirm)
	} else {
		updated, err = h.mgr.Update(name, confirm)
	}
	if err != nil {
		respondPluginMutationError(w, err)
		return
	}
	h.updates.Invalidate(name)
	if updated.Name != name {
		h.updates.Invalidate(updated.Name)
	}
	orihttp.WriteJSON(w, map[string]any{"updated": true, "plugin": updated})
}
