package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
)

// Neither a failed release check nor a successful preview is execution authority.
// This deliberately has no journey service, session handler, or model provider.
func TestFolderSetupPreview_UnavailableAndCachedFactsAreObservational(t *testing.T) {
	entry, _ := reviewedintegration.Get("ori_reaper")
	reads := 0
	available := false
	host := &folderSetupHost{builder: &ServerBuilder{}}
	host.builder.reviewedIntegrationReader = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
		reads++
		if !available {
			return setupjourney.CanonicalStepRead{}, errors.New("release lookup unavailable")
		}
		return setupjourney.CanonicalStepRead{
			AvailableActions: []setupjourney.ActionID{setupjourney.ActionReviewInstall},
			Integration:      &setupjourney.IntegrationProjection{ExpectedVersion: "0.9.0"},
		}, nil
	})
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{
		Subject: personalassistant.FolderCandidateRecord{Name: "Session", Shape: "audio", MarkerName: "*.rpp"},
	}}
	target, err := host.target(req.Offer)
	if err != nil {
		t.Fatal(err)
	}
	if target.provider == nil {
		t.Fatal("registry must identify the Home requirement before either plugin exists")
	}
	// Isolate the software-facts cache from the independently checked Home witness.
	target.provider = nil
	if _, err := host.facts(t.Context(), req, target); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("unavailable lookup: %v", err)
	}
	if len(host.cache) != 0 {
		t.Fatal("a failed lookup was cached as a complete plan")
	}
	available = true
	for range 2 {
		facts, err := host.facts(t.Context(), req, target)
		if err != nil || facts.Integration.State != personalassistant.FolderInstallInstall || facts.Hire.Mode != personalassistant.FolderHirePending || facts.Hire.Model != "" {
			t.Fatalf("no-model, uninstalled blueprint: %+v %v", facts, err)
		}
	}
	if reads != 2 {
		t.Fatalf("expected failed read then one cached successful read, got %d", reads)
	}
	host.mu.Lock()
	cached := host.cache["local|"+entry.Key]
	cached.at = time.Now().Add(-planFactsTTL - time.Second)
	host.cache["local|"+entry.Key] = cached
	host.mu.Unlock()
	if _, err := host.facts(t.Context(), req, target); err != nil || reads != 3 {
		t.Fatalf("expired facts did not recheck: reads=%d err=%v", reads, err)
	}
	host.forget("local", entry)
	if _, err := host.facts(t.Context(), req, target); err != nil || reads != 4 {
		t.Fatalf("explicit invalidation did not recheck: reads=%d err=%v", reads, err)
	}
}

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
