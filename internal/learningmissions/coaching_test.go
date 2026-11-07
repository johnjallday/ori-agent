package learningmissions

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCoachingBudgetRespectsExplicitDepthAndOnlyKnownIndependentPathPractice(t *testing.T) {
	for _, tc := range []struct {
		name        string
		known       bool
		independent map[string]int
		guidance    Guidance
		full        bool
		structure   StructureID
		depth       string
		limit       int
	}{
		{"new", true, nil, GuidanceGuided, false, DelegateInspect, "full", 3},
		{"unknown", false, map[string]int{"delegate": 20, "evaluate": 20}, GuidanceGuided, false, DelegateInspect, "full", 3},
		{"one_is_not_two", true, map[string]int{"delegate": 1, "evaluate": 2}, GuidanceGuided, false, DelegateInspect, "full", 3},
		{"practiced", true, map[string]int{"delegate": 2, "evaluate": 2}, GuidanceGuided, false, DelegateInspect, "brief", 1},
		{"full_override", true, map[string]int{"delegate": 2, "evaluate": 2}, GuidanceGuided, true, DelegateInspect, "full", 3},
		{"basic_override", true, nil, GuidanceBasic, true, DelegateInspect, "basic", 0},
		{"other_structure_is_not_transfer", true, map[string]int{"delegate": 2, "evaluate": 2}, GuidanceGuided, false, ImproveResult, "full", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			depth, limit := coachingBudget(Preferences{Guidance: tc.guidance, FullGuidance: tc.full}, policyContext{PracticeKnown: tc.known, Independent: tc.independent, Practice: map[string]int{"delegate": 100, "evaluate": 100}}, tc.structure)
			if depth != tc.depth || limit != tc.limit {
				t.Fatalf("%s %d", depth, limit)
			}
		})
	}
}

func TestGeneratedBriefCoachingRetainsGuidedSupportAndEveryCompiledCriterion(t *testing.T) {
	snapshot, model, raw := designFixture(t)
	snapshot.Data.Guidance, snapshot.Data.CoachingDepth, snapshot.Data.MaxCoachingHints = GuidanceGuided, "brief", 1
	raw = strings.Replace(raw, `"hints":[]`, `"hints":["Compare the result with the text you supplied"]`, 1)
	d, err := ValidateProposal(raw, snapshot, model, time.Now())
	if err != nil || d.CoachingDepth != "brief" || d.Guidance != GuidanceGuided || len(d.Steps) != 3 || len(d.Hints) != 1 {
		t.Fatal(d, err)
	}
	tooMany := strings.Replace(raw, `"hints":[`, `"hints":["Another optional hint",`, 1)
	if _, err := ValidateProposal(tooMany, snapshot, model, time.Now()); err == nil {
		t.Fatal("model expanded host coaching budget")
	}
	snapshot.Data.Guidance, snapshot.Data.CoachingDepth, snapshot.Data.MaxCoachingHints = GuidanceBasic, "basic", 0
	if _, err := ValidateProposal(raw, snapshot, model, time.Now()); err == nil {
		t.Fatal("model overrode explicit basic guidance")
	}
}

func TestScopedCoachingMetadataNeverPromotesHistoricalFutureOrUnknownSupport(t *testing.T) {
	s, db := testService(t)
	_, err := s.store.Change(t.Context(), "local", 0, 0, true, func(m *Mutation) error {
		for n := range 2 {
			i, proof := resultFixture()
			i.ID = fmt.Sprintf("instance-%d", n)
			i.Definition.Guidance, i.Definition.SupportVersion = GuidanceBasic, GuidanceProtocolVersion
			proof.Ref.TicketID, proof.Ref.RunID, proof.Ref.ArtifactID = fmt.Sprintf("ticket-%d", n), fmt.Sprintf("run-%d", n), fmt.Sprintf("artifact-%d", n)
			i.Progress.Result = &proof.Ref
			r := Review{ID: fmt.Sprintf("review-%d", n), InstanceID: i.ID, Revision: i.Revision, Epoch: i.Epoch, Result: proof.Ref, InputDigest: i.Progress.InputDigest, Structure: DelegateInspect, Version: 1, Guidance: GuidanceBasic, SupportVersion: GuidanceProtocolVersion, CreatedAt: proof.FinishedAt + 1, ExpiresAt: proof.FinishedAt + ReviewLifetime.Milliseconds()}
			if err := m.InsertReview(r); err != nil {
				return err
			}
			r.Decision, r.Reason, r.DecidedAt = "reject", "incorrect", r.CreatedAt
			if err := m.ConsumeReview(r); err != nil {
				return err
			}
			evidence, err := PracticeEvidence("local", i, r)
			if err != nil {
				return err
			}
			for _, e := range evidence {
				if _, err := m.Credit(e); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	owner = generationOwner(owner, "workspace")
	candidates := []EligibleCandidate{{Candidate: Candidate{WorkspaceID: "workspace", Incarnation: "local-install-1"}, Structure: DelegateInspect}}
	check := func(want string) {
		t.Helper()
		policy, err := s.recommendationContext(t.Context(), owner, candidates, DelegateInspect)
		if err != nil {
			t.Fatal(err)
		}
		depth, _ := coachingBudget(owner.Preferences, policy, DelegateInspect)
		if depth != want {
			t.Fatal("depth", depth, "want", want, policy)
		}
	}
	check("brief")
	for _, version := range []int{0, GuidanceProtocolVersion + 1} {
		if _, err := db.ExecContext(t.Context(), `UPDATE learning_evidence SET record=json_set(record,'$.support_version',?)`, version); err != nil {
			t.Fatal(err)
		}
		check("full")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_evidence SET record=json_set(record,'$.support_version',1,'$.guidance','unknown')`); err != nil {
		t.Fatal(err)
	}
	check("full")
	if _, err := db.ExecContext(t.Context(), `UPDATE learning_evidence SET record=json_set(record,'$.guidance','basic')`); err != nil {
		t.Fatal(err)
	}
	candidates[0].Incarnation = "reimported-incarnation"
	check("full")
}
