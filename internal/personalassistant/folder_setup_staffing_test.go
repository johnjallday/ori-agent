package personalassistant

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/types"
)

func homeStaffingPlanFixture() FolderSetupPlan {
	destination := &FolderSetupDestination{
		Status: "new", Kind: "home", Name: "Collection Home", OwnerUserID: "local",
		ProviderPlugin: "fixture", ProviderVersion: "1.0.0", ProgramID: "collection",
		DeclarationDigest: strings.Repeat("c", 64), CompatibilityHash: strings.Repeat("d", 64),
	}
	return FolderSetupPlan{
		Lines: []FolderPlanLine{{Kind: FolderPlanAgents, Name: "Use Aster; create Cedar"}}, Destination: destination,
		Intent: FolderSetupIntent{
			Portfolio: true, StaffsHome: true, CreatesHome: true, Placement: "grouped",
			HomeTemplate: "plugin-home:fixture:collection", ProviderPlugin: "fixture", ProviderVersion: "1.0.0",
			HomeStaffing: &rosterstaffing.TeamIntent{Version: 1, Roles: []rosterstaffing.RoleIntent{
				{RoleID: "manager", Scope: "home", Mode: "assign", Name: "Aster", SourceWitness: strings.Repeat("a", 64), CapabilityWitness: strings.Repeat("b", 64)},
				{RoleID: "helper", Scope: "home", Mode: "create", Name: "Cedar", Provider: "fixture", Model: "model", Appearance: types.NewAgentAppearance()},
			}},
		},
	}
}

func TestFolderHomeStaffingDigestBindsExactChoicesAndEnclosingOwner(t *testing.T) {
	plan := homeStaffingPlanFixture().Stamped()
	if plan.Digest == "" || plan.Stamped().Digest != plan.Digest {
		t.Fatal("valid plan not stable")
	}
	cases := map[string]func(*FolderSetupPlan){
		"same name replacement": func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[0].SourceWitness = strings.Repeat("e", 64) },
		"capability change":     func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[0].CapabilityWitness = strings.Repeat("e", 64) },
		"model":                 func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[1].Model = "other" },
		"provider":              func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[1].Provider = "other" },
		"reasoning":             func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[1].ReasoningEffort = "high" },
		"appearance":            func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[1].Appearance.Generated.Color = "#112233" },
		"different action same name": func(p *FolderSetupPlan) {
			p.Intent.HomeStaffing.Roles[0] = rosterstaffing.RoleIntent{RoleID: "manager", Scope: "home", Mode: "create", Name: "Aster", Provider: "fixture", Model: "model"}
		},
		"owner":           func(p *FolderSetupPlan) { p.Destination.OwnerUserID = "other" },
		"home name":       func(p *FolderSetupPlan) { p.Destination.Name = "Other Home" },
		"declaration":     func(p *FolderSetupPlan) { p.Destination.DeclarationDigest = strings.Repeat("e", 64) },
		"compatibility":   func(p *FolderSetupPlan) { p.Destination.CompatibilityHash = strings.Repeat("e", 64) },
		"sharing promise": func(p *FolderSetupPlan) { p.Intent.GrantsConsent = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := homeStaffingPlanFixture()
			mutate(&changed)
			got := changed.Stamped()
			if got.Digest == "" || got.Digest == plan.Digest {
				t.Fatal("material change lost despite identical visible agent names")
			}
		})
	}
	copy := plan.Stamped()
	copy.Intent.HomeStaffing.Roles[1].Appearance.Generated.Color = "#445566"
	if plan.Intent.HomeStaffing.Roles[1].Appearance.Generated.Color != "" {
		t.Fatal("appearance aliases review")
	}
	copy = plan.Stamped()
	copy.Lines[0].State = FolderLineWorking
	copy.Visual = &FolderSetupVisual{Model: &FolderSetupModel{State: "missing"}}
	if copy.Stamped().Digest != plan.Digest {
		t.Fatal("progress or display renewed the reviewed team")
	}
	data, err := json.Marshal(plan)
	if err != nil || strings.Contains(string(data), "source_witness") || strings.Contains(string(data), strings.Repeat("a", 64)) || strings.Contains(string(data), "capability_witness") {
		t.Fatal("private source evidence escaped plan presentation", err)
	}
}

