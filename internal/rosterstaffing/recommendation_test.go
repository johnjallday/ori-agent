package rosterstaffing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func rankingFixture(t *testing.T) (Input, Proposal) {
	t.Helper()
	roster := Collect(context.Background(), "local", candidateReaders(testProfile("Renamed manager"), testProfile("Portfolio Manager")))
	input := Input{Roster: roster, Roles: []Role{{ID: "manager", Scope: "home", Purpose: "Coordinate a music portfolio", RequiredCapabilities: []string{"planning"}}}}
	bounded, _, err := Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	c := bounded.Roster.Candidates[0]
	return bounded, Proposal{Roles: []RoleProposal{{RoleID: "manager", State: Recommended, Choices: []Choice{{Candidate: c.Handle, Evidence: []string{c.Evidence[0].ID}}}}}}
}

func TestRecommendationValidatesOnlyHostEvidence(t *testing.T) {
	input, proposal := rankingFixture(t)
	result, err := Validate(input, proposal)
	if err != nil || result.State != Recommended {
		t.Fatalf("valid recommendation: %+v %v", result, err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "private-witness") || strings.Contains(string(data), "manager's performance") {
		t.Fatal("private or invented material persisted")
	}
}

func TestRecommendationRejectsInventedOrIneligibleChoices(t *testing.T) {
	for _, mode := range []string{"foreign_candidate", "wrong_role", "wrong_scope", "foreign_evidence", "duplicate_evidence", "missing_evidence", "fake_capability", "unconfigured", "occupied", "too_many_alternatives", "invented_state", "duplicate_role", "no_fit_with_choice"} {
		t.Run(mode, func(t *testing.T) {
			input, proposal := rankingFixture(t)
			choice := &proposal.Roles[0].Choices[0]
			switch mode {
			case "foreign_candidate":
				choice.Candidate = "c_foreign"
			case "wrong_role", "wrong_scope":
				proposal.Roles[0].RoleID = "project_manager"
			case "foreign_evidence":
				choice.Evidence[0] = input.Roster.Candidates[1].Evidence[0].ID
			case "duplicate_evidence":
				choice.Evidence = append(choice.Evidence, choice.Evidence[0])
			case "missing_evidence":
				choice.Evidence = nil
			case "fake_capability":
				input.Roles[0].RequiredCapabilities = []string{"live_daw_access"}
			case "unconfigured":
				input.Roster.Candidates[0].Ready = false
			case "occupied":
				input.Roles[0].Occupied = true
			case "too_many_alternatives":
				for i := 0; i < 4; i++ {
					proposal.Roles[0].Choices = append(proposal.Roles[0].Choices, *choice)
				}
			case "invented_state":
				proposal.Roles[0].State = "high_confidence"
			case "duplicate_role":
				proposal.Roles = append(proposal.Roles, proposal.Roles[0])
			case "no_fit_with_choice":
				proposal.Roles[0].State = NoSuitableMatch
			}
			if _, err := Validate(input, proposal); !errors.Is(err, ErrInvalidRecommendation) {
				t.Fatalf("accepted %s", mode)
			}
		})
	}
}

func TestRecommendationHonestLowEvidenceAndCoverage(t *testing.T) {
	for _, mode := range []string{"partial_recommendation", "partial_no_match", "unknown_purpose", "generic_role_only", "tie"} {
		t.Run(mode, func(t *testing.T) {
			input, proposal := rankingFixture(t)
			switch mode {
			case "partial_recommendation":
				input.Roster.Coverage.Complete = false
			case "partial_no_match":
				input.Roster.Coverage.Complete = false
				proposal.Roles[0].State = NoSuitableMatch
				proposal.Roles[0].Choices = nil
			case "unknown_purpose":
				input.Roster.Candidates[0].Evidence = input.Roster.Candidates[0].Evidence[1:]
				proposal.Roles[0].State = NoSuitableMatch
				proposal.Roles[0].Choices = nil
			case "generic_role_only":
				proposal.Roles[0].Choices[0].Evidence = []string{input.Roster.Candidates[0].Evidence[1].ID}
			case "tie":
				proposal.Roles[0].State = NeedsChoice
			}
			result, err := Validate(input, proposal)
			if err != nil || result.State != NeedsChoice {
				t.Fatalf("dishonest outcome: %+v %v", result, err)
			}
		})
	}
	input, proposal := rankingFixture(t)
	proposal.Roles[0].Choices = nil
	proposal.Roles[0].State = NoSuitableMatch
	if got, err := Validate(input, proposal); err != nil || got.State != NoSuitableMatch {
		t.Fatalf("complete no match: %+v %v", got, err)
	}
}

func TestRecommendationDecodeRejectsUnsupportedClaimsAndMalformedOutput(t *testing.T) {
	for _, raw := range []string{
		`{"roles":[],"confidence":0.99}`,
		`{"roles":[{"role_id":"manager","state":"recommended","choices":[],"reason":"Best performance"}]}`,
		`{"roles":[]} trailing`, `{"roles":[]} {}`, `not json`, strings.Repeat("x", MaxReplyBytes+1),
	} {
		if _, err := DecodeProposal([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid output: %.100s", raw)
		}
	}
}

func TestRecommendationPromptBudgetsAndPrivateCanaries(t *testing.T) {
	input, _ := rankingFixture(t)
	input.Roles[0].Purpose = "Do not use /private/ROLE_CANARY"
	input.Goal = "api_key=GOAL_CANARY"
	for i := 0; i < MaxCandidates; i++ {
		c := input.Roster.Candidates[0]
		c.Evidence = []Evidence{{ID: c.Handle + "_e1", Kind: "declared_purpose", Value: strings.Repeat("long declared purpose ", 100)}}
		input.Roster.Candidates = append(input.Roster.Candidates, c)
	}
	bounded, prompt, err := Prepare(input)
	if err != nil || len(prompt) > MaxPromptBytes || bounded.Roster.Coverage.Complete {
		t.Fatalf("budget failed: %d %v", len(prompt), err)
	}
	for _, canary := range []string{"ROLE_CANARY", "GOAL_CANARY", "private-witness", "Renamed manager", "Portfolio Manager"} {
		if strings.Contains(prompt, canary) {
			t.Fatalf("canary in prompt: %s", canary)
		}
	}
	if !strings.Contains(prompt, "untrusted reference data") {
		t.Fatal("missing data boundary")
	}
}

func TestRecommendBoundedRetryAndCancellation(t *testing.T) {
	input, proposal := rankingFixture(t)
	valid, _ := json.Marshal(proposal)
	for _, mode := range []string{"success", "malformed_then_valid", "malformed", "provider_failure", "cancelled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, time.Millisecond)
				defer stop()
			}
			got := Recommend(ctx, input, func(ctx context.Context, prompt string) ([]byte, error) {
				calls++
				if strings.Contains(prompt, "OUTPUT_CANARY") {
					t.Fatal("untrusted response echoed in retry")
				}
				switch mode {
				case "provider_failure":
					return nil, errors.New("private provider error")
				case "malformed":
					return []byte("OUTPUT_CANARY"), nil
				case "malformed_then_valid":
					if calls == 1 {
						return []byte("OUTPUT_CANARY"), nil
					}
				case "deadline":
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return valid, nil
			})
			wantCalls, wantState := 1, Unavailable
			if mode == "success" {
				wantState = Recommended
			}
			if mode == "malformed_then_valid" {
				wantCalls, wantState = 2, Recommended
			}
			if mode == "malformed" {
				wantCalls = 2
			}
			if mode == "cancelled" {
				wantCalls = 0
			}
			if calls != wantCalls || got.State != wantState {
				t.Fatalf("calls=%d state=%s", calls, got.State)
			}
		})
	}
	if got := Recommend(context.Background(), input, nil); got.State != Unavailable {
		t.Fatal("missing provider is not unavailable")
	}
}
