package rosterstaffing

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
)

func TestRecommendationSkillsRequireWitnessedMembershipNotAuthoredClaims(t *testing.T) {
	for _, scenario := range []string{"enabled", "disabled", "unknown", "foreign_source", "authored_claim"} {
		t.Run(scenario, func(t *testing.T) {
			profile := testProfile("Aster")
			profile.Capabilities = []string{"catalog"}
			readers := candidateReaders(profile)
			readers.Skills = func(context.Context, store.StaffingProfile) SkillMembership {
				value := SkillMembership{SourceWitness: profile.Witness, Witness: strings.Repeat("e", 64), Complete: true}
				switch scenario {
				case "enabled", "foreign_source":
					value.Enabled = []string{"catalog"}
				case "unknown":
					return SkillMembership{}
				}
				if scenario == "foreign_source" {
					value.SourceWitness = "different"
				}
				return value
			}
			if scenario == "authored_claim" {
				readers.Skills = nil
			}
			input := Input{Roster: Collect(t.Context(), "local", readers), Roles: []Role{{ID: "manager", Scope: "home", Purpose: "Coordinate music projects", RequiredSkills: []string{"catalog"}}}}
			bounded, prompt, err := Prepare(input)
			if err != nil {
				t.Fatal(err)
			}
			candidate := bounded.Roster.Candidates[0]
			if strings.Contains(prompt, profile.Witness) || strings.Contains(prompt, strings.Repeat("e", 64)) {
				t.Fatal("private witnesses reached ranking input")
			}
			proposal := Proposal{Roles: []RoleProposal{{RoleID: "manager", State: Recommended, Choices: []Choice{{Candidate: candidate.Handle, Evidence: []string{candidate.Evidence[0].ID}}}}}}
			result, err := Validate(bounded, proposal)
			if (err == nil) != (scenario == "enabled") {
				t.Fatal("authored or unknown skill state became eligibility", scenario, err)
			}
			if scenario == "enabled" && result.State != Recommended {
				t.Fatal("witnessed member rejected")
			}
			noFit := Proposal{Roles: []RoleProposal{{RoleID: "manager", State: NoSuitableMatch}}}
			result, err = Validate(bounded, noFit)
			if err != nil {
				t.Fatal(err)
			}
			if (scenario == "unknown" || scenario == "foreign_source" || scenario == "authored_claim") && result.State != NeedsChoice {
				t.Fatal("unknown membership became exhaustive no-fit")
			}
		})
	}
}