func TestFolderHomeStaffingRequiresKnownHomeDeclaration(t *testing.T) {
	cases := map[string]func(*FolderSetupPlan){
		"unknown destination":   func(p *FolderSetupPlan) { p.Destination = nil },
		"unknown declaration":   func(p *FolderSetupPlan) { p.Destination.DeclarationDigest = "" },
		"unknown compatibility": func(p *FolderSetupPlan) { p.Destination.CompatibilityHash = "" },
		"unknown owner":         func(p *FolderSetupPlan) { p.Destination.OwnerUserID = "" },
		"wrong provider":        func(p *FolderSetupPlan) { p.Intent.ProviderPlugin = "other" },
		"wrong version":         func(p *FolderSetupPlan) { p.Intent.ProviderVersion = "2.0.0" },
		"wrong home template":   func(p *FolderSetupPlan) { p.Intent.HomeTemplate = "plugin-home:fixture:other" },
		"wrong scope":           func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[0].Scope = "project" },
		"wrong action":          func(p *FolderSetupPlan) { p.Intent.HomeStaffing.Roles[0].Mode = "automatic" },
		"wrong recipe":          func(p *FolderSetupPlan) { p.Intent.Recipe = "other" },
		"wrong placement":       func(p *FolderSetupPlan) { p.Intent.Placement = "standalone" },
		"not staffing":          func(p *FolderSetupPlan) { p.Intent.StaffsHome = false },
		"not portfolio":         func(p *FolderSetupPlan) { p.Intent.Portfolio = false },
		"hidden new home":       func(p *FolderSetupPlan) { p.Intent.CreatesHome = false },
		"conflicting destination": func(p *FolderSetupPlan) {
			other := *p.Destination
			other.OwnerUserID = "other"
			p.Intent.Destination = &other
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			plan := homeStaffingPlanFixture()
			mutate(&plan)
			if plan.Stamped().Digest != "" {
				t.Fatal("unknown or conflicting Home gained staffing authority")
			}
		})
	}
}

func homeStaffingDocumentFixture() FolderDigestDocument {
	plan := homeStaffingPlanFixture().Stamped()
	return FolderDigestDocument{SchemaVersion: FolderDigestSchemaVersion, Version: 1,
		Owner: KnowledgeOwner{UserID: "local"}, Offers: []FolderOffer{{
			ID: "offer", Status: FolderOfferPending, FolderKey: strings.Repeat("f", 64), FolderName: "Collection",
			Subject: FolderCandidateRecord{Key: strings.Repeat("f", 64), Name: "Collection", Kind: FolderChoiceProject},
			Setup:   &FolderSetupRun{PlanDigest: plan.Digest, Lines: plan.Lines, Intent: plan.Intent, Status: FolderSetupStopped},
		}}}
}

func TestFolderHomeStaffingRoundTripAndLegacyCannotAcquireReuse(t *testing.T) {
	doc := homeStaffingDocumentFixture()
	data, err := encodeFolderDigest(doc)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeFolderDigest(data, doc.Owner)
	if err != nil || !reflect.DeepEqual(decoded.Offers[0].Setup.Intent.HomeStaffing, doc.Offers[0].Setup.Intent.HomeStaffing) {
		t.Fatal("exact team did not survive storage", err)
	}
	for _, version := range []int{1, 2} {
		legacy := homeStaffingDocumentFixture()
		legacy.SchemaVersion = version
		data, err = json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = decodeFolderDigest(data, legacy.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatal("old schema injected new reuse authority", version, err)
		}
		legacy.Offers[0].Setup.Intent.HomeStaffing = nil
		data, err = json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		migrated, err := decodeFolderDigest(data, legacy.Owner)
		if err != nil || migrated.SchemaVersion != FolderDigestSchemaVersion || migrated.Offers[0].Setup.Intent.HomeStaffing != nil {
			t.Fatal("legacy migration failed or invented a selection", version, err)
		}
	}
	doc.Offers[0].Setup.Intent.Destination.OwnerUserID = "other"
	if _, err = encodeFolderDigest(doc); err == nil {
		t.Fatal("foreign staffing owner persisted")
	}
}
