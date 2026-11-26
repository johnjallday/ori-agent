//go:build darwin || linux

package filejanitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type unavailableStationInstaller struct{}

func (unavailableStationInstaller) InstallFilingStation(string, string, time.Time, func(*workspace.Workspace) error) error {
	return errors.New("install unavailable")
}

func TestStopBeforeInstallAllowsOnlyAFreshReview(t *testing.T) {
	s, ws, _, _ := stationFixture(t)
	s.stationInstaller = unavailableStationInstaller{}
	r, err := s.ReviewStation(ws.ID, "local", "", false)
	if err != nil {
		t.Fatal(err)
	}
	req := LinkedPreparationRequest{BuildStation: true, WorkspaceID: ws.ID, OwnerUserID: "local", ReferenceID: r.SelectedReference, ReviewRevision: r.Revision}
	attempt, err := s.PrepareLinkedSetup(req, nil)
	if err == nil || attempt.OperationID == "" {
		t.Fatal("expected an interrupted install")
	}
	p, err := s.StationProgress("local", ws.ID)
	if err != nil || !p.Repair || p.Part != "plot" {
		t.Fatalf("before stop: %+v %v", p, err)
	}
	if _, err = s.StopLinkedPreparation(ws.ID, "local", attempt.OperationID); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.ReviewStation(ws.ID, "local", "", false)
	if err != nil || fresh.State != "review" || fresh.Revision == r.Revision {
		t.Fatalf("fresh review: %+v, %v", fresh, err)
	}
	if _, err = s.PrepareLinkedSetup(req, nil); err == nil || ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("old press revived stopped station")
	}
}

func TestStationPartsComeFromCurrentPreparationReceipts(t *testing.T) {
	s, ws, _, _ := stationFixture(t)
	const owner = "local"
	part := func(want string, repair bool) StationBuildProgress {
		t.Helper()
		p, err := s.StationProgress(owner, ws.ID)
		if err != nil || p.Part != want || p.Repair != repair {
			t.Fatalf("want %s repair %v; got %+v, %v", want, repair, p, err)
		}
		return p
	}
	part("plot", false)
	r, err := s.ReviewStation(ws.ID, owner, "", false)
	if err != nil {
		t.Fatal(err)
	}
	built, err := s.PrepareLinkedSetup(LinkedPreparationRequest{BuildStation: true, WorkspaceID: ws.ID, OwnerUserID: owner, ReferenceID: r.SelectedReference, ReviewRevision: r.Revision}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := part("open", false); p.Eligible != 0 || p.Operation != built.OperationID || p.Counted || p.Running {
		t.Fatal(p)
	}
	if _, err := s.StationProgress("another", ws.ID); err == nil {
		t.Fatal("foreign owner read progress")
	}
	change := func(f func(*JanitorSettings)) {
		t.Helper()
		if _, err := s.store.UpdateSettings(ws.ID, func(v *JanitorSettings) error { f(v); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	change(func(v *JanitorSettings) { v.SetupPreparation.ScanReceiptDigest = "" })
	part("roof", true)
	if _, err := s.store.UpdateScanState(ws.ID, func(v *ScanState) error { v.ScanOperations = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	part("walls", true)
	ctx, finish := s.buildActivity.start(context.Background(), owner, ws.ID, built.OperationID)
	if p := part("walls", false); !p.Looking || !p.Running || p.Counted {
		t.Fatal(p)
	}
	s.buildActivity.count(built.OperationID, 2, 6)
	if p := part("walls", false); !p.Counted || p.Total != 6 || p.Checked != 2 {
		t.Fatal(p)
	}
	s.buildActivity.stop(owner, "another", built.OperationID)
	s.buildActivity.stop("another", ws.ID, built.OperationID)
	s.buildActivity.stop(owner, ws.ID, "other")
	if ctx.Err() != nil {
		t.Fatal("wrong stop cancelled the scan")
	}
	s.buildActivity.stop(owner, ws.ID, built.OperationID)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("exact stop did not cancel the scan")
	}
	finish()
	if p := part("walls", true); p.Looking || p.Counted {
		t.Fatal("interrupted scan retained live progress")
	}
	change(func(v *JanitorSettings) { v.LastFolderGrant = nil })
	part("foundation", true)
	if _, err := s.StopLinkedPreparation(ws.ID, owner, built.OperationID); err != nil {
		t.Fatal(err)
	}
	if p := part("foundation", true); p.Operation != "" {
		t.Fatal("stopped operation is still active")
	}
}
