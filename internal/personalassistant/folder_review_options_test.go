package personalassistant

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderReviewOptions_ReadOnlyAvailabilityAndScope(t *testing.T) {
	ctx := context.Background()
	f, service, target := observationFixture(t)
	creator := &fakeFolderCreator{}
	f.service.deps.Creator = creator
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	options := service.ReviewOptions(ctx, target, observation.ID)
	if len(options) < 2 {
		t.Fatalf("missing generic choices: %+v", options)
	}
	for _, option := range options {
		candidate, ok := observedReviewCandidate(service.selections[observation.ID], option.CandidateID)
		if !ok || option.WorkspaceType == "" {
			t.Fatal("invented option", option)
		}
		if candidate.Name == "Album" {
			t.Fatal("specialized setup offered without a plan")
		}
	}
	f.service.deps.Setup = &fakeFolderSetup{lines: oneCardLines()}
	withSetup := service.ReviewOptions(ctx, target, observation.ID)
	if len(withSetup) <= len(options) {
		t.Fatal("available specialized setup not offered", withSetup)
	}
	f.service.deps.Setup = &fakeFolderSetup{planErr: ErrFolderOutcomeUnavailable}
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != len(options) {
		t.Fatal("unavailable plan offered", got)
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil || before.Version != after.Version || f.ids != ids || len(creator.requests) != 0 {
		t.Fatal("suggestions wrote offers, allocated IDs, or executed setup")
	}
	f.service.deps.Creator = nil
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("unavailable creator offered", got)
	}
	f.service.deps.Creator = creator
	foreign := target
	foreign.DraftID = "other"
	if got := service.ReviewOptions(ctx, foreign, observation.ID); len(got) != 0 {
		t.Fatal("foreign selection offered")
	}
	pending, err := f.service.ScanChip(ctx, target.UserID, "documents")
	if err != nil {
		t.Fatal(err)
	}
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("pending review ignored")
	}
	doc, err := f.store.Read(ctx, target.UserID)
	if err != nil || doc.Pending().ID != pending.ID {
		t.Fatal("pending review displaced")
	}
	f.now = f.now.Add(foldercontext.SelectionTTL + time.Second)
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) != 0 {
		t.Fatal("expired selection offered")
	}
}
