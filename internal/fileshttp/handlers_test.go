package fileshttp

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/sessionfiles"
)

func setupTestHandler(t *testing.T) (*Handler, string) {
	tmpDir, err := os.MkdirTemp("", "fileshttp-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil) // No watcher for basic tests

	return handler, tmpDir
}

func createTestFile(t *testing.T, dir, name, content string) string {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	return path
}

func TestHandler_UploadFile(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	_, _ = part.Write([]byte("hello world"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/files/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	handler.UploadFile(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["message"] != "File uploaded successfully" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

func TestHandler_UploadFile_NoFile(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/files/upload", nil)

	rr := httptest.NewRecorder()
	handler.UploadFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_LinkFile(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a file to link
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")

	body := map[string]string{"path": srcPath}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/files/link", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.LinkFile(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_LinkFile_InvalidPath(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	body := map[string]string{"path": "/nonexistent/file.txt"}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/files/link", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.LinkFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_ListFiles(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload a file first
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	_, _ = handler.store.AddFile("session-1", srcPath, "test.txt")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/files", nil)

	rr := httptest.NewRecorder()
	handler.ListFiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	count := int(resp["count"].(float64))
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

func TestHandler_ListFiles_Empty(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/files", nil)

	rr := httptest.NewRecorder()
	handler.ListFiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	count := int(resp["count"].(float64))
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

func TestHandler_GetFile(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Add a file
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	entry, _ := handler.store.AddFile("session-1", srcPath, "test.txt")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/files/"+entry.ID, nil)

	rr := httptest.NewRecorder()
	handler.GetFile(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_GetFile_NotFound(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/files/nonexistent", nil)

	rr := httptest.NewRecorder()
	handler.GetFile(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandler_DeleteFile(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Add a file
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	entry, _ := handler.store.AddFile("session-1", srcPath, "test.txt")

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/session-1/files/"+entry.ID, nil)

	rr := httptest.NewRecorder()
	handler.DeleteFile(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify file is deleted
	files, _ := handler.store.ListFiles("session-1")
	if len(files) != 0 {
		t.Errorf("expected 0 files after delete, got %d", len(files))
	}
}

func TestHandler_DeleteFile_NotFound(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/session-1/files/nonexistent", nil)

	rr := httptest.NewRecorder()
	handler.DeleteFile(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandler_DownloadFile(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Add a file
	srcPath := createTestFile(t, tmpDir, "source.txt", "hello download")
	entry, _ := handler.store.AddFile("session-1", srcPath, "test.txt")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/files/"+entry.ID+"/download", nil)

	rr := httptest.NewRecorder()
	handler.DownloadFile(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Check content disposition header
	contentDisp := rr.Header().Get("Content-Disposition")
	if !strings.Contains(contentDisp, "test.txt") {
		t.Errorf("expected Content-Disposition to contain filename, got: %s", contentDisp)
	}
}

func TestHandler_ValidateLinks(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create and link a file
	srcPath := createTestFile(t, tmpDir, "original.txt", "content")
	_, _ = handler.store.LinkFile("session-1", srcPath, "linked.txt")

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/files/validate", nil)

	rr := httptest.NewRecorder()
	handler.ValidateLinks(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	count := int(resp["count"].(float64))
	if count != 0 {
		t.Errorf("expected 0 broken links, got %d", count)
	}
}

func TestHandler_OpenFolder(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Note: We can't fully test this without actually opening a folder
	// but we can test the handler accepts the request
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/folder/open", nil)

	rr := httptest.NewRecorder()
	handler.OpenFolder(rr, req)

	// Should succeed (or fail to start command, depending on environment)
	// We mainly want to ensure it doesn't panic
	if rr.Code != http.StatusOK && rr.Code != http.StatusInternalServerError {
		t.Errorf("unexpected status %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tests := []struct {
		name    string
		method  string
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"UploadFile GET", http.MethodGet, "/api/sessions/session-1/files/upload", handler.UploadFile},
		{"LinkFile GET", http.MethodGet, "/api/sessions/session-1/files/link", handler.LinkFile},
		{"ListFiles POST", http.MethodPost, "/api/sessions/session-1/files", handler.ListFiles},
		{"GetFile POST", http.MethodPost, "/api/sessions/session-1/files/123", handler.GetFile},
		{"DeleteFile GET", http.MethodGet, "/api/sessions/session-1/files/123", handler.DeleteFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			tt.handler(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected status 405, got %d", rr.Code)
			}
		})
	}
}

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		path     string
		prefix   string
		suffix   string
		expected string
	}{
		{"/api/sessions/abc123/files/upload", "/api/sessions/", "/files/upload", "abc123"},
		{"/api/sessions/session-1/files", "/api/sessions/", "/files", "session-1"},
		{"/api/sessions/test/folder/open", "/api/sessions/", "/folder/open", "test"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := extractSessionID(tt.path, tt.prefix, tt.suffix)
			if result != tt.expected {
				t.Errorf("extractSessionID(%s) = %s, want %s", tt.path, result, tt.expected)
			}
		})
	}
}

func TestExtractSessionAndFileID(t *testing.T) {
	tests := []struct {
		path            string
		expectedSession string
		expectedFile    string
	}{
		{"/api/sessions/session-1/files/file-123", "session-1", "file-123"},
		{"/api/sessions/abc/files/xyz", "abc", "xyz"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			sessionID, fileID := extractSessionAndFileID(tt.path)
			if sessionID != tt.expectedSession {
				t.Errorf("expected session '%s', got '%s'", tt.expectedSession, sessionID)
			}
			if fileID != tt.expectedFile {
				t.Errorf("expected file '%s', got '%s'", tt.expectedFile, fileID)
			}
		})
	}
}

func TestHandler_ReadFile(t *testing.T) {
	handler, tmpDir := setupTestHandler(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Add a file
	srcPath := createTestFile(t, tmpDir, "source.txt", "test content")
	entry, _ := handler.store.AddFile("session-1", srcPath, "test.txt")

	reader, fileEntry, err := handler.ReadFile("session-1", entry.ID)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	defer func() { _ = reader.Close() }()

	if fileEntry.Name != "test.txt" {
		t.Errorf("expected name 'test.txt', got '%s'", fileEntry.Name)
	}

	content, _ := io.ReadAll(reader)
	if string(content) != "test content" {
		t.Errorf("expected content 'test content', got '%s'", string(content))
	}
}
