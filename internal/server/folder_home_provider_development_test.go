package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHomeDevelopmentPreview_ExactProcessSourceOnlyAndNoReleaseClaim(t *testing.T) {
	provider := reviewedintegration.HomeProviders()[0]
	home := projecttemplates.AssistantProgramHome{SchemaVersion: 1, Version: 1, ID: provider.ProgramID, StationName: "Music Home", DefaultPrimaryName: "Portfolio Manager", HireTitle: "Staff Home", Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "portfolio_manager", Label: "Portfolio Manager", Required: true, Primary: true, SystemPrompt: "Coordinate reviewed work."}}, Stages: []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation"}}, Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."}}
	if err := projecttemplates.NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	entry := plugin.InstalledPlugin{Name: provider.PluginID, Version: "0.1.1", Format: provider.SourceFormat, Source: source, Enabled: true, Generation: 2, ComponentFingerprint: strings.Repeat("a", 64), WorkspaceSurfaces: &plugin.SurfaceContribution{AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home}}}
	if !folderHomeDevelopmentReady(entry, provider, source) {
		t.Fatal("exact canonical candidate refused")
	}
	alias := entry
	alias.Source = source + "/./"
	if !folderHomeDevelopmentReady(alias, provider, source) {
		t.Fatal("same normalized candidate path refused")
	}
	for _, test := range []struct {
		name, source string
		change       func(*plugin.InstalledPlugin)
	}{
		{"no process override", "", func(*plugin.InstalledPlugin) {}},
		{"another source", source + "-other", func(*plugin.InstalledPlugin) {}},
		{"relative path", "candidate", func(*plugin.InstalledPlugin) {}},
		{"disabled", source, func(e *plugin.InstalledPlugin) { e.Enabled = false }},
		{"wrong version", source, func(e *plugin.InstalledPlugin) { e.Version = "0.0.1" }},
		{"wrong identity", source, func(e *plugin.InstalledPlugin) { e.Name = "other" }},
		{"missing declaration", source, func(e *plugin.InstalledPlugin) { e.WorkspaceSurfaces = nil }},
		{"unverified generation", source, func(e *plugin.InstalledPlugin) { e.Generation = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := entry
			test.change(&copy)
			if folderHomeDevelopmentReady(copy, provider, test.source) {
				t.Fatal("unverified candidate accepted")
			}
		})
	}
}
