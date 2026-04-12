package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHandleWorkspaceMarksMissingAttachmentFiles(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Attachment Status")

	workspaceRecord, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}

	attachments := []agentworkspace.Attachment{
		{
			ID:          "att-missing",
			WorkspaceID: workspaceID,
			Title:       "Missing File",
			Type:        agentworkspace.AttachmentTypeDoc,
			File: &agentworkspace.AttachmentFileMeta{
				Name:         "spec.md",
				URL:          "/api/workspaces/" + workspaceID + "/files/spec.md",
				RelativePath: "spec.md",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}

	data, err := json.Marshal(attachments)
	if err != nil {
		t.Fatalf("Marshal attachments: %v", err)
	}
	workspaceRecord.AttachmentsJSON = data
	if err := handler.store.UpdateWorkspace(context.Background(), workspaceRecord); err != nil {
		t.Fatalf("UpdateWorkspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID, nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	attachmentList, ok := response["attachments"].([]interface{})
	if !ok || len(attachmentList) != 1 {
		t.Fatalf("expected one attachment in response, got %#v", response["attachments"])
	}

	attachmentPayload, ok := attachmentList[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected attachment payload map, got %#v", attachmentList[0])
	}
	fileMeta, ok := attachmentPayload["file_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected file metadata map, got %#v", attachmentPayload["file_meta"])
	}

	if got := fileMeta["status"]; got != string(agentworkspace.AttachmentFileStatusMissing) {
		t.Fatalf("expected file status %q, got %v", agentworkspace.AttachmentFileStatusMissing, got)
	}
}
