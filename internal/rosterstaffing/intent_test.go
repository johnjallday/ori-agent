package rosterstaffing

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func intentFixture() *TeamIntent {
	return &TeamIntent{Version: 1, Roles: []RoleIntent{
		{RoleID: "manager", Scope: "home", Mode: "assign", Name: "Aster", SourceWitness: strings.Repeat("a", 64), CapabilityWitness: strings.Repeat("b", 64)},
		{RoleID: "helper", Scope: "home", Mode: "create", Name: "Cedar", Provider: "fixture", Model: "publisher/model", Appearance: types.NewAgentAppearance()},
	}}
}

func TestStaffingIntentIsBoundedAndActionSpecific(t *testing.T) {
	if err := intentFixture().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*TeamIntent){
		"version":                func(i *TeamIntent) { i.Version++ },
		"empty":                  func(i *TeamIntent) { i.Roles = nil },
		"limit":                  func(i *TeamIntent) { i.Roles = make([]RoleIntent, MaxRoles+1) },
		"duplicate role":         func(i *TeamIntent) { i.Roles[1].RoleID = i.Roles[0].RoleID },
		"duplicate profile":      func(i *TeamIntent) { i.Roles[1].Name = "ASTER" },
		"scope":                  func(i *TeamIntent) { i.Roles[0].Scope = "foreign" },
		"mode":                   func(i *TeamIntent) { i.Roles[0].Mode = "reuse-or-create" },
		"path name":              func(i *TeamIntent) { i.Roles[0].Name = "../Aster" },
		"unbounded name":         func(i *TeamIntent) { i.Roles[0].Name = strings.Repeat("a", 121) },
		"control name":           func(i *TeamIntent) { i.Roles[0].Name = "Aster\nManager" },
		"missing source":         func(i *TeamIntent) { i.Roles[0].SourceWitness = "" },
		"invalid source":         func(i *TeamIntent) { i.Roles[0].SourceWitness = "/private/source" },
		"invalid capabilities":   func(i *TeamIntent) { i.Roles[0].CapabilityWitness = "/private/capability" },
		"assign model edit":      func(i *TeamIntent) { i.Roles[0].Model = "changed" },
		"assign provider edit":   func(i *TeamIntent) { i.Roles[0].Provider = "changed" },
		"assign appearance edit": func(i *TeamIntent) { i.Roles[0].Appearance = types.NewAgentAppearance() },
		"assign reasoning edit":  func(i *TeamIntent) { i.Roles[0].ReasoningEffort = "high" },
		"create source":          func(i *TeamIntent) { i.Roles[1].SourceWitness = strings.Repeat("a", 64) },
		"create capabilities":    func(i *TeamIntent) { i.Roles[1].CapabilityWitness = strings.Repeat("a", 64) },
		"partial configuration":  func(i *TeamIntent) { i.Roles[1].Model = "" },
		"model path":             func(i *TeamIntent) { i.Roles[1].Model = "/private/model" },
		"model control":          func(i *TeamIntent) { i.Roles[1].Model = "model\nother" },
		"appearance upload":      func(i *TeamIntent) { i.Roles[1].Appearance.Uploaded = &types.UploadedAppearance{Image: "private.png"} },
		"appearance mode":        func(i *TeamIntent) { i.Roles[1].Appearance.Mode = "unknown" },
		"appearance color":       func(i *TeamIntent) { i.Roles[1].Appearance.Generated.Color = "invalid" },
		"character absent":       func(i *TeamIntent) { i.Roles[1].Appearance.Mode = types.AppearanceModeCharacter },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			intent := intentFixture()
			mutate(intent)
			if err := intent.Validate(); err == nil {
				t.Fatal("invalid reviewed choice accepted")
			}
		})
	}
	fileStore := intentFixture()
	fileStore.Roles[0].SourceWitness = "12345678-1234-1234-1234-123456789abc"
	if err := fileStore.Validate(); err != nil {
		t.Fatal("opaque file-store witness was mistaken for a composite digest", err)
	}
	missing := intentFixture()
	missing.Roles[1].Provider, missing.Roles[1].Model = "", ""
	if err := missing.Validate(); err != nil {
		t.Fatal("honest needs-model plan refused", err)
	}
}

func TestStaffingIntentStepDigestBindsReviewTargetAndPredecessor(t *testing.T) {
	role := intentFixture().Roles[0]
	review := strings.Repeat("d", 64)
	baseline := role.StepDigest(review, "home-1")
	if baseline == "" || baseline != role.StepDigest(review, "home-1") {
		t.Fatal("step identity is missing or unstable")
	}
	if baseline == role.StepDigest(review, "home-2") || baseline == role.StepDigest(strings.Repeat("e", 64), "home-1") {
		t.Fatal("different owner review or target shares step identity")
	}
	role.PreviousChoiceDigest = strings.Repeat("f", 64)
	if role.StepDigest(review, "home-1") == baseline {
		t.Fatal("fresh review of a cleared slot retained old step identity")
	}
	role.PreviousChoiceDigest = "invalid"
	if role.StepDigest(review, "home-1") != "" || role.StepDigest("invalid", "home-1") != "" {
		t.Fatal("invalid evidence acquired step identity")
	}
}

func TestStaffingIntentCloneDoesNotRenewOrAliasChoices(t *testing.T) {
	original := intentFixture()
	copy := original.Clone()
	copy.Roles[0].SourceWitness = strings.Repeat("c", 64)
	copy.Roles[1].Appearance.Generated.Color = "#112233"
	if original.Roles[0].SourceWitness != strings.Repeat("a", 64) || original.Roles[1].Appearance.Generated.Color != "" {
		t.Fatal("review aliases its caller")
	}
	var absent *TeamIntent
	if absent.Clone() != nil || absent.Validate() == nil {
		t.Fatal("absent selection became a valid team")
	}
}
