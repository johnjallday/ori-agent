package personalassistant

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFolderReceiptModelOmitsUnsafeLabelsWithoutInventingOne(t *testing.T) {
	for _, value := range []string{"", "fixture-assessor", "vendor/model-1", strings.Repeat("界", 128)} {
		if got := FolderReceiptModel(value); got != value {
			t.Errorf("recorded model changed: %q -> %q", value, got)
		}
	}
	for _, value := range []string{"/private/model", "~/.models/name", "model\nsecret", strings.Repeat("界", 129), string([]byte{0xff})} {
		if got := FolderReceiptModel(value); got != "" {
			t.Errorf("unsafe model retained: %q", got)
		}
	}
}

func TestFolderSetupVisualClonesAndDoesNotChangeConsent(t *testing.T) {
	plan := NewFolderSetupPlan([]FolderPlanLine{{Kind: FolderPlanWorkspace, Name: "Creates a workspace"}})
	plan.Visual = &FolderSetupVisual{
		Entity:  &FolderSetupEntity{Kind: "workspace", Name: "Song", Operation: "create"},
		Plugins: []FolderSetupPlugin{{Kind: FolderPlanIntegration, Name: "REAPER", Operation: FolderInstallUpdate, Version: "1.2", InstalledVersion: "1.1"}},
	}
	stamped := plan.Stamped()
	if stamped.Digest != plan.Digest || stamped.Visual == plan.Visual || stamped.Visual.Entity == plan.Visual.Entity {
		t.Fatal("display changed consent or retained mutable source")
	}
	stamped.Visual.Entity.Name = "Other"
	stamped.Visual.Plugins[0].Version = "9.9"
	if plan.Visual.Entity.Name != "Song" || plan.Visual.Plugins[0].Version != "1.2" {
		t.Fatal("display projection aliases its source")
	}
	if stamped.Stamped().Digest != plan.Digest {
		t.Fatal("display facts became authority")
	}
	plan.Intent.HomeTemplate = "server-only-witness"
	encoded, err := json.Marshal(plan.Stamped())
	if err != nil || strings.Contains(string(encoded), "server-only-witness") {
		t.Fatalf("private intent escaped: %s, %v", encoded, err)
	}
}

func TestFolderSetupVisualOmitsInvalidAndUnknownFacts(t *testing.T) {
	for _, name := range []string{"", "/private/secret", "~/.keys", "a /private/secret", "a\\b", "a\nb", strings.Repeat("界", 121)} {
		v := CloneFolderSetupVisual(&FolderSetupVisual{Entity: &FolderSetupEntity{Kind: "workspace", Name: name, Operation: "create"}})
		if v != nil {
			t.Errorf("unsafe/unbounded name retained: %q", name)
		}
	}
	for _, entity := range []FolderSetupEntity{
		{Kind: "unknown", Name: "Song", Operation: "create"},
		{Kind: "workspace", Name: "Song", Operation: "assigned"},
	} {
		if CloneFolderSetupVisual(&FolderSetupVisual{Entity: &entity}) != nil {
			t.Fatal("invented kind/completion retained")
		}
	}
	v := CloneFolderSetupVisual(&FolderSetupVisual{Plugins: []FolderSetupPlugin{
		{Kind: "integration", Name: "REAPER", Operation: "update", Version: "/private/version"},
		{Kind: "integration", Name: "Duplicate", Operation: "install"},
		{Kind: "provider", Name: "Music", Operation: "unknown"},
		{Kind: "other", Name: "Other", Operation: "ready"},
	}})
	if v == nil || len(v.Plugins) != 1 || v.Plugins[0].Version != "" || v.Plugins[0].InstalledVersion != "" {
		t.Fatalf("unknown versions/kinds were fabricated: %#v", v)
	}
	if CloneFolderSetupVisual(nil) != nil {
		t.Fatal("legacy plan gained invented facts")
	}
}

func TestFolderSetupVisualRunProjectionKeepsPendingIdentityAndDestination(t *testing.T) {
	for _, status := range []string{"new", "existing", "standalone"} {
		t.Run(status, func(t *testing.T) {
			run := &FolderSetupRun{Status: FolderSetupStopped,
				Visual: &FolderSetupVisual{Entity: &FolderSetupEntity{Kind: "workspace", Name: "Song", Operation: "create"}},
				Hire:   &FolderHire{Mode: FolderHirePending, RoleLabel: "Production"},
				Intent: FolderSetupIntent{Destination: &FolderSetupDestination{Status: status, Name: "Music"}},
			}
			var view FolderOfferView
			(&FolderDigestService{}).attachSetupRun(FolderOffer{Status: FolderOfferAwaitingOutcome, Setup: run}, &view)
			if view.Setup == nil || view.Setup.Visual.Entity.Name != "Song" || view.Setup.Destination.Status != status || view.Setup.Hire.AgentName != "" {
				t.Fatalf("lost display fact or invented identity: %+v", view.Setup)
			}
			view.Setup.Visual.Entity.Name = "Changed"
			view.Setup.Hire.AgentName = "Changed"
			view.Setup.Destination.Name = "Changed"
			if run.Visual.Entity.Name != "Song" || run.Hire.AgentName != "" || run.Intent.Destination.Name != "Music" {
				t.Fatal("run projection aliases stored facts")
			}
		})
	}
}

func TestFolderSetupVisualStampPreservesUnknownPrerequisite(t *testing.T) {
	plan := FolderSetupPlan{Prerequisites: []FolderPrerequisite{{ID: "model", State: FolderPrerequisiteUnknown}}}
	stamped := plan.Stamped()
	if stamped.Prerequisites[0].State != FolderPrerequisiteUnknown || stamped.Visual != nil {
		t.Fatal("unknown prerequisite gained invented facts")
	}
	stamped.Prerequisites[0].State = FolderPrerequisiteVerified
	if plan.Prerequisites[0].State != FolderPrerequisiteUnknown {
		t.Fatal("prerequisite projection aliases source")
	}
}
