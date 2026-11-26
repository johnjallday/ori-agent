//go:build darwin || linux

package filejanitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

func stationFixture(t *testing.T) (*Service, *workspace.Workspace, string, *StationLook) {
	t.Helper()
	s, store, root, ref := linkedSetupFixture(t)
	ws, _ := store.Get("ws-1")
	ws.DiscardInstalledCapability(workspace.CapabilityFileJanitor)
	ws.Status = workspace.StatusActive
	ws.SharedData = map[string]any{"anchor_directory_reference_id": ref, "primary_directory_id": ref}
	ws.AgentInstances = []workspace.AgentInstance{{ID: "lead", Name: "Manager", EntryPoint: true}}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	look := StationLook{ref, "Manager", diridentity.Of(info), strings.Repeat("a", 64)}
	reg, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	s.SetStationHost(func(string, string) (StationLook, error) { return look, nil }, workspacecapability.NewService(reg, store))
	return s, ws, root, &look
}
func TestStationBuildUsesReviewedPreparationWithoutCreatingFiled(t *testing.T) {
	s, ws, root, _ := stationFixture(t)
	path := filepath.Join(root, "invoice.txt")
	if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	review, err := s.ReviewStation(ws.ID, "local", "", false)
	if err != nil || review.State != "review" || !review.BuildStation || ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatalf("read-only offer: %+v, %v", review, err)
	}
	req := LinkedPreparationRequest{BuildStation: true, WorkspaceID: ws.ID, OwnerUserID: "local", ReferenceID: review.SelectedReference, ReviewRevision: review.Revision}
	res, err := s.PrepareLinkedSetup(req, nil)
	if err != nil || res.Result == nil || res.Result.EligibleCount != 1 {
		t.Fatalf("build: %+v, %v", res, err)
	}
	if !res.Status.Settings.ManualScansOnly() || !res.Status.Settings.DeferredDestination {
		t.Fatal("unexpected build settings")
	}
	for _, check := range res.Status.Readiness.Checks {
		if check.Component == ComponentDestination && check.Status != ComponentOK {
			t.Fatalf("destination: %+v", check)
		}
	}
	again, err := s.PrepareLinkedSetup(req, nil)
	if err != nil || again.Result == nil || again.Result.BatchID != res.Result.BatchID || again.OperationID != res.OperationID {
		t.Fatalf("replay: %+v, %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatalf("build wrote Filed/: %v", err)
	}
}
func TestStationBuildRefusesChangedOfferBeforeInstall(t *testing.T) {
	for _, change := range []string{"look", "folder", "manager", "mode", "removed", "owner"} {
		t.Run(change, func(t *testing.T) {
			s, ws, root, look := stationFixture(t)
			review, err := s.ReviewStation(ws.ID, "local", "", false)
			if err != nil {
				t.Fatal(err)
			}
			req := LinkedPreparationRequest{BuildStation: true, WorkspaceID: ws.ID, OwnerUserID: "local", ReferenceID: review.SelectedReference, ReviewRevision: review.Revision}
			switch change {
			case "look":
				look.Revision = strings.Repeat("b", 64)
			case "folder":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				mkdir(t, root)
			case "manager":
				ws.AgentInstances[0].ID = "other"
			case "mode":
				req.Monitoring = true
			case "removed":
				_, err := ws.AddInstalledCapability(stationInstallRecord(time.Now()))
				if err != nil {
					t.Fatal(err)
				}
				ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
			case "owner":
				req.OwnerUserID = "another"
			}
			if _, err := s.PrepareLinkedSetup(req, nil); err == nil {
				t.Fatal("changed offer built")
			}
			settings, err := s.store.LoadSettings(ws.ID)
			if err != nil || settings.IsSetUp() || settings.SetupPreparation != nil || ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
				t.Fatal("changed offer had effects")
			}
		})
	}
}
