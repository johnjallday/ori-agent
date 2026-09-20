package filejanitorhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/pathselection"
)

type fakeAssistantSetupCoordinator struct {
	calls         int
	owner         string
	workspace     string
	token         string
	authorization assistantsetup.FolderGrantAuthorization
	err           error
}

func (f *fakeAssistantSetupCoordinator) CommitFolderGrant(
	_ context.Context, owner, workspaceID, token string,
	commit func(assistantsetup.FolderGrantAuthorization) (assistantsetup.FolderGrantResult, error),
) (*assistantsetup.Projection, error) {
	f.calls++
	f.owner, f.workspace, f.token = owner, workspaceID, token
	if f.err != nil {
		return nil, f.err
	}
	if _, err := commit(f.authorization); err != nil {
		return nil, err
	}
	return &assistantsetup.Projection{ViewState: "needs_decision"}, nil
}

func TestAssistedSetupAcceptsOnlyScopedOpaqueSelection(t *testing.T) {
	handler, _ := newTestHandler(t, map[string]string{"ws-1": "local"})
	selections := pathselection.NewStore()
	root := filepath.Join(t.TempDir(), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	token, err := selections.IssueFor(root, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &fakeAssistantSetupCoordinator{authorization: assistantsetup.FolderGrantAuthorization{
		OwnerUserID: "local", RunID: "run-1", OperationID: "folder-op-1", WorkspaceID: "ws-1", RunRevision: 2,
	}}
	handler.SetPathSelectionResolver(selections)
	handler.SetAssistantSetupCoordinator(coordinator)
	body := `{"selection_token":"` + token + `","assistant_setup_token":"opaque-setup-token","paused":true}`
	recorder, decoded := serve(t, handler, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", body)
	if recorder.Code != http.StatusOK || coordinator.calls != 1 || coordinator.owner != "local" || coordinator.workspace != "ws-1" {
		t.Fatalf("status=%d coordinator=%+v body=%s", recorder.Code, coordinator, recorder.Body.String())
	}
	status := decoded["status"].(map[string]any)
	settings := status["settings"].(map[string]any)
	if settings["paused"] != true || strings.Contains(recorder.Body.String(), root) {
		t.Fatalf("unsafe assisted response: %s", recorder.Body.String())
	}
}

func TestAssistedSetupFolderConflictReturnsOnlyAnOwnedWorkspaceRoute(t *testing.T) {
	handler, workspaces := newTestHandler(t, map[string]string{"ws-1": "local", "ws-2": "local"})
	workspaces.workspaces["ws-2"].FolderSlug = "ws-2"
	recorder := httptest.NewRecorder()
	handler.respondAssistedFolderConflict(recorder, &filejanitor.SetupError{
		Code: filejanitor.CodeFolderConflict, Message: "That folder is already managed.",
		Repair: filejanitor.RepairChooseFolder, ConflictWorkspaceID: "ws-2",
	}, "local")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	details := decoded["details"].(map[string]any)
	if details["conflict_route"] != "/workspaces/ws-2?panel=file-janitor" {
		t.Fatalf("conflict details = %+v", details)
	}

	recorder = httptest.NewRecorder()
	handler.respondAssistedFolderConflict(recorder, &filejanitor.SetupError{
		Code: filejanitor.CodeFolderConflict, Message: "That folder is already managed.",
		Repair: filejanitor.RepairChooseFolder, ConflictWorkspaceID: "ws-2",
	}, "other-owner")
	decoded = map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if leaked := decoded["details"].(map[string]any)["conflict_route"]; leaked != nil {
		t.Fatalf("cross-owner route leaked: %v", leaked)
	}
}

func TestAssistedSetupRejectsRawPathAndCrossWorkspaceSelectionBeforeCoordinator(t *testing.T) {
	handler, _ := newTestHandler(t, map[string]string{"ws-1": "local"})
	selections := pathselection.NewStore()
	root := filepath.Join(t.TempDir(), "Inbox")
	token, err := selections.IssueFor(root, "other-workspace")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &fakeAssistantSetupCoordinator{}
	handler.SetPathSelectionResolver(selections)
	handler.SetAssistantSetupCoordinator(coordinator)

	body := `{"selection_token":"` + token + `","assistant_setup_token":"setup-token","paused":true,"path":"/private/raw"}`
	recorder, _ := serve(t, handler, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", body)
	if recorder.Code != http.StatusBadRequest || coordinator.calls != 0 {
		t.Fatalf("raw path status=%d calls=%d", recorder.Code, coordinator.calls)
	}
	body = `{"selection_token":"` + token + `","assistant_setup_token":"setup-token","paused":true}`
	recorder, _ = serve(t, handler, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", body)
	if recorder.Code != http.StatusConflict || coordinator.calls != 0 {
		t.Fatalf("cross-workspace status=%d calls=%d body=%s", recorder.Code, coordinator.calls, recorder.Body.String())
	}
}
