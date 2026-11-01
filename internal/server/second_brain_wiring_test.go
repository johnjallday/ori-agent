package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The fill-in copies what the user has confirmed: not a candidate, not a dismissed
// follow-up, and a line from the notes is flagged so it is never written back.
func TestSecondBrainHoldings_FollowUpsAreTheConfirmedOnes(t *testing.T) {
	f := newDraftServerFixture(t)
	service := f.builder.followUpService
	ctx := t.Context()
	capture := func(title string, in followup.CaptureInput) *followup.FollowUp {
		t.Helper()
		in.UserID, in.Title, in.Category = "local", title, followup.CategoryIOwe
		item, err := service.Capture(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	capture("By hand", followup.CaptureInput{Source: followup.SourceRef{Type: "manual"}})
	capture("From the notes", followup.CaptureInput{Source: followup.SourceRef{Type: secondbrain.FollowUpSourceType, ID: "item-1"}})
	capture("Not confirmed", followup.CaptureInput{Source: followup.SourceRef{Type: "email_thread", ID: "t-1"}, Provenance: followup.ProvenanceInferred, Confidence: followup.ConfidenceLow})
	done := capture("Done", followup.CaptureInput{Source: followup.SourceRef{Type: "manual", ID: "d-1"}})
	if _, err := service.Complete(ctx, "local", done.ID); err != nil {
		t.Fatal(err)
	}
	gone := capture("Dismissed", followup.CaptureInput{Source: followup.SourceRef{Type: "manual", ID: "g-1"}})
	if _, err := service.Dismiss(ctx, "local", gone.ID); err != nil {
		t.Fatal(err)
	}

	held, err := secondBrainSeeds{builder: f.builder}.heldFollowUps(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]secondbrain.HeldFollowUp{}
	for _, item := range held {
		byTitle[item.Title] = item
	}
	if len(byTitle) != 3 {
		t.Fatalf("held = %+v", held)
	}
	if item, ok := byTitle["By hand"]; !ok || item.FromNotes || item.Done || item.Source != "Added by you" {
		t.Errorf("by hand = %+v", item)
	}
	if !byTitle["From the notes"].FromNotes {
		t.Errorf("from the notes = %+v", byTitle["From the notes"])
	}
	if !byTitle["Done"].Done {
		t.Errorf("done = %+v", byTitle["Done"])
	}
}

// A group holds workspaces rather than being one. Personal HQ is left out by Sync,
// from the knowledge binding, since a workspace's designation is only a projection
// (TestSecondBrainSync_NeverMirrorsPersonalHQAsAProject).
func TestSecondBrainHoldings_ProjectsLeaveOutGroups(t *testing.T) {
	f := newDraftServerFixture(t)
	save := func(ws *workspace.Workspace) {
		t.Helper()
		if err := f.builder.workspaceStore.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Launch"})
	project.Mission = "Ship the record."
	save(project)
	group := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Grouping Space"})
	group.Kind = "group"
	save(group)

	// The store's listing is a lighter scan that leaves the goal out; the note
	// needs it, so the holdings read each project whole.
	if listed, err := f.builder.workspaceStore.ListActive(); err != nil {
		t.Fatal(err)
	} else {
		for _, ws := range listed {
			if ws.ID == project.ID && ws.Mission != "" {
				t.Skip("the listing now carries the goal; heldProjects no longer needs Get")
			}
		}
	}
	held, err := secondBrainSeeds{builder: f.builder}.heldProjects()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range held {
		switch item.Name {
		case "Launch":
			found = item.Goal == "Ship the record." && item.ID == project.ID
		case "Grouping Space":
			t.Errorf("%s is not a project", item.Name)
		}
	}
	if !found {
		t.Fatalf("held = %+v", held)
	}
}

// A second brain Ori set up is written to by three things: the setup, the
// Journal's Daily copy and the assistant's capture tool. Each reaches the one
// notes service the builder made; none builds its own.
func TestSecondBrain_TheWritersShareTheNotesServiceTheBuilderMade(t *testing.T) {
	f := newDraftServerFixture(t)
	service := f.builder.personalAssistantSecondBrain
	if service == nil {
		t.Fatal("the builder made no second-brain service")
	}
	if f.builder.server.Storage.PersonalAssistantSecondBrain != service {
		t.Fatal("the storage facade does not hold the builder's second-brain service")
	}
	if got := f.builder.server.newHomeAssistantAskHandler().SecondBrainCapture; got == nil {
		t.Fatal("the assistant's capture tool is not wired to the notes service")
	}
	// The Journal copy reads the service when a journal is saved, so it cannot
	// be built before it; with nothing connected it copies nothing.
	mirror := journalDailyMirror{builder: f.builder}
	if result := mirror.MirrorDaily(t.Context(), "local", "2026-10-31", "# Journal"); result != "" {
		t.Fatalf("mirror with nothing connected = %q", result)
	}
	if result := (journalDailyMirror{}).MirrorDaily(t.Context(), "local", "2026-10-31", "# Journal"); result != "" {
		t.Fatalf("mirror with no builder = %q", result)
	}
}
