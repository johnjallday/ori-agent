package personalassistant

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newStudioWorkspace(t *testing.T, store *workspace.InMemoryStore, templateID string, now time.Time) *workspace.Workspace {
	t.Helper()
	studio := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Ivory"})
	studio.ID, studio.FolderSlug, studio.OwnerUserID = "studio-1", "ivory", "local"
	studio.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: templateID})
	completed := now.Add(-2 * time.Hour)
	older := now.Add(-6 * time.Hour)
	studio.Tasks = []workspace.Task{
		{
			ID: "studio-result-1", WorkspaceID: studio.ID, To: "Reaper Producer",
			Description: "Bounce a rough mix of Ivory", TicketState: workspace.TicketStateReview,
			Result: "rough mix rendered", CompletedAt: &completed, CreatedAt: older,
		},
		{
			ID: "studio-result-2", WorkspaceID: studio.ID, To: "Reaper Producer",
			Description: "Tag the session takes", TicketState: workspace.TicketStateReview,
			Result: "takes tagged", CompletedAt: &older, CreatedAt: older,
		},
		{
			// Unassigned: reported without a name rather than credited to the
			// specialist by assumption.
			ID: "studio-result-3", WorkspaceID: studio.ID,
			Description: "Archive the stems", TicketState: workspace.TicketStateReview,
			Result: "archived", CompletedAt: &older, CreatedAt: older,
		},
		{
			// Not finished, so not a result.
			ID: "studio-open", WorkspaceID: studio.ID, To: "Reaper Producer",
			Description: "Master the single", TicketState: workspace.TicketStateReady, CreatedAt: older,
		},
	}
	if err := store.Save(studio); err != nil {
		t.Fatal(err)
	}
	return studio
}

func studioTodayService(t *testing.T, slug, templateID string) *TodayProjection {
	t.Helper()
	now := time.Now().UTC()
	store, _ := newTodayWorkspace(t, now)
	if templateID != "" {
		newStudioWorkspace(t, store, templateID, now)
	}
	projection := baseTodayProjection()
	projection.SpecialistSlug = slug
	service := NewTodayService(
		stubTodayRelationship{projection: projection},
		stubTodayBrief{err: dailybrief.ErrRevisionNotFound},
		store,
		stubTodayFollowUps{},
	)
	out, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return out
}

func TestToday_StudioWorkIsAttributedToTheSpecialistByName(t *testing.T) {
	out := studioTodayService(t, "music_production", "plugin:reaper-plugin:reaper-song")
	if out.Studio == nil {
		t.Fatal("expected a studio section")
	}
	if out.Studio.SpecialistName != "Reaper Producer" {
		t.Fatalf("specialist name = %q", out.Studio.SpecialistName)
	}
	if out.Studio.Domain != "music projects" || out.Studio.WorkspaceName != "Ivory" {
		t.Fatalf("studio = %+v", out.Studio)
	}
	if out.Studio.Route != "/workspaces/ivory" {
		t.Fatalf("studio route = %q", out.Studio.Route)
	}
	if out.Studio.Health.Status != TodaySectionAvailable {
		t.Fatalf("studio health = %+v", out.Studio.Health)
	}
	if len(out.Studio.Items) != 3 {
		t.Fatalf("studio items = %d, want the 3 finished results", len(out.Studio.Items))
	}
	byID := map[string]TodayItem{}
	for _, item := range out.Studio.Items {
		byID[item.ID] = item
		if item.Kind != "studio_result" {
			t.Fatalf("item %q kind = %q", item.ID, item.Kind)
		}
	}
	if byID["studio-result-1"].Attribution != "Reaper Producer" {
		t.Fatalf("attribution = %q", byID["studio-result-1"].Attribution)
	}
	if byID["studio-result-3"].Attribution != "" {
		t.Fatalf("unassigned work must not be credited to anyone: %q", byID["studio-result-3"].Attribution)
	}
	if _, present := byID["studio-open"]; present {
		t.Fatal("unfinished work is not a result")
	}
	// The studio is a separate read; HQ's own results are unaffected.
	if len(out.Results.Items) != 1 || out.Results.Items[0].ID != "result-1" {
		t.Fatalf("hq results = %+v", out.Results.Items)
	}
}

// No specialist, an unknown slug, and a specialist with no workspace yet are
// all "nothing to report" — which is not a degraded source and must not turn
// Today "partial".
func TestToday_StudioIsAbsentRatherThanEmpty(t *testing.T) {
	cases := map[string]struct{ slug, templateID string }{
		"no specialist":         {"", ""},
		"unknown slug":          {"retired_domain", "plugin:reaper-plugin:reaper-song"},
		"no domain workspace":   {"music_production", ""},
		"unrelated workspace":   {"music_production", "calendar-ops"},
		"bare blueprint typo'd": {"music_production", "reaper-songs"},
	}
	for name, testCase := range cases {
		out := studioTodayService(t, testCase.slug, testCase.templateID)
		if out.Studio != nil {
			t.Fatalf("%s: expected no studio section, got %+v", name, out.Studio)
		}
		if out.State == "partial" {
			t.Fatalf("%s: an absent studio must not degrade Today", name)
		}
	}
}

func TestToday_StudioAcceptsTheBareBlueprintID(t *testing.T) {
	out := studioTodayService(t, "music_production", "reaper-song")
	if out.Studio == nil {
		t.Fatal("expected a studio section for the bare blueprint ID")
	}
}

func TestToday_StudioIgnoresAnotherUsersWorkspace(t *testing.T) {
	now := time.Now().UTC()
	store, _ := newTodayWorkspace(t, now)
	foreign := newStudioWorkspace(t, store, "reaper-song", now)
	foreign.OwnerUserID = "someone-else"
	if err := store.Save(foreign); err != nil {
		t.Fatal(err)
	}
	projection := baseTodayProjection()
	projection.SpecialistSlug = "music_production"
	out, err := NewTodayService(
		stubTodayRelationship{projection: projection},
		stubTodayBrief{},
		store,
		stubTodayFollowUps{},
	).Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.Studio != nil {
		t.Fatalf("another user's studio must not be reported: %+v", out.Studio)
	}
}
