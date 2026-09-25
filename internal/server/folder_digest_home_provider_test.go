package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

func TestFolderHomeProviderRejectsUnreviewedInstalledPlugin(t *testing.T) {
	release := verifiedRelease{version: "0.1.0", source: "https://github.com/example/reviewed#sha=pinned"}
	installed := plugin.InstalledPlugin{Name: "music-project-management", Version: release.version, Source: release.source, Format: plugin.FormatClaude, Enabled: true}
	if !reviewedHomeProviderInstalled(installed, release, plugin.FormatClaude) {
		t.Fatal("reviewed installed provider was refused")
	}
	for name, alter := range map[string]func(*plugin.InstalledPlugin){
		"unreviewed source": func(p *plugin.InstalledPlugin) { p.Source = "/tmp/unreviewed" },
		"other release":     func(p *plugin.InstalledPlugin) { p.Version = "0.0.1" },
		"other format":      func(p *plugin.InstalledPlugin) { p.Format = plugin.FormatCodex },
	} {
		copy := installed
		alter(&copy)
		if reviewedHomeProviderInstalled(copy, release, plugin.FormatClaude) {
			t.Errorf("%s accepted by name alone", name)
		}
	}
}
