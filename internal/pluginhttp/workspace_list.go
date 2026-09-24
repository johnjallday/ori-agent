package pluginhttp

// The Workspace Directory's plugin list (Plugins.json). Every completed
// install, update, and uninstall is recorded there, whichever path made it;
// enabling and disabling never are. Another machine reads the list and is
// offered each difference as a reviewed change on the Plugins page.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/store"
)

// workspaceList holds the Workspace Directory resolver for Plugins.json and
// the data-dir file of this machine's skipped changes.
type workspaceList struct {
	mu        sync.Mutex
	root      func() string
	skipsPath string
}

// SetPluginListSkipsPath is the data-dir file that remembers which listed
// changes this machine skips, per Workspace Directory. Without it, Skip on
// this Mac is refused.
func (h *Handler) SetPluginListSkipsPath(path string) {
	h.list.mu.Lock()
	h.list.skipsPath = strings.TrimSpace(path)
	h.list.mu.Unlock()
}

// SetWorkspaceRootResolver turns on the plugin list: completed installs,
// updates, and uninstalls are recorded in <root>/Plugins.json, resolved at
// the moment of each change so a root switch takes effect at once.
func (h *Handler) SetWorkspaceRootResolver(resolve func() string) {
	h.list.mu.Lock()
	h.list.root = resolve
	h.list.mu.Unlock()
	h.mgr.SetChangeObserver(h.recordPluginListChange)
}

func (h *Handler) workspaceRoot() string {
	h.list.mu.Lock()
	defer h.list.mu.Unlock()
	if h.list.root == nil {
		return ""
	}
	return strings.TrimSpace(h.list.root())
}

// recordPluginListChange writes one completed change into the list. A list
// that cannot be read is rebuilt from this machine's installed plugins rather
// than guessed at, and the rebuild is logged.
func (h *Handler) recordPluginListChange(change plugin.PluginChange) {
	root := h.workspaceRoot()
	if root == "" {
		return
	}
	h.list.mu.Lock()
	defer h.list.mu.Unlock()
	now := time.Now().UTC()
	list, _, err := plugin.ReadPluginList(root)
	if err != nil {
		logger.Warn("The plugin list could not be read, so it is rebuilt from the plugins installed here", logger.Fields{
			"path": plugin.PluginListPath(root), "error": err.Error(),
		})
		list = plugin.PluginList{}
		if installed, listErr := h.mgr.Installed(); listErr == nil {
			list.AddUnlisted(installed, now)
		}
	}
	switch change.Kind {
	case plugin.PluginInstalled, plugin.PluginUpdated:
		list.RecordInstalled(change.Plugin, now)
	case plugin.PluginUninstalled:
		list.RecordUninstalled(change.Plugin.Name, now)
	}
	if _, err := plugin.WritePluginList(root, list); err != nil {
		logger.Warn("The plugin list could not be saved", logger.Fields{"path": plugin.PluginListPath(root), "error": err.Error()})
	}
}

// FillWorkspaceList adds the plugins installed here that appear in neither
// part of the list, which is also how the list is first filled. allowed must
// be the host's "this Workspace Directory was confirmed by this data dir"
// check, so a sandbox or worktree server never writes its test plugins into
// someone else's list. An unreadable list is left exactly as it is.
func (h *Handler) FillWorkspaceList(allowed bool) {
	root := h.workspaceRoot()
	if !allowed || root == "" {
		return
	}
	installed, err := h.mgr.Installed()
	if err != nil || len(installed) == 0 {
		return
	}
	h.list.mu.Lock()
	defer h.list.mu.Unlock()
	list, _, err := plugin.ReadPluginList(root)
	if err != nil {
		return
	}
	if !list.AddUnlisted(installed, time.Now().UTC()) {
		return
	}
	if wrote, err := plugin.WritePluginList(root, list); err != nil {
		logger.Warn("The plugin list could not be filled", logger.Fields{"path": plugin.PluginListPath(root), "error": err.Error()})
	} else if wrote {
		logger.Info("Added this machine's plugins to the Workspace Directory's plugin list", logger.Fields{"path": plugin.PluginListPath(root)})
	}
}

