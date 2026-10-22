package personalassistant

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderBuildPlan_IsTheCardsPlanForTheBuildsFolderAndCreatesNothing(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	song := filepath.Join(f.home, "Music", "Ori Music Demo")
	writeFolderFiles(t, song, "Ori Music Demo.rpp", "Media/take.wav")
	lines := []FolderPlanLine{
		{Kind: FolderPlanProvider, Name: "Installs the Music Project Management plugin"},
		{Kind: FolderPlanHome, Name: "Creates Home “Music Production Home”"},
		{Kind: FolderPlanWorkspace, Name: "Creates a song workspace named My Demos"},
	}
	setup := &fakeFolderSetup{
		lines:       lines,
		visual:      &FolderSetupVisual{Entity: &FolderSetupEntity{Kind: "workspace", Name: "My Demos", Operation: "create"}},
		destination: &FolderSetupDestination{Status: "new", Name: "Music Production Home", Kind: "plugin"},
		intent:      FolderSetupIntent{Integration: FolderInstallReady, Provider: FolderInstallInstall, Placement: "grouped", CreatesHome: true},
	}
	f.service.deps.Setup = setup
	observation := observeSelected(t, f, service, target, song)
	recognition, err := service.Recognize(ctx, target, observation.ID)
	if err != nil || !recognition.Recognized() {
		t.Fatalf("recognition = %+v %v", recognition, err)
	}
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, " My Demos ")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Ready || plan.Digest == "" || plan.Unavailable != "" || len(plan.Lines) != 3 || plan.Lines[1].Name != lines[1].Name {
		t.Fatalf("plan = %+v", plan)
	}
	wantNeeds := []BuildFolderNeed{
		{Kind: FolderPlanIntegration, Name: "REAPER integration", State: FolderInstallReady},
		{Kind: FolderPlanProvider, Name: "Music Project Management", State: FolderInstallInstall},
	}
	if len(plan.Needs) != 2 || plan.Needs[0] != wantNeeds[0] || plan.Needs[1] != wantNeeds[1] {
		t.Fatalf("needs = %+v", plan.Needs)
	}
	if plan.Home == nil || *plan.Home != (BuildFolderHome{Name: "Music Production Home", Status: "new"}) || plan.Placement != "grouped" || plan.Evidence == "" {
		t.Fatalf("home/placement/evidence = %+v %q %q", plan.Home, plan.Placement, plan.Evidence)
	}
	if plan.Visual == nil || plan.Visual.Entity.Name != "My Demos" {
		t.Fatalf("missing shared visual facts: %+v", plan.Visual)
	}
	plan.Visual.Entity.Name = "Browser display only"
	if setup.visual.Entity.Name != "My Demos" {
		t.Fatal("build display aliases host facts")
	}
	// The host plans the build's own name, and the project the build attached.
	last := setup.planned[len(setup.planned)-1]
	if last.WorkspaceName != "My Demos" || last.Offer.Subject.Name != "Ori Music Demo" || last.Offer.ConversationReview == nil ||
		last.Offer.ConversationReview.Target != target {
		t.Fatalf("planned request = %+v", last)
	}
	// Reading it creates nothing.
	after, err := f.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version || len(after.Offers) != len(before.Offers) {
		t.Fatalf("planning wrote the digest: %d/%d → %d/%d %v", before.Version, len(before.Offers), after.Version, len(after.Offers), err)
	}

	// A different name is a different plan to confirm.
	renamed, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, "")
	if err != nil || renamed.Digest == "" || setup.planned[len(setup.planned)-1].WorkspaceName != "" {
		t.Fatalf("default-name plan = %+v %v", renamed, err)
	}

	// A host that cannot plan it says why, with what it waits on.
	setup.planErr = errors.New("integration_unavailable")
	blocked, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, "My Demos")
	if err != nil || blocked.Ready || blocked.Digest != "" || blocked.Unavailable == "" || len(blocked.Lines) != 0 {
		t.Fatalf("blocked plan = %+v %v", blocked, err)
	}
	// No Home yet and no provider to make it from: the plan waits on that
	// plugin, named, exactly as Home's card waits on it.
	setup.destinationErr = &FolderSetupBlockedError{
		Blocker: FolderDestinationBlocker{IntegrationKey: "ori_reaper", HomeProviderKey: "music_project_management"},
		Err:     errors.New("home template not ready"),
	}
	waiting, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, "My Demos")
	if err != nil || waiting.Ready || waiting.Unavailable != FolderAvailabilityProviderNotInstalled || waiting.UnavailableSubject != "Music Project Management" {
		t.Fatalf("provider missing = %+v %v", waiting, err)
	}
	setup.destinationErr = nil
	setup.planErr = nil
	f.service.deps.Setup = nil
	if none, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, "My Demos"); err != nil || none.Ready || none.Unavailable == "" {
		t.Fatalf("no setup host = %+v %v", none, err)
	}
	f.service.deps.Setup = setup

	// Loose files are no capability project, and a lost pick is refused.
	desktop, err := service.Observe(ctx, target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if loose, err := service.BuildPlan(ctx, target, desktop.ID, "candidate-0", ""); err != nil || loose.Ready {
		t.Fatalf("loose files planned = %+v %v", loose, err)
	}
	f.now = f.now.Add(foldercontext.SelectionTTL)
	if _, err := service.BuildPlan(ctx, target, observation.ID, recognition.CandidateID, ""); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("an expired pick: %v", err)
	}
}
