package foldersetup

import "github.com/johnjallday/ori-agent/internal/personalassistant"

// PluginVisual copies the same canonical facts pluginLine words. It is not a
// second plugin resolver and never derives versions by parsing that prose.
func PluginVisual(kind string, plugin Plugin) personalassistant.FolderSetupPlugin {
	purpose := ""
	switch kind {
	case personalassistant.FolderPlanIntegration:
		purpose = "Adds the project blueprint and integration tools to Ori."
	case personalassistant.FolderPlanProvider:
		purpose = "Provides the Home that organizes these project workspaces."
	}
	return personalassistant.FolderSetupPlugin{
		Kind: kind, Name: plugin.Name, Operation: plugin.State,
		Version: plugin.Version, InstalledVersion: plugin.InstalledVersion,
		Source: plugin.Source, Purpose: purpose,
	}
}
