package personalassistant

import (
	"context"
	"strings"
	"testing"
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
