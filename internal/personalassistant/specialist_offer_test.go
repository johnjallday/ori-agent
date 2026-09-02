package personalassistant

import (
	"context"
	"errors"
	"testing"
)

func offerFixture(t *testing.T, status RelationshipStatus) (*SpecialistOfferService, Store) {
	t.Helper()
	store, _ := newTestStore(t)
	state := activeTestState("local", "assistant-a")
	state.Status = status
	if status == StatusAwaitingHQ || status == StatusProvisioningHQ {
		// A hired-but-unbuilt relationship carries no HQ identifiers yet, and
		// the store enforces that. A claimed setup additionally needs its
		// operation receipt.
		state.HQWorkspaceID, state.HQEntryAgentInstanceID = "", ""
		if status == StatusProvisioningHQ {
			state.LastHQRequestID = "hq-request-1"
			state.HQPayloadJSON = `{"request_id":"hq-request-1"}`
			state.HQPayloadHash = PayloadHash([]byte(state.HQPayloadJSON))
		}
	}
	if _, err := store.CreateState(context.Background(), state); err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	return NewSpecialistOfferService(store), store
}

func TestSpecialistOffer_AcceptRecordsTheDomainAndReshapesFocus(t *testing.T) {
	ctx := context.Background()
	service, store := offerFixture(t, StatusActive)

	updated, err := service.Answer(ctx, "local", SpecialistOfferRequest{
		Decision: "accepted", Slug: "music_production",
	})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if updated.SpecialistSlug != "music_production" {
		t.Fatalf("slug = %q", updated.SpecialistSlug)
	}
	if updated.SpecialistOfferState != SpecialistOfferAccepted {
		t.Fatalf("offer state = %q", updated.SpecialistOfferState)
	}
	// Saying yes visibly changes what the assistant says it is for.
	want := []FocusArea{FocusPlanMyDay, FocusTrackSongsInProgress, FocusChaseCollaboratorHandoffs}
	if len(updated.FocusAreas) != len(want) {
		t.Fatalf("focus areas = %v", updated.FocusAreas)
	}
	for i, area := range want {
		if updated.FocusAreas[i] != area {
			t.Fatalf("focus area %d = %q, want %q", i, updated.FocusAreas[i], area)
		}
	}

	persisted, err := store.GetState(ctx, "local")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if persisted.SpecialistSlug != "music_production" ||
		persisted.SpecialistOfferState != SpecialistOfferAccepted {
		t.Fatalf("persisted = %+v", persisted)
	}
}

// Declining is remembered. An empty slug alone cannot say "asked and
// answered", which is why the offer state exists.
func TestSpecialistOffer_DeclineIsRememberedAndChangesNothingElse(t *testing.T) {
	ctx := context.Background()
	service, _ := offerFixture(t, StatusActive)

	updated, err := service.Answer(ctx, "local", SpecialistOfferRequest{Decision: "declined"})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if updated.SpecialistOfferState != SpecialistOfferDeclined {
		t.Fatalf("offer state = %q", updated.SpecialistOfferState)
	}
	if updated.SpecialistSlug != "" {
		t.Fatalf("declining recorded a specialist: %q", updated.SpecialistSlug)
	}
	// The working agreement is untouched.
	if len(updated.FocusAreas) != 2 || updated.FocusAreas[0] != FocusPlanMyDay {
		t.Fatalf("declining changed the focus areas: %v", updated.FocusAreas)
	}
}

func TestSpecialistOffer_RejectsBadAnswers(t *testing.T) {
	ctx := context.Background()
	service, _ := offerFixture(t, StatusActive)

	cases := map[string]SpecialistOfferRequest{
		"no decision":       {Decision: ""},
		"unknown decision":  {Decision: "maybe"},
		"accept no slug":    {Decision: "accepted"},
		"accept bad slug":   {Decision: "accepted", Slug: "not_a_domain"},
		"accept empty slug": {Decision: "accepted", Slug: "   "},
	}
	for name, request := range cases {
		if _, err := service.Answer(ctx, "local", request); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: err = %v, want a validation error", name, err)
		}
	}
}

// The offer belongs to a relationship that exists. Before a hire there is
// nothing to shape.
func TestSpecialistOffer_RefusedBeforeAHire(t *testing.T) {
	ctx := context.Background()
	for _, status := range []RelationshipStatus{StatusNotHired, StatusHiring, StatusRepairNeeded} {
		service, _ := offerFixture(t, status)
		_, err := service.Answer(ctx, "local", SpecialistOfferRequest{
			Decision: "accepted", Slug: "music_production",
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("status %q: err = %v, want a conflict", status, err)
		}
	}
}

// A hired assistant with no HQ yet can still be asked.
func TestSpecialistOffer_AllowedBeforeHQExists(t *testing.T) {
	ctx := context.Background()
	for _, status := range []RelationshipStatus{StatusAwaitingHQ, StatusProvisioningHQ, StatusPaused} {
		service, _ := offerFixture(t, status)
		if _, err := service.Answer(ctx, "local", SpecialistOfferRequest{
			Decision: "accepted", Slug: "music_production",
		}); err != nil {
			t.Fatalf("status %q: %v", status, err)
		}
	}
}

// A double click or a replayed request is a no-op, not a failure. A different
// answer after the fact is a conflict: the question was already settled.
func TestSpecialistOffer_IsAnsweredExactlyOnce(t *testing.T) {
	ctx := context.Background()
	service, _ := offerFixture(t, StatusActive)

	first, err := service.Answer(ctx, "local", SpecialistOfferRequest{
		Decision: "accepted", Slug: "music_production",
	})
	if err != nil {
		t.Fatalf("first answer: %v", err)
	}
	replay, err := service.Answer(ctx, "local", SpecialistOfferRequest{
		Decision: "accepted", Slug: "music_production",
	})
	if err != nil {
		t.Fatalf("replayed answer: %v", err)
	}
	if replay.StateVersion != first.StateVersion {
		t.Fatalf("a replay wrote again: %d -> %d", first.StateVersion, replay.StateVersion)
	}

	if _, err := service.Answer(ctx, "local", SpecialistOfferRequest{Decision: "declined"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("changing the answer: err = %v, want a conflict", err)
	}
}

func TestSpecialistOffer_RejectsAStaleVersion(t *testing.T) {
	ctx := context.Background()
	service, _ := offerFixture(t, StatusActive)

	_, err := service.Answer(ctx, "local", SpecialistOfferRequest{
		IfVersion: 99, Decision: "accepted", Slug: "music_production",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
}
