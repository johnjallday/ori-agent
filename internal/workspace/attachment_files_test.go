package workspace

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHydrateAttachmentFileMetaTracksWorkspaceOwnedFileStatus(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	ws := newTestWorkspace("ws-files", "Workspace Files")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	filesPath := store.GetFilesPath(ws.ID)
	relativePath := "abc12345_report.txt"
	absolutePath := filepath.Join(filesPath, relativePath)
	if err := os.WriteFile(absolutePath, []byte("report"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	meta := &AttachmentFileMeta{
		Name: "report.txt",
		URL:  workspaceFileURL(ws.ID, relativePath),
	}

	hydrated := HydrateAttachmentFileMeta(store, ws.ID, meta)
	if hydrated == nil {
		t.Fatal("expected hydrated metadata")
	}
	if hydrated.RelativePath != relativePath {
		t.Fatalf("expected relative path %q, got %q", relativePath, hydrated.RelativePath)
	}
	if hydrated.Status != string(AttachmentFileStatusOK) {
		t.Fatalf("expected status %q, got %q", AttachmentFileStatusOK, hydrated.Status)
	}

	if err := os.Remove(absolutePath); err != nil {
		t.Fatalf("Remove file: %v", err)
	}

	missing := HydrateAttachmentFileMeta(store, ws.ID, meta)
	if missing == nil {
		t.Fatal("expected hydrated metadata after delete")
	}
	if missing.Status != string(AttachmentFileStatusMissing) {
		t.Fatalf("expected status %q after delete, got %q", AttachmentFileStatusMissing, missing.Status)
	}
}

func TestHTTPHandlerRelinkAttachmentFileCopiesReplacementIntoWorkspace(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	ws := newTestWorkspace("ws-relink", "Relink Workspace")
	attachment := Attachment{
		ID:          "att-1",
		WorkspaceID: ws.ID,
		Title:       "Broken Spec",
		Type:        AttachmentTypeDoc,
		File: &AttachmentFileMeta{
			Name:         "spec.md",
			URL:          workspaceFileURL(ws.ID, "missing_spec.md"),
			RelativePath: "missing_spec.md",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := ws.AddAttachment(attachment); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewHTTPHandler(store, nil, nil)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileWriter, err := writer.CreateFormFile("file", "replacement.md")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fileWriter.Write([]byte("updated content")); err != nil {
		t.Fatalf("Write file content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/attachments/att-1/relink", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	handler.RelinkAttachmentFile(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	attachmentPayload, ok := response["attachment"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected attachment payload, got %#v", response["attachment"])
	}
	fileMeta, ok := attachmentPayload["file_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected file metadata, got %#v", attachmentPayload["file_meta"])
	}
	if got := fileMeta["status"]; got != string(AttachmentFileStatusOK) {
		t.Fatalf("expected status %q, got %v", AttachmentFileStatusOK, got)
	}

	relativePath, ok := fileMeta["relative_path"].(string)
	if !ok || relativePath == "" {
		t.Fatalf("expected relative_path in response, got %#v", fileMeta["relative_path"])
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	updatedAttachment, err := stored.GetAttachment("att-1")
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if updatedAttachment.File == nil {
		t.Fatal("expected updated attachment file metadata")
	}
	if updatedAttachment.File.RelativePath != relativePath {
		t.Fatalf("expected stored relative path %q, got %q", relativePath, updatedAttachment.File.RelativePath)
	}

	absolutePath := filepath.Join(store.GetFilesPath(ws.ID), relativePath)
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "updated content" {
		t.Fatalf("expected relinked file content to match upload, got %q", string(data))
	}
}
