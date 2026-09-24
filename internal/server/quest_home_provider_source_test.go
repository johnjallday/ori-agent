package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

type fixedPluginList struct {
	installed []plugin.InstalledPlugin
	err       error
}

func (f fixedPluginList) List() ([]plugin.InstalledPlugin, error) { return f.installed, f.err }

func TestQuestHomeProviderSourceMatchesTheCreateWorkspaceCard(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func([]plugin.InstalledPlugin) []plugin.InstalledPlugin
		reason  blueprintreadiness.Reason
		summary string
		first   blueprintreadiness.Action
	}{
		{
			name:    "not installed",
			mutate:  func(installed []plugin.InstalledPlugin) []plugin.InstalledPlugin { return installed[1:] },
			reason:  blueprintreadiness.ReasonPluginInstallRequired,
			summary: "REAPER Song needs its Home, which comes from a separate plugin.",
			first:   blueprintreadiness.ActionInstallPlugin,
		},
		{
			name: "switched off",
			mutate: func(installed []plugin.InstalledPlugin) []plugin.InstalledPlugin {
				installed[0].Enabled = false
				return installed
			},
			reason:  blueprintreadiness.ReasonPluginEnableRequired,
			summary: "Music Project Management is installed but switched off.",
			first:   blueprintreadiness.ActionEnablePlugin,
		},
		{
			name: "does not accept the project",
			mutate: func(installed []plugin.InstalledPlugin) []plugin.InstalledPlugin {
				installed[0].WorkspaceSurfaces.AssistantProgramHomes[0].AllowedProjectAttachments = nil
				return installed
			},
			reason:  blueprintreadiness.ReasonPluginUpdateRequired,
			summary: "Music Project Management does not accept REAPER Song projects yet.",
			first:   blueprintreadiness.ActionReviewPluginUpdate,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installed, template := splitProgramFixture()
			lister := fixedPluginList{installed: test.mutate(installed)}
			source := questHomeProviderSource{installed: lister}
			state, err := source.HomeProviderState(context.Background(), template)
			if err != nil {
				t.Fatal(err)
			}
			if !state.Reviewed || state.DisplayName != "Music Project Management" || state.MinimumVersion != "0.1.0" {
				t.Fatalf("reviewed provider = %q %v", state.DisplayName, state.Reviewed)
			}
			// The summaries are the Create Workspace card's own copy for the same
			// plugin state; both come from recoveryReadinessSources + Derive.
			if state.Readiness.Reason != test.reason || state.Readiness.Summary != test.summary ||
				len(state.Readiness.Actions) == 0 || state.Readiness.Actions[0] != test.first {
				t.Fatalf("readiness = %+v", state.Readiness)
			}
		})
	}
}

func TestQuestHomeProviderSourceReportsAnUnreadablePluginStore(t *testing.T) {
	_, template := splitProgramFixture()
	source := questHomeProviderSource{installed: fixedPluginList{err: errors.New("store unavailable")}}
	state, err := source.HomeProviderState(context.Background(), template)
	if err != nil || state.Readiness.Reason != blueprintreadiness.ReasonDependencyStateUnknown {
		t.Fatalf("unreadable store = %+v err=%v", state.Readiness, err)
	}
	template.AssistantProject = nil
	if _, err := source.HomeProviderState(context.Background(), template); err == nil {
		t.Fatal("a combined blueprint was given a Home-provider state")
	}
}
