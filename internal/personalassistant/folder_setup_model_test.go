package personalassistant

import "testing"

func TestFolderModelDisplayIsBoundedClonedAndNotConsent(t *testing.T) {
	plan := NewFolderSetupPlan([]FolderPlanLine{{Kind: FolderPlanWorkspace, Name: "Creates a workspace"}})
	plan.Visual = &FolderSetupVisual{Model: &FolderSetupModel{State: "configured", Provider: "fixture", Name: "fixture-model", Settings: "system"}}
	shown := plan.Stamped()
	if shown.Digest != plan.Digest || shown.Visual.Model == plan.Visual.Model {
		t.Fatal("display changed consent or aliases its source")
	}
	shown.Visual.Model.State = "missing"
	if plan.Visual.Model.State != "configured" {
		t.Fatal("display mutation escaped its clone")
	}
	for _, value := range []FolderSetupModel{
		{State: "connected", Settings: "https://untrusted"},
		{State: "configured", Provider: "fixture"},
		{State: "configured", Provider: "fixture", Name: "/private/model"},
	} {
		plan.Visual.Model = &value
		got := plan.Stamped()
		if got.Visual.Model.State != "not_checked" || got.Digest != plan.Digest {
			t.Fatalf("invalid configuration became authority: %+v", got)
		}
	}
	plan.Visual.Model = &FolderSetupModel{State: "missing", Settings: "agent", AgentName: "../private"}
	if got := plan.Stamped().Visual.Model; got.Settings != "" || got.AgentName != "" {
		t.Fatal("unsafe agent configuration target retained", got)
	}
}

func TestFolderModelWitnessChangesConsentButProgressAndDisplayDoNot(t *testing.T) {
	plan := NewFolderSetupPlan([]FolderPlanLine{{Kind: FolderPlanWorkspace, Name: "Creates a workspace"}})
	plan.Intent.Model = &FolderSetupModelIntent{Source: "system", Provider: "fixture", Model: "first"}
	first := plan.Stamped()
	if first.Digest == plan.Digest || first.Intent.Model == plan.Intent.Model {
		t.Fatal("material witness omitted or aliased")
	}
	plan.Intent.Model.Model = "second"
	if first.Intent.Model.Model != "first" || plan.Stamped().Digest == first.Digest {
		t.Fatal("material model change retained old consent")
	}
	first.Visual = &FolderSetupVisual{Model: &FolderSetupModel{State: "missing"}}
	first.Lines[0].State = FolderLineWorking
	if first.Stamped().Digest != first.Digest {
		t.Fatal("progress/display changed consent")
	}
}
