package learningmissions

import (
	"context"
	"testing"
	"time"
)

func advanceMilestoneClock(s *Service, delay time.Duration) {
	now := s.now().Add(delay)
	s.now = func() time.Time { return now }
}

func TestAutomaticMilestoneReservesOneBoundedCallAndNeverReplays(t *testing.T) {
	s, designer, out, _ := milestoneFixture(t, true)
	id := out.Operation.MilestoneID
	if err := s.ProcessMilestone(t.Context(), "local", id); err != nil || designer.calls.Load() != 0 {
		t.Fatal("debounce dispatched early", err)
	}
	advanceMilestoneClock(s, MilestoneDelay)
	designer.generate = func(_ context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
		claim, err := s.store.Operation(t.Context(), "local", "auto-"+id)
		event, _ := s.store.Operation(t.Context(), "local", id)
		if err != nil || claim.Status != "pending" || !claim.Automatic || claim.MilestoneID != id || !validDigest(claim.CandidateFingerprint) || claim.ReservedTokens != GenerationReservedTokens || event.Status != "succeeded" {
			t.Fatal("model called before durable automatic reservation", claim, err)
		}
		return designReply(snapshot), nil
	}
	for range 2 {
		if err := s.ProcessMilestone(t.Context(), "local", id); err != nil {
			t.Fatal(err)
		}
	}
	claim, _ := s.store.Operation(t.Context(), "local", "auto-"+id)
	instance, err := s.store.Instance(t.Context(), "local", claim.InstanceID)
	if err != nil || designer.calls.Load() != 1 || !instance.Definition.Automatic || !instance.Definition.Personalized || instance.State != Offered || instance.Progress.TicketID != "" {
		t.Fatal("automatic generation created work, replayed, or lost provenance", err)
	}
}

func TestAutomaticMilestoneRechecksConsentSourceModelActiveWorkAndExpiry(t *testing.T) {
	for _, reason := range []string{"off", "model", "source", "offer", "decline", "expired"} {
		t.Run(reason, func(t *testing.T) {
			s, designer, out, _ := milestoneFixture(t, true)
			advanceMilestoneClock(s, MilestoneDelay)
			owner, _ := s.store.Owner(t.Context(), "local")
			switch reason {
			case "off":
				if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "off", ExpectedRevision: owner.Revision}); err != nil {
					t.Fatal(err)
				}
			case "model":
				designer.config.Fingerprint = digest([]byte("different model"))
			case "source":
				s.source.(milestoneReviewSource).missing = true
			case "offer", "decline":
				offer, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "explicit-priority", ExpectedRevision: owner.Revision}, WorkspaceID: "workspace"})
				if err != nil {
					t.Fatal(err)
				}
				if reason == "decline" {
					if _, err := s.Decide(t.Context(), "local", offer.Instance.ID, DecisionRequest{MutationRequest: MutationRequest{OperationID: "decline", ExpectedRevision: owner.Revision + 1}, InstanceRevision: 1, Decision: "decline"}); err != nil {
						t.Fatal(err)
					}
				}
			case "expired":
				advanceMilestoneClock(s, MilestoneLifetime)
			}
			if err := s.ProcessMilestone(t.Context(), "local", out.Operation.MilestoneID); err != nil {
				t.Fatal(err)
			}
			event, _ := s.store.Operation(t.Context(), "local", out.Operation.MilestoneID)
			if event.Status != "invalidated" || designer.calls.Load() != 0 {
				t.Fatal("ineligible milestone called the model", reason, event)
			}
		})
	}
}

func TestAutomaticMilestoneCadenceAndCandidateCoalescingIncludeUnknownCalls(t *testing.T) {
	for _, reason := range []string{"daily", "candidate"} {
		t.Run(reason, func(t *testing.T) {
			s, designer, out, _ := milestoneFixture(t, true)
			advanceMilestoneClock(s, MilestoneDelay)
			owner, _ := s.store.Owner(t.Context(), "local")
			snapshot, err := BuildContext(t.Context(), s.source, owner, s.now())
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err = s.rankContext(t.Context(), owner, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, err := automaticCandidateFingerprint(owner, snapshot, designer.config)
			if err != nil {
				t.Fatal(err)
			}
			created := s.now().Add(-25 * time.Hour).UnixMilli()
			if reason == "daily" {
				created, fingerprint = s.now().Add(-time.Hour).UnixMilli(), digest([]byte("other candidate"))
			}
			_, err = s.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(m *Mutation) error {
				return m.InsertOperation(Operation{ID: "unknown-earlier", Kind: "generate", Status: "unknown", Epoch: owner.Epoch, RequestDigest: digest([]byte("earlier")), CreatedAt: created,
					Automatic: true, MilestoneID: "earlier-event", CandidateFingerprint: fingerprint, ReservedTokens: GenerationReservedTokens, ModelFingerprint: designer.config.Fingerprint})
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ProcessMilestone(t.Context(), "local", out.Operation.MilestoneID); err != nil {
				t.Fatal(err)
			}
			if designer.calls.Load() != 0 {
				t.Fatal("unknown call or candidate fingerprint was forgotten")
			}
		})
	}
}

func TestAutomaticMilestoneHeldReplyCannotPublishAfterRevocation(t *testing.T) {
	s, designer, out, _ := milestoneFixture(t, true)
	advanceMilestoneClock(s, MilestoneDelay)
	entered, release := make(chan struct{}), make(chan struct{})
	designer.generate = func(_ context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
		close(entered)
		<-release
		return designReply(snapshot), nil
	}
	done := make(chan error, 1)
	go func() { done <- s.ProcessMilestone(t.Context(), "local", out.Operation.MilestoneID) }()
	<-entered
	owner, _ := s.store.Owner(t.Context(), "local")
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "revoke-held", ExpectedRevision: owner.Revision}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	claim, _ := s.store.Operation(t.Context(), "local", "auto-"+out.Operation.MilestoneID)
	if claim.Status != "invalidated" || claim.InstanceID != "" || claim.ReservedTokens != GenerationReservedTokens || designer.calls.Load() != 1 {
		t.Fatal("late reply published or lost budget", claim)
	}
}
