package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestFolderHomeStaffingBaseSeparatesLegacyDefaultsFromReviewedReuse(t *testing.T) {
	makePlan := func(model string) personalassistant.FolderSetupPlan {
		configuration := &personalassistant.FolderSetupModel{State: "configured", Provider: "fixture", Name: model, Settings: "system"}
		return personalassistant.FolderSetupPlan{
			Intent: personalassistant.FolderSetupIntent{Portfolio: true, StaffsHome: true, Placement: "grouped", GrantsConsent: true,
				Model: &personalassistant.FolderSetupModelIntent{Source: "system", Provider: "fixture", Model: model}},
			Visual: &personalassistant.FolderSetupVisual{Model: configuration, Entity: &personalassistant.FolderSetupEntity{Kind: "home", Name: "Home", Operation: personalassistant.FolderSoftwareCreate}},
			Lines: []personalassistant.FolderPlanLine{
				{Kind: personalassistant.FolderPlanAgents, Name: "Adds the agents the Home requires", Detail: "Old automatic manager promise." + homeAgentsModelSentence(configuration)},
				{Kind: personalassistant.FolderPlanLibrary, Name: "Lists the collection", Detail: "Names only."},
				{Kind: personalassistant.FolderPlanAgents, Name: "Adds the agents this blueprint requires", Detail: "The assistant is shared for later projects." + homeAgentsModelSentence(configuration)},
			},
		}
	}
	plan := makePlan("old-default")
	before, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	base := homeStaffingBasePlan(plan)
	other := homeStaffingBasePlan(makePlan("changed-default"))
	if base.Intent.Model != nil || base.Visual.Model != nil || base.Intent.HomeStaffing != nil || base.Intent.RequiresHomeStaffing || !base.Intent.GrantsConsent || base.Digest == "" || base.Digest != other.Digest {
		t.Fatal("new-agent defaults affected unchosen reuse context or other consent")
	}
	if !strings.Contains(base.Lines[0].Name, "reviewed team") || base.Lines[1] != plan.Lines[1] || base.Lines[2].Name != plan.Lines[2].Name || base.Lines[2].Detail != "The assistant is shared for later projects." {
		t.Fatal("normalization hid another owner's effects")
	}
	after, err := json.Marshal(plan)
	if err != nil || string(before) != string(after) || plan.Intent.Model.Model != "old-default" {
		t.Fatal("normalization changed the original plan", err)
	}
}
