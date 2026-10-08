package agenthttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestReviewContext_DestinationUsesCanonicalPlanOrPinnedRunNotPage(t *testing.T) {
	destination := &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: "PRIVATE_HOME_ID", Name: "Music Home", Kind: "home", OwnerUserID: "PRIVATE_OWNER_ID", CompatibilityHash: "PRIVATE_COMPATIBILITY_HASH"}
	view := &personalassistant.FolderOfferView{Status: personalassistant.FolderOfferPending, Subject: personalassistant.FolderSubjectView{Name: "Album-5"}, CreateAvailable: true, Plan: &personalassistant.FolderSetupPlan{Destination: destination}}
	projection := projectReviewContext(view, true)
	if projection.DestinationName != "Music Home" || projection.DestinationKind != "home" || projection.DestinationStatus != "existing" {
		t.Fatal(projection)
	}
	text := reviewContextPrompt(context.WithValue(context.Background(), reviewContextKey{}, projection))
	for _, private := range []string{destination.WorkspaceID, destination.OwnerUserID, destination.CompatibilityHash} {
		if strings.Contains(text, private) {
			t.Fatal("destination witness leaked to model")
		}
	}
	// A stopped run describes its original destination, not a refreshed plan.
	view.Status = personalassistant.FolderOfferAwaitingOutcome
	view.Plan.Destination = &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: "other-home", Name: "Other Home", OwnerUserID: "local"}
	view.Setup = &personalassistant.FolderSetupView{Status: personalassistant.FolderSetupStopped, StopReason: personalassistant.FolderStopInterrupted, Destination: destination}
	projection = projectReviewContext(view, true)
	if projection.DestinationName != "Music Home" {
		t.Fatal("interrupted setup silently retargeted", projection)
	}
	view.DestinationStatus = "changed"
	projection = projectReviewContext(view, true)
	if projection.Status != "setup_unavailable" || projection.Blocker != "destination_changed" || len(projection.Controls) != 0 || projection.DestinationName != "Music Home" {
		t.Fatal("changed destination retained executable controls or lost original disclosure", projection)
	}
}
