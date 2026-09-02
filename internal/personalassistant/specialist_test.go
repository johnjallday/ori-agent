package personalassistant

import (
	"context"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
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

// The specialist is a workspace agent, not a second personal-assistant
// relationship. Recording one must not create a second row, a second
// assistant identity, or a second relationship of any kind.
func TestSpecialistDoesNotCreateASecondRelationship(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)

	first := activeTestState("user-one", "assistant-one")
	first.SpecialistSlug = "music_production"
	if _, err := store.CreateState(ctx, first); err != nil {
		t.Fatalf("CreateState: %v", err)
	}

	// A second relationship for the same user is refused, specialist or not.
	second := activeTestState("user-one", "assistant-two")
	second.SpecialistSlug = ""
	if _, err := store.CreateState(ctx, second); err == nil {
		t.Fatal("expected a second relationship for the same user to be refused")
	}

	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM personal_assistant_state WHERE user_id = ?`, "user-one",
	).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("personal_assistant_state rows for one user = %d, want 1", rows)
	}

	// user_id is still the primary key, and specialist_slug is an ordinary
	// nullable-free additive column beside it.
	var pkColumns, slugColumns int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('personal_assistant_state') WHERE pk > 0`,
	).Scan(&pkColumns); err != nil {
		t.Fatalf("read primary key: %v", err)
	}
	if pkColumns != 1 {
		t.Fatalf("primary key columns = %d, want 1 (user_id)", pkColumns)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('personal_assistant_state')
		 WHERE name = 'specialist_slug' AND pk = 0 AND "notnull" = 1`,
	).Scan(&slugColumns); err != nil {
		t.Fatalf("read specialist column: %v", err)
	}
	if slugColumns != 1 {
		t.Fatal("specialist_slug must be an additive NOT NULL non-key column")
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

// This feature is for new hires. A relationship that predates it must be
// completely untouched: nothing backfills it, nothing offers it a specialist
// after the fact, and every post-hire read behaves exactly as it did before.
func TestExistingRelationshipsAreNeverRetrofitted(t *testing.T) {
	ctx := context.Background()

	// A relationship as it exists before this feature: an active hire with no
	// specialist column value at all.
	service, store, _, _, _ := serviceMatrixFixture(StatusActive)
	store.state.SpecialistSlug = ""

	projection, err := service.Get(ctx, "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.SpecialistSlug != "" {
		t.Fatalf("relationship read invented a specialist: %q", projection.SpecialistSlug)
	}

	// The capability projection is the pre-feature order with no suggestion.
	workspaces := workspace.NewInMemoryStore()
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	hq.ID, hq.FolderSlug, hq.OwnerUserID = "hq-local", "personal-hq", "local"
	if err := workspaces.Save(hq); err != nil {
		t.Fatal(err)
	}
	capabilities, err := NewCapabilityService(service, workspaces, nil).Get(ctx, "local")
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if got := capabilityKeys(capabilities.Cards); !slices.Equal(got, []string{"email", "calendar", "projects", "folders"}) {
		t.Fatalf("existing relationship card order = %v", got)
	}
	if capabilities.Suggestion != nil {
		t.Fatalf("existing relationship was offered a workspace: %+v", capabilities.Suggestion)
	}

	// Even with a domain workspace already on disk, no specialist surfaces:
	// the offer is the only thing that records one, and it is only shown
	// during a hire.
	studio := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Ivory"})
	studio.ID, studio.FolderSlug, studio.OwnerUserID = "studio-1", "ivory", "local"
	studio.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "reaper-song"})
	if err := workspaces.Save(studio); err != nil {
		t.Fatal(err)
	}
	today, err := NewTodayService(
		stubTodayRelationship{projection: projection},
		stubTodayBrief{err: dailybrief.ErrRevisionNotFound},
		workspaces,
		stubTodayFollowUps{},
	).Get(ctx, "local")
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if today.Studio != nil {
		t.Fatalf("existing relationship got a studio section: %+v", today.Studio)
	}

	// And the persisted row is still what it was.
	persisted, err := store.GetState(ctx, "local")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if persisted.SpecialistSlug != "" {
		t.Fatalf("persisted specialist = %q, want empty", persisted.SpecialistSlug)
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
