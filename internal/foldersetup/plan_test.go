package foldersetup

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func baseFacts() PlanFacts {
	return PlanFacts{
		AppName: "REAPER", AppInstalled: true, BlueprintLabel: "REAPER Song", WorkspaceName: "My Song",
		Integration: Plugin{
			Name: "REAPER", PluginID: "reaper-plugin", State: personalassistant.FolderInstallReady,
			Version: "0.9.0", InstalledVersion: "0.9.0", Source: "johnjallday/reaper-plugin",
		},
	}
}

func line(plan personalassistant.FolderSetupPlan, kind string) personalassistant.FolderPlanLine {
	for _, l := range plan.Lines {
		if l.Kind == kind {
			return l
		}
	}
	return personalassistant.FolderPlanLine{}
}

func TestBuildPlanWordsTheIntegrationLine(t *testing.T) {
	cases := []struct {
		state, installed, name string
	}{
		{personalassistant.FolderInstallReady, "0.9.0", "Uses the installed reviewed REAPER integration"},
		{personalassistant.FolderInstallInstall, "", "Installs and enables the reviewed REAPER integration 0.9.0"},
		{personalassistant.FolderInstallEnable, "0.9.0", "Enables the reviewed REAPER integration 0.9.0"},
		{personalassistant.FolderInstallUpdate, "0.8.2", "Updates the reviewed REAPER integration from 0.8.2 to 0.9.0"},
	}
	for _, tc := range cases {
		facts := baseFacts()
		facts.Integration.State, facts.Integration.InstalledVersion = tc.state, tc.installed
		plan := BuildPlan(facts)
		got := line(plan, personalassistant.FolderPlanIntegration)
		if got.Name != tc.name {
			t.Errorf("%s: name = %q, want %q", tc.state, got.Name, tc.name)
		}
		if plan.Intent.Integration != tc.state || plan.Intent.IntegrationPlugin != "reaper-plugin" || plan.Intent.IntegrationVersion != "0.9.0" {
			t.Errorf("%s: intent = %+v", tc.state, plan.Intent)
		}
	}
	// A download names its reviewed source; nothing installed claims one.
	facts := baseFacts()
	facts.Integration.State = personalassistant.FolderInstallInstall
	if d := line(BuildPlan(facts), personalassistant.FolderPlanIntegration).Detail; !strings.Contains(d, "johnjallday/reaper-plugin") {
		t.Errorf("an install must name its reviewed source: %q", d)
	}
	if d := line(BuildPlan(baseFacts()), personalassistant.FolderPlanIntegration).Detail; d != "Nothing is installed." {
		t.Errorf("a ready integration installs nothing: %q", d)
	}
}

func TestBuildPlanListsTheHomeProviderOnlyWhenThereIsOne(t *testing.T) {
	plain := BuildPlan(baseFacts())
	if line(plain, personalassistant.FolderPlanProvider).Kind != "" || plain.Intent.Provider != "" {
		t.Fatalf("no provider, no line: %+v", plain)
	}
	facts := baseFacts()
	facts.Provider = &Plugin{
		Name: "Music Project Management", PluginID: "music-project-management",
		State: personalassistant.FolderInstallInstall, Version: "0.1.1", Source: "johnjallday/music-project-management",
	}
	plan := BuildPlan(facts)
	got := line(plan, personalassistant.FolderPlanProvider)
	if got.Name != "Installs and enables the reviewed Music Project Management plugin 0.1.1" ||
		!strings.Contains(got.Detail, "johnjallday/music-project-management") {
		t.Fatalf("provider line = %+v", got)
	}
	if plan.Intent.Provider != personalassistant.FolderInstallInstall || plan.Intent.ProviderPlugin != "music-project-management" || plan.Intent.ProviderVersion != "0.1.1" {
		t.Fatalf("intent = %+v", plan.Intent)
	}
	// The provider is listed right after the integration, before anything that needs it.
	kinds := make([]string, 0, len(plan.Lines))
	for _, l := range plan.Lines {
		kinds = append(kinds, l.Kind)
	}
	if strings.Join(kinds, ",") != "integration,provider,workspace,folder,mode,agents,task" {
		t.Fatalf("order = %v", kinds)
	}
	facts.Provider.State = personalassistant.FolderInstallReady
	if got := line(BuildPlan(facts), personalassistant.FolderPlanProvider); got.Name != "Uses the installed reviewed Music Project Management plugin" || got.Detail == "" {
		t.Fatalf("ready provider line = %+v", got)
	}
}

func TestBuildPlanSaysWhenTheAppItselfIsNotInstalled(t *testing.T) {
	facts := baseFacts()
	facts.AppInstalled = false
	mode := line(BuildPlan(facts), personalassistant.FolderPlanMode)
	if mode.Name != "Uses File-only mode" || mode.Detail != "REAPER itself is not installed here; File-only mode works without it." {
		t.Fatalf("mode line = %+v", mode)
	}
	if got := line(BuildPlan(baseFacts()), personalassistant.FolderPlanMode).Detail; got != "Ori does not control REAPER." {
		t.Fatalf("mode detail = %q", got)
	}
	// No line promises a live connection.
	for _, l := range BuildPlan(facts).Lines {
		if strings.Contains(strings.ToLower(l.Name+l.Detail), "live") {
			t.Fatalf("a line promises live control: %+v", l)
		}
	}
}

func TestBuildPlanDigestMovesWithWhatItPromises(t *testing.T) {
	base := BuildPlan(baseFacts())
	installing := baseFacts()
	installing.Integration.State = personalassistant.FolderInstallInstall
	if BuildPlan(installing).Digest == base.Digest {
		t.Fatal("installing and not installing must be different consents")
	}
	newer := baseFacts()
	newer.Integration.State, newer.Integration.Version = personalassistant.FolderInstallInstall, "0.9.1"
	if BuildPlan(newer).Digest == BuildPlan(installing).Digest {
		t.Fatal("a different release must be a different consent")
	}
	for _, l := range base.Lines {
		if strings.ContainsAny(l.Name+l.Detail, "/\\") && !strings.Contains(l.Detail, "johnjallday/") {
			t.Fatalf("a plan line names a path: %+v", l)
		}
	}
}
