package sessionhttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// Every follow-up state and every brief revision (several on one date) comes
// back exactly — IDs, states, dates, current selection — and restoring them
// changes nothing by itself: no nudge becomes due until the user turns this
// workspace's routines on here.
func TestContinuityImportKeepsEveryFollowUpStateAndBriefRevision(t *testing.T) {
	ctx := t.Context()
	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	past := at.Add(-72 * time.Hour)
	later := at.Add(72 * time.Hour)
	source := newContinuityInstallation(t, "source")
	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Errands"})
	ws.OwnerUserID, ws.FolderSlug, ws.CreatedAt, ws.UpdatedAt = "local", "errands", at, at
	if err := source.sync.Save(ws); err != nil {
		t.Fatal(err)
	}
	follow := func(id string, status followup.Status, mutate func(*followup.FollowUp)) *followup.FollowUp {
		f := &followup.FollowUp{ID: id, UserID: "local", WorkspaceID: ws.ID, Category: followup.CategoryIOwe,
			Direction: followup.DirectionOutbound, Title: "Follow-up " + id, Source: followup.SourceRef{Type: "manual", ID: id},
			Provenance: followup.ProvenanceManual, Status: status, CreatedAt: at, UpdatedAt: at}
		if mutate != nil {
			mutate(f)
		}
		return f
	}
	followUps := []*followup.FollowUp{
		follow("f-candidate", followup.StatusCandidate, func(f *followup.FollowUp) { f.Provenance = followup.ProvenanceInferred }),
		follow("f-active", followup.StatusActive, func(f *followup.FollowUp) { f.DueAt = &past }),
		follow("f-snoozed", followup.StatusSnoozed, func(f *followup.FollowUp) { f.SnoozedUntil = &later }),
		follow("f-completed", followup.StatusCompleted, func(f *followup.FollowUp) { f.CompletedAt = &at }),
		follow("f-dismissed", followup.StatusDismissed, func(f *followup.FollowUp) { f.DismissedAt = &at }),
		follow("f-reopened", followup.StatusReopened, func(f *followup.FollowUp) { f.DueAt = &past }),
	}
	sourceFollowUps := followup.NewSQLiteStore(source.db)
	for _, f := range followUps {
		if err := sourceFollowUps.Create(ctx, f); err != nil {
			t.Fatalf("%s: %v", f.ID, err)
		}
	}
	briefs := dailybrief.NewSQLiteStore(source.db)
	for i, rev := range []dailybrief.Revision{
		{ID: "b-1", LocalDate: "2026-08-30", RevisionNumber: 1, Trigger: dailybrief.TriggerScheduled},
		{ID: "b-2", LocalDate: "2026-08-31", RevisionNumber: 1, Trigger: dailybrief.TriggerScheduled},
		{ID: "b-3", LocalDate: "2026-08-31", RevisionNumber: 2, Trigger: dailybrief.TriggerManual},
	} {
		rev.WorkspaceID, rev.UserID, rev.Status, rev.ConfigRevision = ws.ID, "local", dailybrief.GenerationSucceeded, 1
		rev.ContentJSON = `{"opening_summary":"` + rev.ID + `"}`
		rev.GeneratedAt, rev.CreatedAt = at.Add(time.Duration(i)*time.Hour), at.Add(time.Duration(i)*time.Hour)
		if err := briefs.CreateRevision(ctx, &rev); err != nil {
			t.Fatal(err)
		}
	}
	// One current revision per workspace; other dates keep their newest.
	if err := briefs.SetCurrentRevision(ctx, ws.ID, "b-3"); err != nil {
		t.Fatal(err)
	}
	if status, err := source.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		prep, _ := source.local.Preparation(ctx, ws.ID)
		t.Fatalf("source not ready: %+v %v %+v", status, err, prep)
	}
	folder, _ := source.files.GetFolderPath(ws.ID)
	copied := copyContinuityFolder(t, folder)

	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	history, _ := review["history"].(map[string]any)
	if history["follow_ups"] != float64(6) || history["brief_revisions"] != float64(3) {
		t.Fatalf("review counts: %v", history)
	}
	code, payload := dest.request(t, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "workspace_only"})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}

	destFollowUps := followup.NewSQLiteStore(dest.db)
	for _, want := range followUps {
		got, err := destFollowUps.Get(ctx, "local", want.ID)
		if err != nil {
			t.Fatalf("%s missing: %v", want.ID, err)
		}
		if got.Status != want.Status || got.WorkspaceID != ws.ID || !got.CreatedAt.Equal(want.CreatedAt) ||
			!sameTime(got.DueAt, want.DueAt) || !sameTime(got.SnoozedUntil, want.SnoozedUntil) ||
			!sameTime(got.CompletedAt, want.CompletedAt) || !sameTime(got.DismissedAt, want.DismissedAt) {
			t.Fatalf("%s changed: got %+v want %+v", want.ID, got, want)
		}
	}
	destBriefs := dailybrief.NewSQLiteStore(dest.db)
	summaries, err := destBriefs.ListHistory(ctx, ws.ID, 30)
	if err != nil || len(summaries) != 2 || summaries[0].CurrentRevisionID != "b-3" || summaries[0].RevisionCount != 2 ||
		summaries[0].LatestRevisionID != "b-3" || summaries[1].CurrentRevisionID != "" || summaries[1].LatestRevisionID != "b-1" {
		t.Fatalf("brief history changed: %+v %v", summaries, err)
	}

	// Restoring is not a routine: overdue commitments are not nudged until the
	// workspace's routines are turned on here.
	service := followup.NewService(destFollowUps)
	service.SetAutomaticAdmission(func(ctx context.Context, workspaceID string) bool {
		return dest.local.CheckExecution(ctx, workspaceID, true) == nil
	})
	if due, err := service.DueForNudge(ctx, "local"); err != nil || len(due) != 0 {
		t.Fatalf("imported commitments became due for nudges: %d %v", len(due), err)
	}
	attachment, err := dest.local.Attachment(ctx, ws.ID)
	if err != nil || attachment.State != workspacecontinuity.ImportedInactive {
		t.Fatalf("attachment: %+v %v", attachment, err)
	}
	if err := dest.local.SetImportedActive(ctx, ws.ID, attachment.Version, true); err != nil {
		t.Fatal(err)
	}
	if due, err := service.DueForNudge(ctx, "local"); err != nil || len(due) != 2 {
		t.Fatalf("enabled routines should see the two overdue commitments: %d %v", len(due), err)
	}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
