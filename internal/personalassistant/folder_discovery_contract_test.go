package personalassistant

import (
	"context"
	"encoding/json"
	"testing"
)

// Discovery must not reuse the explicit create preview's prerequisite hooks.
// These counters characterize that boundary before changing recommendations.
type discoveryContractSetup struct {
	*structureSetup
	readiness, invalidations int
}

func (s *discoveryContractSetup) ReadSetupReadiness(context.Context, FolderSetupRequest) *FolderSetupVisual {
	s.readiness++
	return nil
}

func (s *discoveryContractSetup) InvalidateSetupReadiness(string) { s.invalidations++ }

func TestFolderDiscoveryContract_ReadersPreserveOffersAndSeparatePrerequisites(t *testing.T) {
	f, service, target, observation := albumsObservation(t)
	setup := &discoveryContractSetup{structureSetup: &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}}
	f.service.deps.Setup = setup
	ctx := t.Context()
	before, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	bytesBefore, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	ids := f.ids
	if got := service.Structure(ctx, target, observation.ID); got.Structure == nil {
		t.Fatal("collection discovery missing")
	}
	if got := service.ReviewOptions(ctx, target, observation.ID); len(got) == 0 {
		t.Fatal("collection options missing")
	}
	if _, err := service.PreviewReview(WithFolderSetupRecheck(ctx), target, observation.ID, folderRootCandidateID); err != nil {
		t.Fatal(err)
	}
	if setup.readiness != 0 || setup.invalidations != 0 {
		t.Fatal("local discovery entered explicit prerequisite checks")
	}
	explicit := WithFolderSetupRecheck(WithFolderReviewPlacement(ctx, FolderReviewPlacement{Operation: FolderOperationCreate}))
	if _, err := service.PreviewReview(explicit, target, observation.ID, folderRootCandidateID); err != nil {
		t.Fatal(err)
	}
	if setup.readiness != 1 || setup.invalidations != 1 {
		t.Fatalf("explicit preview hook boundary: readiness=%d invalidations=%d", setup.readiness, setup.invalidations)
	}
	after, err := f.store.Read(ctx, target.UserID)
	if err != nil {
		t.Fatal(err)
	}
	bytesAfter, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytesBefore) != string(bytesAfter) || f.ids != ids || setup.runs != 0 {
		t.Fatal("preview allocated, retired, changed, or executed an offer")
	}
}
