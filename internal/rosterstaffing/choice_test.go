package rosterstaffing

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestReviewedChoicesAreDetachedPublicEffectsNotSourceAuthority(t *testing.T) {
	team := &TeamIntent{Version: 1, Roles: []RoleIntent{
		{RoleID: "manager", Scope: "home", Mode: "assign", Name: "Aster", SourceWitness: "PRIVATE_SOURCE", CapabilityWitness: strings.Repeat("a", 64), PreviousChoiceDigest: strings.Repeat("b", 64)},
		{RoleID: "helper", Scope: "home", Mode: "create", Name: "Cedar", Provider: "fixture", Model: "model", Appearance: types.NewAgentAppearance()},
	}}
	choices := team.Choices()
	data, err := json.Marshal(choices)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE_SOURCE", "source_witness", "capability_witness", "previous_choice_digest", strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatal("public effects exposed private proof")
		}
	}
	choices[1].Appearance.Generated.Color = "#123456"
	if team.Roles[1].Appearance.Generated.Color != "" || choices[0].Recommendation != "" || choices[0].Provider != "" {
		t.Fatal("public projection renewed authority or aliases intent")
	}
	var decoded []RoleChoice
	decoder := json.NewDecoder(strings.NewReader(`[{"role_id":"manager","mode":"assign","name":"Aster","source_witness":"injected"}]`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err == nil {
		t.Fatal("public choice accepted source authority")
	}
}