// workspaceListResponse is GET /api/plugins/workspace-list.
type workspaceListResponse struct {
	Pending   []plugin.PendingChange `json:"pending"`
	ReadError string                 `json:"read_error,omitempty"`
	Found     bool                   `json:"found"`
}

// workspaceListState compares the list with this machine's plugins, marking
// the changes this machine skips.
func (h *Handler) workspaceListState() (workspaceListResponse, error) {
	response := workspaceListResponse{Pending: []plugin.PendingChange{}}
	root := h.workspaceRoot()
	if root == "" {
		return response, nil
	}
	list, found, err := plugin.ReadPluginList(root)
	response.Found = found
	if err != nil {
		response.ReadError = "Your plugin list (Plugins.json) could not be read: " + err.Error()
		return response, nil
	}
	installed, err := h.mgr.Installed()
	if err != nil {
		return response, err
	}
	response.Pending = plugin.Pending(list, installed)
	skips := h.readSkips()[store.RootKey(root)]
	for index := range response.Pending {
		change := &response.Pending[index]
		change.Skipped = skips[change.Name] != "" && skips[change.Name] == change.Fingerprint
	}
	return response, nil
}

// listedEntry is the plugin list's entry for name.
func (h *Handler) listedEntry(name string) (plugin.PluginListEntry, bool) {
	root := h.workspaceRoot()
	if root == "" {
		return plugin.PluginListEntry{}, false
	}
	list, _, err := plugin.ReadPluginList(root)
	if err != nil {
		return plugin.PluginListEntry{}, false
	}
	for _, entry := range list.Plugins {
		if entry.Name == name {
			return entry, true
		}
	}
	return plugin.PluginListEntry{}, false
}

// pluginListSkips is {root key: {plugin name: skipped entry fingerprint}}.
type pluginListSkips map[string]map[string]string

func (h *Handler) readSkips() pluginListSkips {
	h.list.mu.Lock()
	path := h.list.skipsPath
	h.list.mu.Unlock()
	skips := pluginListSkips{}
	if path == "" {
		return skips
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the host's fixed data-dir file
	if err != nil {
		return skips
	}
	if json.Unmarshal(data, &skips) != nil {
		return pluginListSkips{}
	}
	return skips
}

// skipChange remembers that this machine skips a listed change until that
// entry changes (its fingerprint covers the source and version).
func (h *Handler) skipChange(name string) error {
	response, err := h.workspaceListState()
	if err != nil {
		return err
	}
	var target *plugin.PendingChange
	for index := range response.Pending {
		if response.Pending[index].Name == name {
			target = &response.Pending[index]
		}
	}
	if target == nil {
		return errNothingToSkip
	}
	h.list.mu.Lock()
	path := h.list.skipsPath
	h.list.mu.Unlock()
	if path == "" {
		return errors.New("skipping is not available")
	}
	skips := h.readSkips()
	key := store.RootKey(h.workspaceRoot())
	if skips[key] == nil {
		skips[key] = map[string]string{}
	}
	skips[key][name] = target.Fingerprint
	data, err := json.MarshalIndent(skips, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

var errNothingToSkip = errors.New("the plugin list asks nothing of this plugin")

// SkipHandler serves POST /api/plugins/workspace-list/skip with {name}.
func (h *Handler) SkipHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		orihttp.BadRequest(w, "plugin name is required")
		return
	}
	if err := h.skipChange(name); errors.Is(err, errNothingToSkip) {
		orihttp.NotFound(w, err.Error())
		return
	} else if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, map[string]any{"skipped": name})
}

// WorkspaceListHandler serves GET /api/plugins/workspace-list: the changes the
// Workspace Directory's plugin list asks of this machine.
func (h *Handler) WorkspaceListHandler(w http.ResponseWriter, _ *http.Request) {
	response, err := h.workspaceListState()
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.WriteJSON(w, response)
}
