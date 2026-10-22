package foldersetup

import "github.com/johnjallday/ori-agent/internal/personalassistant"

// visualPlugin copies the same canonical facts pluginLine words. It is not a
// second plugin resolver and never derives versions by parsing that prose.
func visualPlugin(kind string, plugin Plugin) personalassistant.FolderSetupPlugin {
	return personalassistant.FolderSetupPlugin{
		Kind: kind, Name: plugin.Name, Operation: plugin.State,
		Version: plugin.Version, InstalledVersion: plugin.InstalledVersion,
	}
}
