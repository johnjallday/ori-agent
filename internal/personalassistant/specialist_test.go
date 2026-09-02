package personalassistant

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

func TestSpecialistSlugSurvivesAStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	state := activeTestState("user-specialist", "assistant-specialist")
	state.SpecialistSlug = "music_production"
	if _, err := store.CreateState(ctx, state); err != nil {
		t.Fatalf("CreateState: %v", err)
	}

	// Read back through a fresh query — the point of the column is that a
	// post-hire surface can read the answer without re-running detection.
	loaded, err := store.GetState(ctx, "user-specialist")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if loaded.SpecialistSlug != "music_production" {
		t.Fatalf("specialist slug = %q, want music_production", loaded.SpecialistSlug)
	}

	loaded.SpecialistSlug = ""
	updated, err := store.UpdateState(ctx, loaded, loaded.StateVersion)
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	if updated.SpecialistSlug != "" {
		t.Fatalf("cleared specialist slug = %q, want empty", updated.SpecialistSlug)
	}
}

// An existing relationship predates the column entirely. It must read as "no
// specialist" and behave exactly as it does today, with no backfill.
func TestRelationshipWithoutASpecialistReadsAsGeneric(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	if _, err := store.CreateState(ctx, activeTestState("user-generic", "assistant-generic")); err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	loaded, err := store.GetState(ctx, "user-generic")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if loaded.SpecialistSlug != "" {
		t.Fatalf("specialist slug = %q, want empty", loaded.SpecialistSlug)
	}
}

func TestNormalizeSpecialistSlugRejectsUnknownValues(t *testing.T) {
	if got, err := NormalizeSpecialistSlug(""); err != nil || got != "" {
		t.Fatalf("empty slug = %q, %v; want the generic relationship", got, err)
	}
	if got, err := NormalizeSpecialistSlug("  music_production  "); err != nil || got != "music_production" {
		t.Fatalf("trimmed slug = %q, %v", got, err)
	}
	for _, raw := range []string{"reaper", "MUSIC_PRODUCTION", "music production", "../etc", "not_a_domain"} {
		if _, err := NormalizeSpecialistSlug(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestHireRequestRejectsAnUnknownSpecialistSlug(t *testing.T) {
	request := validHireRequest()
	request.SpecialistSlug = "not_a_domain"
	_, err := normalizeHireRequest(request)
	if err == nil {
		t.Fatal("expected an unknown specialist slug to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid hire request") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHireRequestCarriesAnAcceptedSpecialistSlug(t *testing.T) {
	request := validHireRequest()
	request.SpecialistSlug = "music_production"
	normalized, err := normalizeHireRequest(request)
	if err != nil {
		t.Fatalf("normalizeHireRequest: %v", err)
	}
	if normalized.SpecialistSlug != "music_production" {
		t.Fatalf("normalized slug = %q", normalized.SpecialistSlug)
	}
}

// Ignoring the offer entirely is a complete, valid hire on the generic path.
func TestHireRequestWithoutASpecialistIsValid(t *testing.T) {
	normalized, err := normalizeHireRequest(validHireRequest())
	if err != nil {
		t.Fatalf("normalizeHireRequest: %v", err)
	}
	if normalized.SpecialistSlug != "" {
		t.Fatalf("normalized slug = %q, want empty", normalized.SpecialistSlug)
	}
}

// Accepting the offer must not make the hire heavier. It creates the same one
// Personal HQ, designates it once, writes one Daily Brief config, and runs no
// setup wizard — exactly what the generic path does.
func TestAcceptingASpecialistDoesNotChangeTheHireTransaction(t *testing.T) {
	ctx := context.Background()

	run := func(t *testing.T, slug string) (*fakeAssistantCreator, *fakeHireHQ, *fakeHireBriefs, *HireResult) {
		t.Helper()
		store, _ := newTestStore(t)
		creator := &fakeAssistantCreator{}
		hq := &fakeHireHQ{}
		briefs := &fakeHireBriefs{getErr: dailybrief.ErrConfigNotFound}
		request := validHireRequest()
		request.SpecialistSlug = slug
		result, err := NewHireCoordinator(store, creator, hq, briefs).Hire(ctx, "local", request)
		if err != nil {
			t.Fatalf("Hire(slug=%q): %v", slug, err)
		}
		return creator, hq, briefs, result
	}

	genericCreator, genericHQ, genericBriefs, generic := run(t, "")
	producerCreator, producerHQ, producerBriefs, producer := run(t, "music_production")

	if producerCreator.calls != genericCreator.calls || producerCreator.calls != 1 {
		t.Fatalf("workspace creations: producer=%d generic=%d", producerCreator.calls, genericCreator.calls)
	}
	if producerHQ.designateCalls != genericHQ.designateCalls || producerHQ.onboardingCalls != genericHQ.onboardingCalls {
		t.Fatalf("hq calls: producer=(%d,%d) generic=(%d,%d)",
			producerHQ.designateCalls, producerHQ.onboardingCalls,
			genericHQ.designateCalls, genericHQ.onboardingCalls)
	}
	if producerBriefs.updateCalls != genericBriefs.updateCalls {
		t.Fatalf("brief updates: producer=%d generic=%d", producerBriefs.updateCalls, genericBriefs.updateCalls)
	}
	// The creation options are identical apart from nothing at all: the
	// specialist never reaches the workspace creator.
	if producerCreator.seen.SystemPromptFragment != genericCreator.seen.SystemPromptFragment ||
		producerCreator.seen.Role != genericCreator.seen.Role {
		t.Fatalf("creation options diverged: producer=%#v generic=%#v", producerCreator.seen, genericCreator.seen)
	}
	if generic.State.SpecialistSlug != "" {
		t.Fatalf("generic hire recorded a specialist: %q", generic.State.SpecialistSlug)
	}
	if producer.State.SpecialistSlug != "music_production" {
		t.Fatalf("producer hire specialist = %q", producer.State.SpecialistSlug)
	}
	if producer.State.HQWorkspaceID != generic.State.HQWorkspaceID {
		t.Fatalf("producer hire produced a different HQ: %q vs %q",
			producer.State.HQWorkspaceID, generic.State.HQWorkspaceID)
	}
}

func TestProducerFocusAreasRoundTripThroughTheClosedEnum(t *testing.T) {
	producer := []string{
		"plan_my_day",
		"track_songs_in_progress",
		"chase_collaborator_handoffs",
		"keep_release_dates_visible",
		"organize_project_files",
		"something_else",
	}
	areas, err := NormalizeFocusAreas(producer)
	if err != nil {
		t.Fatalf("NormalizeFocusAreas: %v", err)
	}
	if len(areas) != len(producer) {
		t.Fatalf("normalized %d focus areas, want %d", len(areas), len(producer))
	}
	for i, area := range areas {
		if string(area) != producer[i] {
			t.Fatalf("focus area %d = %q, want %q", i, area, producer[i])
		}
	}

	// Spaced forms are accepted the same way the generic values are.
	spaced, err := NormalizeFocusAreas([]string{"track songs in progress", "keep release dates visible"})
	if err != nil {
		t.Fatalf("NormalizeFocusAreas(spaced): %v", err)
	}
	if spaced[0] != FocusTrackSongsInProgress || spaced[1] != FocusKeepReleaseDatesVisible {
		t.Fatalf("spaced focus areas = %v", spaced)
	}

	// The enum stays closed.
	if _, err := NormalizeFocusAreas([]string{"master_my_tracks"}); err == nil {
		t.Fatal("expected an unknown focus area to be rejected")
	}
}
