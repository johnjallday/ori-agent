package server

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
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
