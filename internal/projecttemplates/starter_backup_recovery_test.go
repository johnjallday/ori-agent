package projecttemplates_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestBackupRecoveryTemplateHasOneOrdinaryAdvisorAndNoGrants(t *testing.T) {
	root := t.TempDir()
	for range 2 {
		if err := projecttemplates.EnsureLibrary(root); err != nil {
			t.Fatal(err)
		}
	}
	tpl, err := projecttemplates.FindLibraryTemplate(root, "backup-and-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.ID != "backup-and-recovery" || tpl.Name != "Backup & Recovery" || !tpl.Builtin || tpl.BuiltinVersion != 1 || tpl.Retired || len(tpl.Warnings) != 0 {
		t.Fatal("template identity or normalization drifted", tpl.Warnings)
	}
	if tpl.HasSkeleton || tpl.HasDashboard || tpl.ProjectEntry != nil || tpl.ProgramHomeOwner != nil || tpl.PluginOwner != nil || tpl.RuntimeRequirements != nil {
		t.Fatal("template created a project scaffold, Home or integration authority")
	}
	if len(tpl.Agents) != 1 || tpl.Agents[0].Name != "Backup Advisor" || tpl.Agents[0].Role != "specialist" || !reflect.DeepEqual(projecttemplates.AgentRoleIDs(tpl.Agents), []string{"backup-advisor"}) {
		t.Fatal("template changed the single advisor role")
	}
	if !tpl.Tools.IsEmpty() || len(tpl.Tools.PluginSources) != 0 || !tpl.Agents[0].Tools.IsEmpty() || len(tpl.Agents[0].Tools.PluginSources) != 0 || len(tpl.DirectoryRequirements) != 0 || len(tpl.AutomationRecipes) != 0 || len(tpl.CapabilityRequirements) != 0 || len(tpl.IntakeRequirements) != 0 {
		t.Fatal("creation declared a tool, folder, watcher or execution requirement")
	}
	if len(tpl.Capabilities) != 1 || tpl.Capabilities[0].ID != workspace.CapabilityBackupRecovery || tpl.Capabilities[0].Source != "blueprint" {
		t.Fatal("capability selection drifted")
	}
	if !tpl.HasSetupWizard() || tpl.HasInvalidSetupWizard() || len(tpl.SetupWizard.Steps) != 2 {
		t.Fatal("wizard unusable", tpl.SetupWizardError)
	}
	for i, step := range tpl.SetupWizard.Steps {
		kind := workspace.SetupStepKindReadiness
		id := "backup-connection"
		if i == 1 {
			kind = workspace.SetupStepKindSummary
			id = "backup-summary"
		}
		if step.ID != id || step.Kind != kind || !step.Required || step.Adapter != "backup_recovery" || step.RequirementKey != "" {
			t.Fatal("wizard gained a granting action")
		}
	}
	if err := projecttemplates.ValidateAgentPrompts(tpl.Agents); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"restic 0.19.1", "outside Ori", "not proof", "not off-site"} {
		if !strings.Contains(tpl.Description, required) {
			t.Errorf("missing disclosure %q", required)
		}
	}
}
