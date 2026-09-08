package server

import (
	"context"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

func TestSetupSummaryOffersOnlyClosedExistingScopesAndRootContinuation(t *testing.T) {
	for _, kind := range []setupjourney.RunKind{setupjourney.RunKindRoot, setupjourney.RunKindChild} {
		empty, err := readSetupSummary(context.Background(), setupjourney.ReadScope{RunKind: kind})
		if err != nil || len(empty.AvailableActions) != 1 || empty.AvailableActions[0] != setupjourney.ActionReviewSetup || empty.Complete {
			t.Fatalf("empty scope: %+v %v", empty, err)
		}
		ready, err := readSetupSummary(context.Background(), setupjourney.ReadScope{RunKind: kind, HomeWorkspaceID: "home", ProjectWorkspaceID: "project"})
		if err != nil || ready.Complete || !slices.Contains(ready.AvailableActions, setupjourney.ActionOpenSampleLibrarySetup) || !slices.Contains(ready.AvailableActions, setupjourney.ActionOpenLiveSetup) {
			t.Fatalf("scope navigation: %+v %v", ready, err)
		}
		if slices.Contains(ready.AvailableActions, setupjourney.ActionConnectAnotherProject) != (kind == setupjourney.RunKindRoot) {
			t.Fatalf("continuation uses another run's revision: %+v", ready)
		}
	}
}
