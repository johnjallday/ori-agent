package server

import (
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// A process-local override recognizes only this exact installed, normalized
// candidate. It cannot install/enable another local source or mark a release
// verified. Ordinary local installs retain the production refusal.
func folderHomeDevelopmentReady(entry plugin.InstalledPlugin, provider reviewedintegration.HomeProvider, source string) bool {
	source = strings.TrimSpace(source)
	if source == "" || !filepath.IsAbs(source) || !filepath.IsAbs(entry.Source) || filepath.Clean(entry.Source) != filepath.Clean(source) || entry.Name != provider.PluginID || entry.Format != provider.SourceFormat || !entry.Enabled || !reviewedintegration.AtLeast(entry.Version, provider.MinimumVersion) || entry.Generation <= 0 || entry.ComponentFingerprint == "" || entry.WorkspaceSurfaces == nil {
		return false
	}
	count := 0
	for _, raw := range entry.WorkspaceSurfaces.AssistantProgramHomes {
		home := raw
		if home.ID != provider.ProgramID {
			continue
		}
		if home.SchemaVersion != provider.HomeSchemaVersion || projecttemplates.NormalizeAssistantProgramHome(&home) != nil {
			return false
		}
		count++
	}
	return count == 1
}
