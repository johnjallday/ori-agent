package learningmissions

import (
	"slices"
	"strings"
	"testing"
)

func policyFixture() ([]EligibleCandidate, policyContext) {
	candidates := []EligibleCandidate{}
	for _, structure := range []StructureID{DelegateInspect, ImproveResult, RepeatSuccess} {
		candidate := EligibleCandidate{Candidate: Candidate{Ref: string(structure), WorkspaceID: "ws", Incarnation: "local-source", ScopeDigest: strings.Repeat("a", 64), Name: "Workshop"}, Structure: structure}
		if structure != DelegateInspect {
			candidate.OriginalReceipt = "local-review"
		}
		candidates = append(candidates, candidate)
	}
	return candidates, policyContext{WorkspaceIDs: []string{"ws"}, Mode: "on_request", Effort: "small", Now: 1000}
}

func TestRecommendationPolicyEligibilityPrecedesRanking(t *testing.T) {
	for _, scenario := range []string{"off", "active", "scope", "thin", "missing-original", "unsupported", "existing-review", "unknown-mode", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			candidates, policy := policyFixture()
			switch scenario {
			case "off":
				policy.Mode = "off"
			case "active":
				policy.Active = true
			case "scope":
				policy.WorkspaceIDs = []string{"other"}
			case "thin":
				candidates = nil
			case "missing-original":
				candidates = candidates[1:]
				for n := range candidates {
					candidates[n].OriginalReceipt = ""
				}
			case "unsupported":
				candidates = candidates[:1]
				candidates[0].Structure = "invented"
			case "existing-review":
				policy.ReviewableWork = true
			case "unknown-mode":
				policy.Mode = "invented"
			case "oversized":
				for len(candidates) <= MaxCandidates {
					candidates = append(candidates, candidates[0])
				}
			}
			if got := rankCandidates(candidates, policy); len(got) != 0 {
				t.Fatalf("ineligible work ranked: %+v", got)
			}
		})
	}
}

func TestRecommendationPolicyUsesExplicitFocusAndKnownPracticeNotActivityOrTime(t *testing.T) {
	candidates, policy := policyFixture()
	policy.Focus = "Music for my workshop"
	candidates[1].Name = "Music workshop"
	// Other candidates have no direct focus word, avoiding a tie on workshop.
	candidates[0].Name, candidates[2].Name = "Writing", "Writing"
	got := rankCandidates(candidates, policy)
	if len(got) != 3 || got[0].Candidate.Structure != ImproveResult || !slices.Contains(got[0].Signals, "explicit_focus_words_match") {
		t.Fatalf("focus was ignored: %+v", got)
	}
	for _, row := range got {
		if slices.Contains(row.Signals, "no_recorded_practice_for_structure") {
			t.Fatal("unknown was labelled no practice")
		}
	}
	policy.Focus = ""
	policy.PracticeKnown, policy.Practice = true, map[string]int{"delegate": 2, "reuse": 1}
	got = rankCandidates(candidates, policy)
	if got[0].Candidate.Structure != ImproveResult {
		t.Fatal("needed practice lost to a shorter already-practiced structure")
	}
	policy.PracticeKnown = false
	got = rankCandidates(candidates, policy)
	policy.Now += 365 * 24 * 60 * 60 * 1000
	later := rankCandidates(candidates, policy)
	if got[0].Candidate.Ref != later[0].Candidate.Ref {
		t.Fatal("inactivity changed practice selection")
	}
	if explicitFocusOverlap("art", "smart projects") || explicitFocusOverlap("the and for", "the workspace") {
		t.Fatal("substring/common words manufactured an interest")
	}
}

func TestRecommendationPolicyDeclinePersistsButDeferExpiresAndTopicsAreExplicit(t *testing.T) {
	candidates, policy := policyFixture()
	policy.Exclusions = []RecommendationExclusion{{WorkspaceID: "ws", Structure: DelegateInspect}, {WorkspaceID: "ws", Structure: RepeatSuccess, Until: 1500}}
	got := rankCandidates(candidates, policy)
	if len(got) != 1 || got[0].Candidate.Structure != ImproveResult {
		t.Fatalf("suppression ignored: %+v", got)
	}
	policy.Now = 1500
	got = rankCandidates(candidates, policy)
	if len(got) != 2 {
		t.Fatal("defer did not expire or decline expired")
	}
	for n := range candidates {
		candidates[n].Incarnation = "reimported-source"
	}
	if len(rankCandidates(candidates, policy)) != 2 {
		t.Fatal("reimport silently reset a preference")
	}
	policy.Exclusions = []RecommendationExclusion{{WorkspaceID: "ws", Structure: DelegateInspect, Topic: recommendationTopic("  MUSIC  ")}}
	policy.Focus = "music"
	if len(rankCandidates(candidates, policy)) != 2 {
		t.Fatal("equivalent focus evaded decline")
	}
	policy.Focus = "gardening"
	if len(rankCandidates(candidates, policy)) != 3 {
		t.Fatal("one explicit topic suppressed unrelated focus")
	}
}

func TestRecommendationPolicyPreservesExplicitStructureAndAvoidsDuplicateAlternatives(t *testing.T) {
	candidates, policy := policyFixture()
	policy.Requested = ImproveResult
	policy.ReviewableWork = true // an explicit different structure is still a choice
	got := rankCandidates(candidates, policy)
	if len(got) != 1 || got[0].Candidate.Structure != ImproveResult {
		t.Fatal("explicit selection was rewritten")
	}
	policy.AvoidRef = got[0].Candidate.Ref
	if len(rankCandidates(candidates, policy)) != 0 {
		t.Fatal("same candidate offered as something else")
	}
	policy.Requested, policy.AvoidRef, policy.ReviewableWork = "", "", false
	policy.Effort = "standard"
	policy.Recent = []StructureID{DelegateInspect, DelegateInspect}
	got = rankCandidates(candidates, policy)
	if got[0].Candidate.Structure == DelegateInspect {
		t.Fatal("eligible alternatives ignored recent repetition")
	}
	candidates = append(candidates, candidates[0])
	if len(rankCandidates(candidates, policy)) != 3 {
		t.Fatal("duplicate candidate produced duplicate recommendations")
	}
}

func TestRecommendationPolicyRefusesInvalidOrUnboundedExclusionState(t *testing.T) {
	candidates, policy := policyFixture()
	for _, bad := range []RecommendationExclusion{
		{WorkspaceID: "ws", Structure: DelegateInspect, Until: -1},
		{WorkspaceID: "ws", Structure: "invented"},
		{WorkspaceID: "ws", Structure: DelegateInspect, Topic: "unverified"},
	} {
		policy.Exclusions = []RecommendationExclusion{bad}
		if len(rankCandidates(candidates, policy)) != 0 {
			t.Fatal("invalid preferences silently forgotten")
		}
	}
	policy.Exclusions = make([]RecommendationExclusion, MaxRecommendationExclusions+1)
	if len(rankCandidates(candidates, policy)) != 0 {
		t.Fatal("unbounded preference scan")
	}
}
