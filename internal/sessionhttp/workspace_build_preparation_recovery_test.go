package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type interruptedGrantStore struct {
	workspace.Store
	fail bool
}

func (s *interruptedGrantStore) Update(id string, mutate func(*workspace.Workspace) error) error {
	if s.fail {
		return errors.New("injected grant storage failure")
	}
	return s.Store.Update(id, mutate)
}

type splitPreparationOwnerStore struct{ workspace.Store }

func (s splitPreparationOwnerStore) Get(id string) (*workspace.Workspace, error) {
	ws, err := s.Store.Get(id)
	if err != nil || ws == nil {
		return ws, err
	}
	return &workspace.Workspace{ID: ws.ID, Status: ws.Status}, nil
}

func (s splitPreparationOwnerStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	ws, err := s.Store.Get(id)
	if err != nil || ws == nil {
		return ws, err
	}
	data, err := json.Marshal(ws)
	if err != nil {
		return nil, err
	}
	var mirror workspace.Workspace
	if err := json.Unmarshal(data, &mirror); err != nil {
		return nil, err
	}
	mirror.OwnerUserID = "foreign"
	return &mirror, nil
}

func TestWorkspaceBuildPreparation_BlankPrimaryOwnerIsLocalNotUnowned(t *testing.T) {
	f := newBuildPreparationFixture(t)
	request := f.review(t)
	if response := f.call("janitor-prepare", request); response.Code != http.StatusOK {
		t.Fatalf("prepare: %s", response.Body.String())
	}
	doc, _ := f.builds.Read(context.Background(), "local")
	f.h.workspaceTaskStore = splitPreparationOwnerStore{Store: f.workspaces}
	verified, failure := f.h.preparedBuildWorkspace("foreign", *doc.Session(f.build.ID), f.janitor)
	if verified != nil || failure == nil || failure.code != "creation_changed" {
		t.Fatalf("a foreign mirror adopted the local primary: %+v %+v", verified, failure)
	}
}

func TestWorkspaceBuildPreparation_FailedGrantReadsDoNotContinueAndRestartKeepsIdentity(t *testing.T) {
	f := newBuildPreparationFixture(t)
	primary := &interruptedGrantStore{Store: f.workspaces, fail: true}
	store := filejanitor.NewStore(f.h.workspaceStore)
	f.janitor = filejanitor.NewService(store, primary)
	f.h.workspaceBuild.FileJanitor = func() (*filejanitor.Service, filejanitor.PreparationAutomation) { return f.janitor, nil }
	request := f.review(t)
	failed := f.call("janitor-prepare", request)
	if failed.Code < 400 {
		t.Fatalf("grant failure claimed completion: %s", failed.Body.String())
	}
	doc, _ := f.builds.Read(context.Background(), "local")
	build := *doc.Session(f.build.ID)
	id := build.Preparation.WorkspaceID
	before, err := store.LoadSettings(id)
	if err != nil || before.SetupPreparation == nil || before.PendingFolderGrant == nil {
		t.Fatalf("missing partial authority: %+v %v", before, err)
	}
	original, _ := json.Marshal(before)
	primary.fail = false
	// Reconstruct the domain service, not a fabricated receipt or fresh claim.
	f.janitor = filejanitor.NewService(filejanitor.NewStore(f.h.workspaceStore), primary)
	f.folders.rootErr = errors.New("process-local hold lost")
	for range 3 {
		observed := f.h.ObserveWorkspaceBuildPreparation("local", build)
		if observed.State != "incomplete" || observed.WorkspaceID != id || observed.Preparation != nil {
			t.Fatalf("partial setup was continued or misreported: %+v", observed)
		}
	}
	after, _ := store.LoadSettings(id)
	unchanged, _ := json.Marshal(after)
	if string(original) != string(unchanged) {
		t.Fatal("status read continued the pending grant")
	}
	if batches, err := f.janitor.ListBatches(id); err != nil || len(batches) != 0 {
		t.Fatalf("status read scanned: %v %v", batches, err)
	}
	resumed := f.call("janitor-prepare", request)
	if resumed.Code != http.StatusOK {
		t.Fatalf("explicit continuation: %s", resumed.Body.String())
	}
	final, _ := store.LoadSettings(id)
	if final.SetupPreparation == nil || final.SetupPreparation.ID != before.SetupPreparation.ID {
		t.Fatal("continuation changed the accepted domain operation")
	}
	current, _ := f.builds.Read(context.Background(), "local")
	if *current.Session(f.build.ID).Preparation != *build.Preparation {
		t.Fatal("continuation changed the accepted build envelope")
	}
	if ids, _ := f.workspaces.List(); len(ids) != 1 {
		t.Fatalf("continuation created another workspace: %v", ids)
	}
	if batches, err := f.janitor.ListBatches(id); err != nil || len(batches) != 1 {
		t.Fatalf("wrong initial result: %v %v", batches, err)
	}
}

func TestWorkspaceBuildPreparation_LostReceiptRequiresReconciliationWithoutRepeatingEffects(t *testing.T) {
	f := newBuildPreparationFixture(t)
	request := f.review(t)
	if prepared := f.call("janitor-prepare", request); prepared.Code != http.StatusOK {
		t.Fatalf("prepare: %s", prepared.Body.String())
	}
	doc, _ := f.builds.Read(context.Background(), "local")
	build := *doc.Session(f.build.ID)
	id := build.Preparation.WorkspaceID
	store := filejanitor.NewStore(f.h.workspaceStore)
	settings, _ := store.LoadSettings(id)
	before, _ := json.Marshal(settings)
	if _, err := store.UpdateScanState(id, func(state *filejanitor.ScanState) error { state.ScanOperations = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	observed := f.h.ObserveWorkspaceBuildPreparation("local", build)
	if observed.State != "reconciliation_required" || observed.Preparation != nil || observed.WorkspaceID != id || observed.FolderSlug == "" {
		t.Fatalf("lost receipt became success or a reserved route: %+v", observed)
	}
	if retry := f.call("janitor-prepare", request); retry.Code < 400 {
		t.Fatalf("lost receipt was regenerated: %s", retry.Body.String())
	}
	afterSettings, _ := store.LoadSettings(id)
	after, _ := json.Marshal(afterSettings)
	if string(before) != string(after) {
		t.Fatal("reconciliation repeated grants or monitoring")
	}
	state, err := store.LoadScanState(id)
	if err != nil || len(state.Batches) != 1 || len(state.ScanOperations) != 0 {
		t.Fatalf("reconciliation rescanned: %+v %v", state, err)
	}
	if ids, _ := f.workspaces.List(); len(ids) != 1 {
		t.Fatalf("reconciliation recreated: %v", ids)
	}
}
