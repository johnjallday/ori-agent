package sessionhttp

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/homeupgrade"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The reviewed Home package upgrade addressed by plugin, for the Plugins
// page's Update. It is the Home page's upgrade over the same service and the
// same review, operation and error mapping. The plugin name in the path is
// only a lookup key: the owner is the current user, the package must be
// installed, and the plan covers every Home the package backs rather than one
// the request chose.

// pluginUpgradeScope resolves the current owner and the installed package the
// path names. It accepts the package only when the owner has a live Home
// pinned to it, so a package that is not installed, one no Home uses, and one
// only other owners' Homes use all answer the same 404.
func (h *Handler) pluginUpgradeScope(w http.ResponseWriter, r *http.Request) (string, plugin.InstalledPlugin, []*workspace.Workspace, bool) {
	unavailable := func() (string, plugin.InstalledPlugin, []*workspace.Workspace, bool) {
		_ = orihttp.RespondNotFound(w, "This plugin has no Home package upgrade")
		return "", plugin.InstalledPlugin{}, nil, false
	}
	failed := func() (string, plugin.InstalledPlugin, []*workspace.Workspace, bool) {
		respondProviderUpgradeError(w, homeupgrade.ErrUnavailable, nil)
		return "", plugin.InstalledPlugin{}, nil, false
	}
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil || h.homePackageUpgrades == nil || h.installedPluginLister == nil {
		return unavailable()
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		return unavailable()
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		return unavailable()
	}
	installed, err := h.installedPluginLister.List()
	if err != nil {
		return failed()
	}
	index := slices.IndexFunc(installed, func(candidate plugin.InstalledPlugin) bool { return candidate.Name == name })
	if index < 0 {
		return unavailable()
	}
	homes, err := h.ownerProviderHomes(owner, name)
	if err != nil {
		return failed()
	}
	if len(homes) == 0 {
		return unavailable()
	}
	return owner, installed[index], homes, true
}

// ownerProviderHomes lists the owner's live Homes pinned to the package, by
// name. A record that cannot be read is an error, never a skip.
func (h *Handler) ownerProviderHomes(owner, pluginID string) ([]*workspace.Workspace, error) {
	ids, err := h.workspaceTaskStore.List()
	if err != nil {
		return nil, err
	}
	var homes []*workspace.Workspace
	for _, id := range ids {
		candidate, err := h.workspaceTaskStore.Get(id)
		if err != nil {
			return nil, err
		}
		if candidate == nil || candidate.OwnerUserID != owner ||
			candidate.Status == workspace.StatusTrashed || candidate.Status == workspace.StatusMissing {
			continue
		}
		state := candidate.GetAssistantProgramState()
		if state == nil || state.HomeProvider == nil || state.HomeProvider.PluginID != pluginID ||
			state.Key.Normalize().OwnerUserID != owner {
			continue
		}
		homes = append(homes, candidate)
	}
	slices.SortFunc(homes, func(a, b *workspace.Workspace) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
	})
	return homes, nil
}

// GetPluginHomeUpgrade is GET /api/plugins/{name}/home-upgrade: the owner's
// Homes the package backs, whether a newer release is available, and the
// package's newest operation. Reading it can settle an interrupted operation,
// as every upgrade read does; it never starts one.
func (h *Handler) GetPluginHomeUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, installed, homes, ok := h.pluginUpgradeScope(w, r)
	if !ok {
		return
	}
	view := h.providerUpgradeStatus(r.Context(), owner, installed.Name, installed.Version)
	view.Homes = make([]providerUpgradeHomeLink, 0, len(homes))
	for _, home := range homes {
		view.Homes = append(view.Homes, providerUpgradeHomeLink{Name: home.Name, Link: ownerHomeLink(owner, home)})
	}
	_ = orihttp.RespondSuccess(w, view)
}

// ReviewPluginHomeUpgrade is POST /api/plugins/{name}/home-upgrade/review. It
// is the same review the Home page shows.
func (h *Handler) ReviewPluginHomeUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, installed, _, ok := h.pluginUpgradeScope(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	h.respondProviderUpgradeReview(w, r, owner, installed.Name)
}

// CommitPluginHomeUpgrade is POST /api/plugins/{name}/home-upgrade/commit. It
// applies exactly the review its token names: the service re-derives the plan
// and refuses one that changed, belongs to another owner or plugin, or was
// already used.
func (h *Handler) CommitPluginHomeUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, installed, _, ok := h.pluginUpgradeScope(w, r)
	if !ok {
		return
	}
	h.respondProviderUpgradeCommit(w, r, owner, installed.Name)
}

// OutcomePluginHomeUpgrade is POST /api/plugins/{name}/home-upgrade/outcome:
// what became of one review's commit, for a client that lost its response.
func (h *Handler) OutcomePluginHomeUpgrade(w http.ResponseWriter, r *http.Request) {
	owner, installed, _, ok := h.pluginUpgradeScope(w, r)
	if !ok {
		return
	}
	h.respondProviderUpgradeAttempt(w, r, owner, installed.Name)
}
