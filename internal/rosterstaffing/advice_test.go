package rosterstaffing

import (
	"encoding/json"
	"strings"
	"testing"
)

func adviceFixture() *Advice {
	return &Advice{Version: 2, State: Recommended, TemplateID: "home", TemplateRevision: strings.Repeat("c", 64),
		Coverage: Coverage{Complete: true, Inspected: 1, Included: 1}, Roles: []RoleAdvice{{
			RoleID: "manager", Scope: "home", State: Recommended, Choices: []AdviceChoice{{
				Handle: "candidate", Name: "Aster", Source: "root", Configured: true,
				SourceWitness: "private-source-token", CapabilityWitness: strings.Repeat("d", 64),
				Reasons: []Evidence{{ID: "purpose", Kind: "declared_purpose", Value: "Coordinates music projects."}}, Gaps: []string{"assignments_unknown"},
			}},
		}}}
}

func TestAdvicePublicAndCloneSeparateEvidenceFromPrivateWitnesses(t *testing.T) {
	advice := adviceFixture()
	if err := advice.Validate(0); err != nil {
		t.Fatal(err)
	}
	public := advice.Public()
	data, err := json.Marshal(public)
	if err != nil || strings.Contains(string(data), "witness") || strings.Contains(string(data), "private-source-token") || strings.Contains(string(data), strings.Repeat("d", 64)) {
		t.Fatal("private evidence escaped presentation", err)
	}
	public.Roles[0].Choices[0].Reasons[0].Value = "Changed"
	public.Roles[0].Choices[0].Gaps[0] = "changed"
	if advice.Roles[0].Choices[0].Reasons[0].Value != "Coordinates music projects." || advice.Roles[0].Choices[0].Gaps[0] != "assignments_unknown" {
		t.Fatal("public projection aliases persisted evidence")
	}
	clone := advice.Clone()
	clone.Roles[0].Choices[0].SourceWitness = "changed"
	if advice.Roles[0].Choices[0].SourceWitness != "private-source-token" {
		t.Fatal("private clone aliases source")
	}
	var absent *Advice
	if absent.Public() != nil || absent.Clone() != nil || absent.Validate(0) != nil {
		t.Fatal("absent advice changed shape")
	}
}

func TestAdviceLegacyCannotAcquireSkillWitnesses(t *testing.T) {
	advice := adviceFixture()
	advice.Version = 1
	if err := advice.Validate(0); err == nil {
		t.Fatal("legacy advice acquired a capability witness")
	}
	advice.Roles[0].Choices[0].CapabilityWitness = ""
	if err := advice.Validate(0); err != nil {
		t.Fatal("genuine legacy evidence refused", err)
	}
	advice.Roles[0].Choices[0].Reasons[0].Kind = "enabled_skill"
	if err := advice.Validate(0); err == nil {
		t.Fatal("legacy advice acquired new skill evidence")
	}
	advice.Version = 2
	advice.Roles[0].Choices[0].CapabilityWitness = strings.Repeat("d", 64)
	if err := advice.Validate(0); err != nil {
		t.Fatal("current witnessed evidence refused", err)
	}
}
