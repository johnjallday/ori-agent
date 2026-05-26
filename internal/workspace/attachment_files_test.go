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
	defer func() { _ = store.Close() }()

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

func TestHydrateAttachmentFileMetaTracksNestedWorkspaceOwnedFileStatus(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := newTestWorkspace("ws-nested-files", "Nested Workspace Files")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	relativePath := filepath.Join("research", "abc12345_report.txt")
	absolutePath := filepath.Join(store.GetFilesPath(ws.ID), relativePath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absolutePath, []byte("report"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	meta := &AttachmentFileMeta{
		Name:         "report.txt",
		RelativePath: relativePath,
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
}

func TestStoreWorkspaceFileNestedFolder(t *testing.T) {
	filesPath := t.TempDir()

	stored, err := storeWorkspaceFile(filesPath, bytes.NewBufferString("nested content"), "report.txt", "research/notes")
	if err != nil {
		t.Fatalf("storeWorkspaceFile: %v", err)
	}
	if filepath.Dir(stored.RelativePath) != filepath.Join("research", "notes") {
		t.Fatalf("expected nested relative path under research/notes, got %q", stored.RelativePath)
	}

	data, err := os.ReadFile(filepath.Join(filesPath, stored.RelativePath))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "nested content" {
		t.Fatalf("expected stored content to match, got %q", string(data))
	}
}

func TestStoreWorkspaceFileRejectsTraversalFolder(t *testing.T) {
	filesPath := t.TempDir()

	if _, err := storeWorkspaceFile(filesPath, bytes.NewBufferString("nope"), "report.txt", "../outside"); err == nil {
		t.Fatal("expected traversal folder path to be rejected")
	}
}

func TestStoreWorkspaceFileRejectsSymlinkFolderEscape(t *testing.T) {
	filesPath := t.TempDir()
	outsideDir := t.TempDir()
	linkPath := filepath.Join(filesPath, "escape")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	if _, err := storeWorkspaceFile(filesPath, bytes.NewBufferString("nope"), "report.txt", "escape"); err == nil {
		t.Fatal("expected symlink folder path to be rejected")
	}
	if entries, err := os.ReadDir(outsideDir); err != nil {
		t.Fatalf("ReadDir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("expected no files outside workspace, got %d entries", len(entries))
	}
}

func TestWorkspaceFileURLPreservesNestedSlashStructure(t *testing.T) {
	got := workspaceFileURL("workspace-1", filepath.Join("research", "draft notes", "final #1.txt"))
	want := "/api/workspaces/workspace-1/files/research/draft%20notes/final%20%231.txt"
	if got != want {
		t.Fatalf("expected URL %q, got %q", want, got)
	}
}

func TestHTTPHandlerServeFileSupportsNestedPaths(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := newTestWorkspace("ws-serve-nested", "Serve Nested")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	relativePath := filepath.Join("research", "notes", "report.txt")
	absolutePath := filepath.Join(store.GetFilesPath(ws.ID), relativePath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absolutePath, []byte("nested report"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	handler := NewHTTPHandler(store, nil, nil)
	req := httptest.NewRequest(http.MethodGet, workspaceFileURL(ws.ID, relativePath), nil)
	rr := httptest.NewRecorder()

	handler.ServeFile(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "nested report" {
		t.Fatalf("expected nested report content, got %q", rr.Body.String())
	}
}

func TestHTTPHandlerServeFileRejectsTraversal(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := newTestWorkspace("ws-serve-traversal", "Serve Traversal")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewHTTPHandler(store, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/files/../workspace.json", nil)
	rr := httptest.NewRecorder()

	handler.ServeFile(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPHandlerServeFileRejectsSymlinkEscape(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := newTestWorkspace("ws-serve-symlink", "Serve Symlink")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(store.GetFilesPath(ws.ID), "escape")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	handler := NewHTTPHandler(store, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/files/escape/secret.txt", nil)
	rr := httptest.NewRecorder()

	handler.ServeFile(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() == "secret" {
		t.Fatal("expected symlink target content not to be served")
	}
}

func TestHTTPHandlerUploadFileStoresBytesInSelectedFolder(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := newTestWorkspace("ws-upload-folder", "Upload Folder")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewHTTPHandler(store, nil, nil)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("folder_path", "research/notes"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	fileWriter, err := writer.CreateFormFile("file", "report.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fileWriter.Write([]byte("uploaded report")); err != nil {
		t.Fatalf("Write file content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	handler.UploadFile(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var response struct {
		Attachment Attachment `json:"attachment"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Attachment.File == nil {
		t.Fatal("expected attachment file metadata")
	}
	if filepath.Dir(response.Attachment.File.RelativePath) != filepath.Join("research", "notes") {
		t.Fatalf("expected upload under research/notes, got %q", response.Attachment.File.RelativePath)
	}
	data, err := os.ReadFile(filepath.Join(store.GetFilesPath(ws.ID), response.Attachment.File.RelativePath))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "uploaded report" {
		t.Fatalf("expected uploaded bytes to be stored, got %q", string(data))
	}
}

func TestHTTPHandlerCreateAttachmentPreservesFolderPrefixedRelativePath(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := newTestWorkspace("ws-metadata-file", "Metadata File")
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewHTTPHandler(store, nil, nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/"+ws.ID+"/attachments",
		bytes.NewBufferString(`{"title":"Report","type":"doc","file_meta":{"name":"report.txt","relative_path":"research/report.txt"}}`),
	)
	rr := httptest.NewRecorder()

	handler.CreateAttachment(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var response struct {
		Attachment Attachment `json:"attachment"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Attachment.File == nil {
		t.Fatal("expected file metadata")
	}
	if response.Attachment.File.RelativePath != filepath.Join("research", "report.txt") {
		t.Fatalf("expected nested relative path, got %q", response.Attachment.File.RelativePath)
	}
	if response.Attachment.File.URL != "/api/workspaces/"+ws.ID+"/files/research/report.txt" {
		t.Fatalf("expected nested workspace file URL, got %q", response.Attachment.File.URL)
	}
}

func TestHTTPHandlerRelinkAttachmentFileCopiesReplacementIntoWorkspace(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

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

	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	attachmentPayload, ok := response["attachment"].(map[string]any)
	if !ok {
		t.Fatalf("expected attachment payload, got %#v", response["attachment"])
	}
	fileMeta, ok := attachmentPayload["file_meta"].(map[string]any)
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

func TestHTTPHandlerRelinkAttachmentFilePreservesExistingFolder(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := newTestWorkspace("ws-relink-nested", "Relink Nested Workspace")
	attachment := Attachment{
		ID:          "att-1",
		WorkspaceID: ws.ID,
		Title:       "Nested Spec",
		Type:        AttachmentTypeDoc,
		File: &AttachmentFileMeta{
			Name:         "spec.md",
			URL:          workspaceFileURL(ws.ID, filepath.Join("research", "missing_spec.md")),
			RelativePath: filepath.Join("research", "missing_spec.md"),
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
	if _, err := fileWriter.Write([]byte("updated nested content")); err != nil {
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
	if got := filepath.Dir(updatedAttachment.File.RelativePath); got != "research" {
		t.Fatalf("expected relinked file to remain under research, got %q", got)
	}
	data, err := os.ReadFile(filepath.Join(store.GetFilesPath(ws.ID), updatedAttachment.File.RelativePath))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "updated nested content" {
		t.Fatalf("expected relinked file content to match upload, got %q", string(data))
	}
}
