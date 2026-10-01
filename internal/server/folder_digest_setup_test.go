package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFolderSetupHostFallsBackToTheJourneyWhenItCannotPlan(t *testing.T) {
	offer := personalassistant.FolderOffer{
		ID:      "offer-1",
		Subject: personalassistant.FolderCandidateRecord{Name: "Song", Shape: "audio", MarkerName: "*.rpp"},
	}
	// No journey service at all: the card must keep the step-by-step path.
	host := &folderSetupHost{builder: &ServerBuilder{}}
	if _, err := host.Plan(t.Context(), personalassistant.FolderSetupRequest{UserID: "local", Offer: offer}); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("plan without a journey service = %v", err)
	}
	if err := host.Run(t.Context(), personalassistant.FolderSetupRequest{UserID: "local", Offer: offer}); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("run without a journey service = %v", err)
	}
	// A subject that is not a recognized project is never planned.
	other := &folderSetupHost{builder: &ServerBuilder{}}
	if _, err := other.Plan(t.Context(), personalassistant.FolderSetupRequest{Offer: personalassistant.FolderOffer{}}); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("plan of an unrecognized subject = %v", err)
	}
}

// The single-song plan says when Set up switches the Home's shared assistant
// back on, which it can only know from the Home's own consent.
func TestReviewedHomeSharingOffReadsTheHomesConsent(t *testing.T) {
	providers := reviewedintegration.HomeProviders()
	if len(providers) == 0 {
		t.Skip("no reviewed Home provider is registered")
	}
	provider := providers[0]
	files, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &ServerBuilder{workspaceFileStore: files}
	if reviewedHomeSharingOff(b, "local", provider.Key) {
		t.Fatal("no Home, nothing switched off")
	}
	home := &workspace.Workspace{ID: "home", Name: "Music Production Home", OwnerUserID: "local", Kind: "group",
		Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
		Key:           workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: provider.PluginID, ProgramID: provider.ProgramID},
	})
	if err := files.Save(home); err != nil {
		t.Fatal(err)
	}
	consents := workspace.NewProjectStaffingConsents(files)
	if _, err := consents.Grant(home.ID, workspace.ProjectStaffingConsentGrant{
		Source: workspace.ProjectStaffingConsentFolderOffer, PluginID: "ori-reaper", BlueprintID: "reaper-song",
		TeamDigest: strings.Repeat("a", 64), RoleIDs: []string{"reaper-assistant"},
	}); err != nil {
		t.Fatal(err)
	}
	if reviewedHomeSharingOff(b, "local", provider.Key) {
		t.Fatal("an active consent is not switched off")
	}
	if _, err := consents.Revoke(home.ID); err != nil {
		t.Fatal(err)
	}
	if !reviewedHomeSharingOff(b, "local", provider.Key) {
		t.Fatal("a revoked consent must read as switched off")
	}
	if reviewedHomeSharingOff(b, "someone-else", provider.Key) {
		t.Fatal("another user's Home was read")
	}
}

func TestFolderSelectionsIssueATokenOnlyForTheRememberedFolder(t *testing.T) {
	store := pathselection.NewStore()
	token, folder, err := (folderSelections{builder: &ServerBuilder{pathSelectionStore: store}, path: "/Users/me/Songs/My Song"}).Select(t.Context())
	if err != nil || token == "" || folder != "/Users/me/Songs/My Song" {
		t.Fatalf("selection = %q %q %v", token, folder, err)
	}
	resolved, err := store.Resolve(token)
	if err != nil || resolved != folder {
		t.Fatalf("the token must resolve to the remembered folder: %q %v", resolved, err)
	}
	// A path lost to a restart asks the user to pick the folder again.
	for name, selections := range map[string]folderSelections{
		"no path":  {builder: &ServerBuilder{pathSelectionStore: store}},
		"no store": {builder: &ServerBuilder{}, path: "/Users/me/Songs/My Song"},
	} {
		if _, _, err := selections.Select(t.Context()); !errors.Is(err, foldersetup.ErrNeedsPick) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
