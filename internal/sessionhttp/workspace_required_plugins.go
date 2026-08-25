package sessionhttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

type installedPluginLister interface {
	List() ([]plugin.InstalledPlugin, error)
}

func (h *Handler) SetInstalledPluginLister(lister installedPluginLister) {
	if h != nil {
		h.installedPluginLister = lister
	}
}

// unsatisfiedRequiredPlugins reports template-declared plugins that are not
// installed and enabled. Plugin-contributed blueprints are already filtered by
// this same trusted lifecycle; this guard also protects editable local
// templates that declare plugin defaults.
func (h *Handler) unsatisfiedRequiredPlugins(tools projecttemplates.ToolDefaults) (missing, disabled []string) {
	if len(tools.Plugins) == 0 || h == nil || h.installedPluginLister == nil {
		return nil, nil
	}
	installed, err := h.installedPluginLister.List()
	if err != nil {
		return nil, nil
	}
	enabledByName := make(map[string]bool, len(installed))
	present := make(map[string]bool, len(installed))
	for _, candidate := range installed {
		key := strings.ToLower(strings.TrimSpace(candidate.Name))
		present[key] = true
		enabledByName[key] = candidate.Enabled
	}
	for _, rawName := range tools.Plugins {
		name := strings.TrimSpace(rawName)
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
