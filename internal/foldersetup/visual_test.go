package foldersetup

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestVisualPlanUsesCanonicalFactsNotProse(t *testing.T) {
	plan := BuildPlan(PlanFacts{WorkspaceName: "A name with <markup>",
		Integration: Plugin{Name: "REAPER", State: personalassistant.FolderInstallUpdate, Version: "0.9.1", InstalledVersion: "0.9.0"},
		Provider:    &Plugin{Name: "Music", State: personalassistant.FolderInstallInstall},
		Hire:        &personalassistant.FolderHire{Mode: personalassistant.FolderHirePending},
	})
	if plan.Visual.Entity.Name != "A name with <markup>" || plan.Visual.Entity.Operation != "create" || plan.Visual.Entity.Kind != "workspace" {
		t.Fatalf("wrong entity: %+v", plan.Visual.Entity)
	}
	if len(plan.Visual.Plugins) != 2 || plan.Visual.Plugins[0].InstalledVersion != "0.9.0" || plan.Visual.Plugins[1].Version != "" {
		t.Fatalf("wrong versions: %+v", plan.Visual.Plugins)
	}
	if plan.Hire.Mode != personalassistant.FolderHirePending || plan.Hire.AgentName != "" {
		t.Fatal("unknown roster gained an identity")
	}
}

func TestVisualVariantsKeepTheirActualEntityAndOperation(t *testing.T) {
	software := BuildSoftwarePlan(SoftwareFacts{WorkspaceName: "Draft", Existing: &SoftwareExisting{WorkspaceID: "existing", Name: "Actual"}})
	if software.Visual.Entity.Name != "Actual" || software.Visual.Entity.Operation != "reuse" || len(software.Visual.Plugins) != 0 {
		t.Fatalf("wrong software projection: %+v", software.Visual)
	}
	for _, existing := range []bool{false, true} {
		operation := "create"
		if existing {
			operation = "reuse"
		}
		group := BuildStructurePlan(StructureFacts{GroupName: "Collection", GroupType: personalassistant.FolderGroupPlain, GroupExists: existing})
		if group.Visual.Entity.Kind != "group" || group.Visual.Entity.Operation != operation || len(group.Visual.Plugins) != 0 {
			t.Fatalf("wrong group projection: %+v", group.Visual)
		}
		home := BuildPortfolioPlan(PortfolioFacts{HomeName: "Music", HomeExists: existing})
		if home.Visual.Entity.Kind != "home" || home.Visual.Entity.Name != "Music" || home.Visual.Entity.Operation != operation {
			t.Fatalf("wrong library projection: %+v", home.Visual)
		}
	}
}
