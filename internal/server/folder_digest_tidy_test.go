package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// fakeTidyCoordinator scripts the assistant-led setup run the tidy outcome
// drives: a proposal, an accepted run at the folder step, a grant, and a
// first review.
type fakeTidyCoordinator struct {
	proposalMode assistantsetup.TargetMode
	activeRun    *assistantsetup.Run
	target       *assistantsetup.Target
	calls        []string
	granted      bool
	reviewRoute  string
}

func (c *fakeTidyCoordinator) Get(context.Context, string, string) (*assistantsetup.Projection, error) {
	c.calls = append(c.calls, "get")
	p := &assistantsetup.Projection{}
	if c.activeRun != nil {
		p.Run = c.activeRun
		return p, nil
	}
	if c.target != nil {
		p.Target = c.target
		return p, nil
	}
	p.Proposal = &assistantsetup.Proposal{Revision: strings.Repeat("a", 64), Mode: c.proposalMode}
	return p, nil
}

func (c *fakeTidyCoordinator) Accept(_ context.Context, _ string, revision, _ string) (*assistantsetup.Projection, bool, error) {
	c.calls = append(c.calls, "accept:"+revision[:4])
	return &assistantsetup.Projection{Run: &assistantsetup.Run{ID: "run-1", Revision: 2, CurrentStep: assistantsetup.StepFolder, TargetWorkspaceID: "ws-janitor"}}, true, nil
}

func (c *fakeTidyCoordinator) BeginFolderIntent(_ context.Context, _ string, runID string, ifVersion int64) (*assistantsetup.Projection, string, error) {
	c.calls = append(c.calls, "intent")
	if runID != "run-1" || ifVersion != 2 {
		return nil, "", assistantsetup.ErrStaleRun
	}
	return &assistantsetup.Projection{Run: &assistantsetup.Run{ID: "run-1", Revision: 3, CurrentStep: assistantsetup.StepFolder, TargetWorkspaceID: "ws-janitor"}}, "token-1", nil
}

func (c *fakeTidyCoordinator) CommitFolderGrant(_ context.Context, _ string, workspaceID, token string, commit func(assistantsetup.FolderGrantAuthorization) (assistantsetup.FolderGrantResult, error)) (*assistantsetup.Projection, error) {
	c.calls = append(c.calls, "grant")
	if token != "token-1" || workspaceID != "ws-janitor" {
		return nil, assistantsetup.ErrNotFound
	}
	if _, err := commit(assistantsetup.FolderGrantAuthorization{RunID: "run-1", OperationID: "op-folder", WorkspaceID: workspaceID}); err != nil {
		return nil, err
	}
	c.granted = true
	return &assistantsetup.Projection{
		Run:        &assistantsetup.Run{ID: "run-1", Revision: 4, CurrentStep: assistantsetup.StepMonitoring, TargetWorkspaceID: "ws-janitor"},
		Monitoring: &assistantsetup.MonitoringReview{Revision: strings.Repeat("b", 64)},
	}, nil
}

func (c *fakeTidyCoordinator) PrepareReview(_ context.Context, _ string, runID string, ifVersion int64, reviewRevision string) (*assistantsetup.Projection, error) {
	c.calls = append(c.calls, "review")
	if runID != "run-1" || ifVersion != 4 || reviewRevision != strings.Repeat("b", 64) {
		return nil, assistantsetup.ErrStaleRun
	}
	return &assistantsetup.Projection{FirstResult: &assistantsetup.FirstResult{BatchID: "batch-1", ReviewRoute: c.reviewRoute}}, nil
}

type fakeTidyJanitor struct {
	owner    *filejanitor.RootOwner
	granted  []filejanitor.SetupRequest
	grantErr error
	rootPath string
}

func (j *fakeTidyJanitor) FolderOwner(string) (*filejanitor.RootOwner, bool) {
	return j.owner, j.owner != nil
}

func (j *fakeTidyJanitor) ConfirmSetup(req filejanitor.SetupRequest) (filejanitor.Status, error) {
	j.granted = append(j.granted, req)
	if j.grantErr != nil {
		return filejanitor.Status{}, j.grantErr
	}
	var status filejanitor.Status
	status.Settings.RootID = "root-1"
	status.Settings.DirectoryReferenceID = "dir-1"
	return status, nil
}

func (j *fakeTidyJanitor) Status(string) (filejanitor.Status, error) {
	var status filejanitor.Status
	status.Settings.RootPath = j.rootPath
	return status, nil
}

func newTidyRunner(coordinator *fakeTidyCoordinator, janitor *fakeTidyJanitor) folderTidyRunner {
	return folderTidyRunner{
		coordinator: func() tidyCoordinator { return coordinator },
		janitor:     func() tidyJanitor { return janitor },
		route:       func(id string) string { return "/workspaces/" + id + "?panel=file-janitor" },
	}
}

