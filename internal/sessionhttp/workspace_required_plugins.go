package sessionhttp

import (
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// installedPluginLister is the authoritative installed-plugin store as the
// creation gate sees it: what exists on this machine, and whether it is
// enabled. Blueprint readiness is derived from it and from nothing a template
// asserted about the same plugins — see workspace_blueprint_readiness.go.
type installedPluginLister interface {
	List() ([]plugin.InstalledPlugin, error)
}

func (h *Handler) SetInstalledPluginLister(lister installedPluginLister) {
	if h != nil {
		h.installedPluginLister = lister
	}
}
