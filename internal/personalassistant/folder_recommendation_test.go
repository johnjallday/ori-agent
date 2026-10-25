package personalassistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

type localRecommendationSetup struct {
	*discoveryContractSetup
	destination FolderRecommendationDestination
}

func (s *localRecommendationSetup) ReadFolderRecommendation(context.Context, string, string) FolderRecommendationDestination {
	return s.destination
}

func TestFolderRecommendation_ObservedFormatsAndIntegrationAreDifferent(t *testing.T) {
	result, _, now := scanAlbums(t)
	// The named exact fixture's policy input: a Logic bundle, not another RPP.
	for i := range result.Candidates {
		if result.Candidates[i].Name == "Logic Sketch" {
			result.Candidates[i].Marker = &folderdigest.Marker{Name: "*.logicx", Shape: folderdigest.ShapeAudio, Label: "Logic Pro project"}
		}
	}
	verdict := folderdigest.Decide(result, now)
	r := collectionRecommendation(result, verdict, now)
	if r == nil || r.Operation != FolderGroupLibrary || r.Projects != 6 || r.SupportedProjects != 5 ||
		!strings.Contains(r.Evidence, "5 REAPER session folders") || !strings.Contains(r.Evidence, "1 Logic Pro project folder") || r.Limitation == "" {
		t.Fatalf("recommendation: %+v", r)
	}
	result.Partial = true
	r = collectionRecommendation(result, folderdigest.Decide(result, now), now)
	if !strings.Contains(r.Evidence, "partial") {
		t.Fatal("partial scan represented as complete")
	}
	result.Candidates = result.Candidates[:5] // only four audio folders observed
	r = collectionRecommendation(result, folderdigest.Decide(result, now), now)
	if r == nil || r.Operation != FolderGroupPlain {
		t.Fatal("lowered portfolio threshold")
	}
	result.Candidates = result.Candidates[:2]
	if r := collectionRecommendation(result, folderdigest.Decide(result, now), now); r != nil {
		t.Fatal("single project gained a collection recommendation")
	}
}

func TestFolderRecommendation_LocalDiscoveryPreservesPendingReview(t *testing.T) {
	for _, destination := range []FolderRecommendationDestination{
		{State: "existing", Name: "My Music", Readiness: "review"},
		{State: "absent", Readiness: "provider_missing"},
		{State: "absent", Readiness: "provider_disabled"},
		{State: "absent", Readiness: "provider_outdated"},
		{State: "unknown", Readiness: "destination_ambiguous"},
		{State: "unknown", Readiness: "destination_incompatible"},
	} {
		t.Run(destination.Readiness, func(t *testing.T) {
			f, service, target, observation := albumsObservation(t)
			setup := &localRecommendationSetup{discoveryContractSetup: &discoveryContractSetup{structureSetup: &structureSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines()}}}, destination: destination}
			f.service.deps.Setup = setup
			if _, err := f.service.ScanChip(t.Context(), target.UserID, "documents"); err != nil {
				t.Fatal(err)
			}
			before, err := f.store.Read(t.Context(), target.UserID)
			if err != nil || before.Pending() == nil {
				t.Fatal("missing blocking offer", err)
			}
			prior, _ := json.Marshal(before)
			ids, homeReads, integrationReads := f.ids, setup.homeReads, setup.integrationReads
			got := service.Structure(t.Context(), target, observation.ID)
			if got.Structure == nil || got.Structure.Recommendation == nil || got.Unavailable != FolderStructureReviewPending {
				t.Fatalf("pending review hid discovery: %+v", got)
			}
			r := got.Structure.Recommendation
			if r.ObservationID != observation.ID || r.Operation != FolderGroupLibrary || r.Destination.State != destination.State || r.Destination.Readiness != destination.Readiness {
				t.Fatalf("recommendation lost identity or readiness: %+v", r)
			}
			after, err := f.store.Read(t.Context(), target.UserID)
			current, _ := json.Marshal(after)
			if err != nil || string(prior) != string(current) || ids != f.ids || setup.runs != 0 || setup.readiness != 0 || setup.invalidations != 0 || setup.homeReads != homeReads || setup.integrationReads != integrationReads {
				t.Fatal("discovery entered plan/prerequisite/effect hooks or changed the blocker", err)
			}
			data, err := json.Marshal(got.Structure)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{f.home, "rel_path", "plan_digest", "owner_user_id", "declaration_digest"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("discovery exposed %s", secret)
				}
			}
		})
	}
}