func TestFolderTidyRunner_CreatesOneWorkspaceAndLandsOnTheFirstReview(t *testing.T) {
	coordinator := &fakeTidyCoordinator{proposalMode: assistantsetup.TargetCreate, reviewRoute: "/workspaces/file-janitor?panel=file-janitor&batch_id=batch-1"}
	janitor := &fakeTidyJanitor{}
	runner := newTidyRunner(coordinator, janitor)

	result, err := runner.TidyFolder(context.Background(), personalassistant.FolderTidyRequest{
		UserID: "local", OfferID: "offer-1", Name: "Downloads", Path: "/Users/me/Downloads", RequestID: "req-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceID != "ws-janitor" || result.Route != coordinator.reviewRoute || result.BatchID != "batch-1" || result.Existing {
		t.Fatalf("result=%+v", result)
	}
	if got := strings.Join(coordinator.calls, ","); got != "get,accept:aaaa,intent,grant,review" {
		t.Fatalf("calls=%s", got)
	}
	if len(janitor.granted) != 1 {
		t.Fatalf("grants=%+v", janitor.granted)
	}
	grant := janitor.granted[0]
	if grant.WorkspaceID != "ws-janitor" || grant.Path != "/Users/me/Downloads" || grant.Paused == nil || !*grant.Paused ||
		grant.Operation == nil || grant.Operation.RunID != "run-1" || grant.Operation.OperationID != "op-folder" {
		t.Fatalf("grant=%+v", grant)
	}
}

func TestFolderTidyRunner_OpensTheWorkspaceThatAlreadyOwnsTheFolder(t *testing.T) {
	coordinator := &fakeTidyCoordinator{proposalMode: assistantsetup.TargetCreate}
	janitor := &fakeTidyJanitor{owner: &filejanitor.RootOwner{WorkspaceID: "ws-existing", WorkspaceName: "File Janitor", Root: "/Users/me"}}
	result, err := newTidyRunner(coordinator, janitor).TidyFolder(context.Background(), personalassistant.FolderTidyRequest{
		UserID: "local", OfferID: "offer-1", Name: "Downloads", Path: "/Users/me/Downloads", RequestID: "req-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Existing || result.WorkspaceID != "ws-existing" || result.Route != "/workspaces/ws-existing?panel=file-janitor" || result.Note == "" {
		t.Fatalf("result=%+v", result)
	}
	if len(coordinator.calls) != 0 || len(janitor.granted) != 0 {
		t.Fatalf("an owned folder started a setup: calls=%v grants=%d", coordinator.calls, len(janitor.granted))
	}
}

func TestFolderTidyRunner_ConflictDuringGrantOpensTheOwner(t *testing.T) {
	coordinator := &fakeTidyCoordinator{proposalMode: assistantsetup.TargetCreate}
	janitor := &fakeTidyJanitor{grantErr: &filejanitor.SetupError{Code: filejanitor.CodeFolderConflict, Message: "taken", ConflictWorkspaceID: "ws-other"}}
	result, err := newTidyRunner(coordinator, janitor).TidyFolder(context.Background(), personalassistant.FolderTidyRequest{
		UserID: "local", OfferID: "offer-1", Name: "Downloads", Path: "/Users/me/Downloads", RequestID: "req-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Existing || result.WorkspaceID != "ws-other" || coordinator.granted {
		t.Fatalf("result=%+v granted=%v", result, coordinator.granted)
	}
}

func TestFolderTidyRunner_InFlightRunAndUnavailableCollaborators(t *testing.T) {
	coordinator := &fakeTidyCoordinator{activeRun: &assistantsetup.Run{ID: "run-9", TargetWorkspaceID: "ws-running", CurrentStep: assistantsetup.StepMonitoring}}
	result, err := newTidyRunner(coordinator, &fakeTidyJanitor{}).TidyFolder(context.Background(), personalassistant.FolderTidyRequest{
		UserID: "local", OfferID: "offer-1", Name: "Downloads", Path: "/Users/me/Downloads", RequestID: "req-1",
	})
	if err != nil || !result.Existing || result.WorkspaceID != "ws-running" {
		t.Fatalf("in-flight result=%+v err=%v", result, err)
	}
	unavailable := folderTidyRunner{coordinator: func() tidyCoordinator { return nil }, janitor: func() tidyJanitor { return nil }, route: func(string) string { return "" }}
	if _, err := unavailable.TidyFolder(context.Background(), personalassistant.FolderTidyRequest{Path: "/x"}); !errors.Is(err, personalassistant.ErrFolderOutcomeUnavailable) {
		t.Fatalf("unavailable err=%v", err)
	}
	if _, err := newTidyRunner(&fakeTidyCoordinator{}, &fakeTidyJanitor{}).TidyFolder(context.Background(), personalassistant.FolderTidyRequest{}); !errors.Is(err, personalassistant.ErrFolderPathLost) {
		t.Fatalf("empty path err=%v", err)
	}
}
