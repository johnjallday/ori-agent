package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

func TestFolderHomeProviderRejectsUnreviewedInstalledPlugin(t *testing.T) {
	provider, ok := reviewedintegration.HomeProviderFor("music-project-management")
	if !ok {
		t.Fatal("the Music Home provider is not reviewed")
	}
	pinned := func(commit string) string { return provider.SourceRepository + "#sha=" + strings.Repeat(commit, 40) }
	release := verifiedRelease{version: "0.1.1", source: pinned("b")}
	current := plugin.InstalledPlugin{Name: provider.PluginID, Version: release.version, Source: release.source, Format: provider.SourceFormat, Enabled: true}
	if got := classifyInstalledHomeProvider(current, release, provider); got != installedHomeProviderCurrent {
		t.Fatalf("reviewed installed provider = %v", got)
	}

	// An older reviewed release is offered as an update to the newest one.
	older := current
	older.Version, older.Source = "0.1.0", pinned("a")
	if got := classifyInstalledHomeProvider(older, release, provider); got != installedHomeProviderOlder {
		t.Fatalf("older reviewed release = %v", got)
	}

	for name, alter := range map[string]func(*plugin.InstalledPlugin){
		"unreviewed source":        func(p *plugin.InstalledPlugin) { p.Source = "/tmp/unreviewed" },
		"same source, other bytes": func(p *plugin.InstalledPlugin) { p.Version = "0.0.1" },
		"other format":             func(p *plugin.InstalledPlugin) { p.Format = plugin.FormatCodex },
		"older, unpinned source":   func(p *plugin.InstalledPlugin) { p.Version, p.Source = "0.1.0", provider.SourceRepository },
		"older, local export":      func(p *plugin.InstalledPlugin) { p.Version, p.Source = "0.1.0", "/tmp/music-project-management" },
		"newer than the release":   func(p *plugin.InstalledPlugin) { p.Version, p.Source = "0.2.0", pinned("c") },
	} {
		copy := current
		alter(&copy)
		if got := classifyInstalledHomeProvider(copy, release, provider); got != installedHomeProviderUnrelated {
			t.Errorf("%s accepted by name alone: %v", name, got)
		}
	}
}
