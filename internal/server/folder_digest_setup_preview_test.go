package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
)

func TestFolderSetupPreview_UsesCanonicalReaderWithoutJourneyWrites(t *testing.T) {
	db := testdb.Open(t)
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("missing registry fixture")
	}
	for _, test := range []struct {
		complete bool
		action   setupjourney.ActionID
		want     string
	}{
		{true, "", personalassistant.FolderInstallReady},
		{false, setupjourney.ActionReviewInstall, personalassistant.FolderInstallInstall},
		{false, setupjourney.ActionReviewEnable, personalassistant.FolderInstallEnable},
		{false, setupjourney.ActionReviewUpdate, personalassistant.FolderInstallUpdate},
	} {
		reads := 0
		reader := setupjourney.CanonicalReaderFunc(func(_ context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			reads++
			if scope.OwnerUserID != "local" || scope.IntegrationKey != entry.Key || scope.ExpectedBlueprintID != entry.ExpectedBlueprintID || scope.ExpectedAssistantProgramID != entry.ExpectedProgramID {
				t.Fatal("preview bypassed canonical integration identity", scope)
			}
			return setupjourney.CanonicalStepRead{Complete: test.complete, AvailableActions: []setupjourney.ActionID{test.action}, Integration: &setupjourney.IntegrationProjection{ExpectedVersion: "0.9.0", InstalledVersion: "0.9.0"}}, nil
		})
		// No journey service: a preview must not require one to read software
		// prerequisites or initialize/reconcile its mutable run store.
		host := &folderSetupHost{builder: &ServerBuilder{reviewedIntegrationReader: reader}}
		for range 3 {
			facts, err := host.integrationFacts(t.Context(), "local", entry)
			if err != nil || facts.State != test.want || facts.Version != "0.9.0" {
				t.Fatal(facts, err)
			}
		}
		if reads != 3 {
			t.Fatal("preview did not read current prerequisite")
		}
		var rows int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM setup_journey_run").Scan(&rows); err != nil || rows != 0 {
			t.Fatal("preview created journey state", rows, err)
		}
	}
}
