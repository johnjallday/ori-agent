package personalassistant

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// namingFolderSetup plans from the request it is given: its lines name the
// workspace (the request's name, or the folder's) and the folder it links, so
// two entries get the same digest only when they ask the host for the same
// setup.
type namingFolderSetup struct{ *fakeFolderSetup }

func (n namingFolderSetup) Plan(_ context.Context, req FolderSetupRequest) (FolderSetupPlan, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.planned = append(n.planned, req)
	name := strings.TrimSpace(req.WorkspaceName)
	if name == "" {
		name = req.Offer.Subject.Name
	}
	plan := NewFolderSetupPlan([]FolderPlanLine{
		{Kind: FolderPlanHome, Name: "Creates Home “Music Production Home”"},
		{Kind: FolderPlanWorkspace, Name: "Creates a REAPER song workspace named " + name},
		{Kind: FolderPlanFolder, Name: "Links " + req.Offer.Subject.Name + " where it is"},
	})
	plan.Destination = n.destination
	plan.Intent = n.intent
	return plan.Stamped(), nil
}

// FR29, FR30: the same song folder, explored on Home and attached to a build,
// is one plan with one digest, and both entries say what they recognized and
// what the plan needs in the same words.
func TestFolderPlanIdentity_HomesCardAndABuildPlanTheSameSongFolderAlike(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	song := filepath.Join(f.home, "Music", "Ori Music Demo")
	writeFolderFiles(t, song, "Ori Music Demo.rpp", "Media/take.wav")
	f.service.deps.Setup = namingFolderSetup{&fakeFolderSetup{
		destination: &FolderSetupDestination{Status: "new", Name: "Music Production Home", Kind: "plugin"},
		intent:      FolderSetupIntent{Integration: FolderInstallReady, Provider: FolderInstallInstall, Placement: "grouped", CreatesHome: true},
	}}

	// Home's Explore a folder.
	card, err := f.service.scanRoot(ctx, "local", song, "")
	if err != nil {
		t.Fatal(err)
	}
	if card.Plan == nil || card.Plan.Digest == "" {
		t.Fatalf("Home's card has no plan: %+v", card)
	}

	// A build that attached the same folder, named after it as the card is.
	observation := observeSelected(t, f, service, target, song)
	recognition, err := service.Recognize(ctx, target, observation.ID)
	if err != nil || !recognition.Recognized() {
		t.Fatalf("recognition = %+v %v", recognition, err)
	}
	build, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, "")
	if err != nil || !build.Ready {
		t.Fatalf("build plan = %+v %v", build, err)
	}

	if build.Digest != card.Plan.Digest {
		t.Fatalf("the two entries planned different setups:\n card  %s %+v\n build %s %+v", card.Plan.Digest, card.Plan.Lines, build.Digest, build.Lines)
	}
	if !reflect.DeepEqual(build.Needs, card.Needs) || len(card.Needs) != 2 {
		t.Fatalf("needs differ: card %+v, build %+v", card.Needs, build.Needs)
	}
	if card.Recognition == nil || card.Recognition.Verdict != BuildFolderProject || card.Recognition.Label != recognition.Label ||
		card.Recognition.Label != "REAPER project" {
		t.Fatalf("recognition differs: card %+v, build %+v", card.Recognition, recognition)
	}

	// A build named otherwise asks for another workspace: another plan.
	renamed, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, "Summer Demos")
	if err != nil || renamed.Digest == card.Plan.Digest {
		t.Fatalf("a different name kept the card's digest: %+v %v", renamed, err)
	}
}
