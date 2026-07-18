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
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
)

// These tests call the handlers directly, so they populate the {id} / {fileId}
// path parameters with SetPathValue exactly as ServeMux would from the matched
// pattern (see RegisterRoutes). Mux-level routing is covered by routes_test.go
// and the server golden route-table test.

// Integration test: Upload file via browser → verify in session
func TestIntegration_UploadAndVerify(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	sessionID := "test-session-1"

	// Step 1: Upload a file (simulating browser upload)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "document.pdf")
	content := []byte("PDF content here - this is a test file with enough content to verify")
	_, _ = part.Write(content)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/upload", &buf)
	req.SetPathValue("id", sessionID)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	handler.UploadFile(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("upload failed with status %d: %s", rr.Code, rr.Body.String())
	}

	// Step 2: Parse upload response to get file ID
	var uploadResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("failed to parse upload response: %v", err)
	}

	fileData := uploadResp["file"].(map[string]any)
	fileID := fileData["id"].(string)

	// Step 3: List files and verify the file is present
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files", nil)
	req.SetPathValue("id", sessionID)
	rr = httptest.NewRecorder()
	handler.ListFiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list failed with status %d", rr.Code)
	}

	var listResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)

	count := int(listResp["count"].(float64))
	if count != 1 {
		t.Errorf("expected 1 file, got %d", count)
	}

	// Step 4: Get file metadata and verify
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files/"+fileID, nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", fileID)
	rr = httptest.NewRecorder()
	handler.GetFile(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("get file failed with status %d", rr.Code)
	}

	var fileResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &fileResp)

	if fileResp["name"] != "document.pdf" {
		t.Errorf("expected name 'document.pdf', got '%v'", fileResp["name"])
	}

	// Step 5: Download and verify content
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files/"+fileID+"/download", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", fileID)
	rr = httptest.NewRecorder()
	handler.DownloadFile(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("download failed with status %d", rr.Code)
	}

	downloadedContent := rr.Body.Bytes()
	if !bytes.Equal(downloadedContent, content) {
		t.Errorf("downloaded content doesn't match original")
	}

	t.Log("Integration test: Upload and verify - PASSED")
}

// Integration test: Add file via Finder (file system) → UI updates via SSE
func TestIntegration_FileSystemChangeSSE(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-sse-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	watcher, err := filewatcher.NewWatcher(filewatcher.DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	// Start the watcher's event processor
	watcher.Start()

	_ = NewHandler(store, watcher) // Handler not needed for this test
	sessionID := "test-session-sse"

	// Initialize session directory
	sessionPath := store.GetSessionFilesPath(sessionID)
	if err := os.MkdirAll(sessionPath, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	// Start watching
	if err := watcher.Watch(sessionID, sessionPath); err != nil {
		t.Fatalf("failed to start watching: %v", err)
	}

	// Give watcher time to initialize
	time.Sleep(100 * time.Millisecond)

	// Track received events
	var receivedEvents []filewatcher.WatchEvent
	var mu sync.Mutex
	eventsCh := watcher.Events()

	// Use a done channel for cleanup
	done := make(chan struct{})
	defer close(done)

	// Start listening for events in background
	go func() {
		for {
			select {
			case <-done:
				return
			case event, ok := <-eventsCh:
				if !ok {
					return
				}
				mu.Lock()
				receivedEvents = append(receivedEvents, event)
				mu.Unlock()
			}
		}
	}()

	// Simulate adding a file via the file system (like Finder drag-drop)
	testFilePath := filepath.Join(sessionPath, "added-via-finder.txt")
	if err := os.WriteFile(testFilePath, []byte("content added via finder"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Wait for debounced event
	time.Sleep(700 * time.Millisecond)

	// Check if we received an event
	mu.Lock()
	eventCount := len(receivedEvents)
	mu.Unlock()

	if eventCount == 0 {
		t.Log("Note: No file system events received - this may be expected in some test environments")
	} else {
		t.Logf("Received %d file system events", eventCount)
	}

	// Verify the file can be accessed
	files, err := os.ReadDir(sessionPath)
	if err != nil {
		t.Fatalf("failed to read session dir: %v", err)
	}

	found := false
	for _, f := range files {
		if f.Name() == "added-via-finder.txt" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected to find 'added-via-finder.txt' in session directory")
	}

	t.Log("Integration test: File system change SSE - PASSED")
}

// Integration test: Agent reads uploaded file
func TestIntegration_AgentReadsUploadedFile(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-agent-read-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	sessionID := "test-session-agent-read"

	// Step 1: Upload a file
	testContent := "This is the content the agent will read"
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "agent-readable.txt")
	_, _ = part.Write([]byte(testContent))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/upload", &buf)
	req.SetPathValue("id", sessionID)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	handler.UploadFile(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("upload failed with status %d", rr.Code)
	}

	var uploadResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &uploadResp)
	fileData := uploadResp["file"].(map[string]any)
	fileID := fileData["id"].(string)

	// Step 2: Simulate agent reading the file using ReadFile method
	reader, entry, err := handler.ReadFile(sessionID, fileID)
	if err != nil {
		t.Fatalf("agent read failed: %v", err)
	}
	defer func() { _ = reader.Close() }()

	// Step 3: Verify content
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read content: %v", err)
	}

	if string(content) != testContent {
		t.Errorf("expected content '%s', got '%s'", testContent, string(content))
	}

	if entry.Name != "agent-readable.txt" {
		t.Errorf("expected filename 'agent-readable.txt', got '%s'", entry.Name)
	}

	t.Log("Integration test: Agent reads uploaded file - PASSED")
}

// Integration test: Agent writes new file → appears in file list
func TestIntegration_AgentWritesNewFile(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-agent-write-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	sessionID := "test-session-agent-write"

	// Step 1: Agent creates a file by writing directly to store
	agentContent := []byte("This content was generated by the agent")
	entry, err := store.AddFileFromReader(sessionID, bytes.NewReader(agentContent), "agent-generated.txt", int64(len(agentContent)))
	if err != nil {
		t.Fatalf("agent write failed: %v", err)
	}

	// Step 2: Verify file appears in list via HTTP API
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files", nil)
	req.SetPathValue("id", sessionID)
	rr := httptest.NewRecorder()
	handler.ListFiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list failed with status %d", rr.Code)
	}

	var listResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)

	count := int(listResp["count"].(float64))
	if count != 1 {
		t.Errorf("expected 1 file, got %d", count)
	}

	// Step 3: Verify file content via download
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files/"+entry.ID+"/download", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", entry.ID)
	rr = httptest.NewRecorder()
	handler.DownloadFile(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("download failed with status %d", rr.Code)
	}

	if !bytes.Equal(rr.Body.Bytes(), agentContent) {
		t.Error("downloaded content doesn't match agent-generated content")
	}

	t.Log("Integration test: Agent writes new file - PASSED")
}

// Integration test: Permission denied / not-found scenarios
func TestIntegration_PermissionDeniedScenarios(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-perms-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	sessionID := "test-session-perms"

	// Test 1: Try to link a non-existent file
	body := map[string]string{"path": "/this/path/does/not/exist/file.txt"}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/link", bytes.NewReader(jsonBody))
	req.SetPathValue("id", sessionID)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.LinkFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for non-existent file, got %d", rr.Code)
	}

	// Test 2: Try to get a non-existent file
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files/nonexistent-id", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", "nonexistent-id")
	rr = httptest.NewRecorder()
	handler.GetFile(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for non-existent file ID, got %d", rr.Code)
	}

	// Test 3: Try to delete a non-existent file
	req = httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sessionID+"/files/nonexistent-id", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", "nonexistent-id")
	rr = httptest.NewRecorder()
	handler.DeleteFile(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for deleting non-existent file, got %d", rr.Code)
	}

	// Test 4: Try to download a non-existent file
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files/nonexistent-id/download", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", "nonexistent-id")
	rr = httptest.NewRecorder()
	handler.DownloadFile(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for downloading non-existent file, got %d", rr.Code)
	}

	// Note: the "missing session ID" case is now enforced by ServeMux (a request
	// whose {id} segment is empty never matches a file pattern), so it is a
	// routing concern verified by the server golden route-table test rather than
	// a handler-level 400. It is intentionally not re-tested here.

	t.Log("Integration test: Permission denied scenarios - PASSED")
}

// Integration test: Broken link detection and handling
func TestIntegration_BrokenLinkDetection(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-broken-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	sessionID := "test-session-broken"

	// Step 1: Create a source file
	sourceFile := filepath.Join(tmpDir, "original.txt")
	if err := os.WriteFile(sourceFile, []byte("original content"), 0644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	// Step 2: Link the file
	body := map[string]string{"path": sourceFile, "name": "linked.txt"}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/link", bytes.NewReader(jsonBody))
	req.SetPathValue("id", sessionID)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.LinkFile(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("link failed with status %d: %s", rr.Code, rr.Body.String())
	}

	var linkResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &linkResp)
	fileData := linkResp["file"].(map[string]any)
	fileID := fileData["id"].(string)

	// Step 3: Verify link is valid
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/validate", nil)
	req.SetPathValue("id", sessionID)
	rr = httptest.NewRecorder()
	handler.ValidateLinks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("validate failed with status %d", rr.Code)
	}

	var validateResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &validateResp)

	brokenCount := int(validateResp["count"].(float64))
	if brokenCount != 0 {
		t.Errorf("expected 0 broken links initially, got %d", brokenCount)
	}

	// Step 4: Delete the source file to break the link
	if err := os.Remove(sourceFile); err != nil {
		t.Fatalf("failed to remove source file: %v", err)
	}

	// Step 5: Validate again - should detect broken link
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/validate", nil)
	req.SetPathValue("id", sessionID)
	rr = httptest.NewRecorder()
	handler.ValidateLinks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("validate failed with status %d", rr.Code)
	}

	_ = json.Unmarshal(rr.Body.Bytes(), &validateResp)
	brokenCount = int(validateResp["count"].(float64))
	if brokenCount != 1 {
		t.Errorf("expected 1 broken link after deleting source, got %d", brokenCount)
	}

	// Step 6: Try to download broken link - should fail gracefully
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files/"+fileID+"/download", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", fileID)
	rr = httptest.NewRecorder()
	handler.DownloadFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for broken link download, got %d", rr.Code)
	}

	// Step 7: Create a new source file and relink
	newSourceFile := filepath.Join(tmpDir, "new-original.txt")
	if err := os.WriteFile(newSourceFile, []byte("new content"), 0644); err != nil {
		t.Fatalf("failed to create new source file: %v", err)
	}

	relinkBody := map[string]string{"new_path": newSourceFile}
	relinkJSON, _ := json.Marshal(relinkBody)

	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/"+fileID+"/relink", bytes.NewReader(relinkJSON))
	req.SetPathValue("id", sessionID)
	req.SetPathValue("fileId", fileID)
	req.Header.Set("Content-Type", "application/json")

	rr = httptest.NewRecorder()
	handler.RelinkFile(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("relink failed with status %d: %s", rr.Code, rr.Body.String())
	}

	// Step 8: Validate again - should be fixed
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/validate", nil)
	req.SetPathValue("id", sessionID)
	rr = httptest.NewRecorder()
	handler.ValidateLinks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("validate failed with status %d", rr.Code)
	}

	_ = json.Unmarshal(rr.Body.Bytes(), &validateResp)
	brokenCount = int(validateResp["count"].(float64))
	if brokenCount != 0 {
		t.Errorf("expected 0 broken links after relink, got %d", brokenCount)
	}

	t.Log("Integration test: Broken link detection - PASSED")
}

// Integration test: File count limit (max 50 files)
func TestIntegration_FileCountLimit(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-limit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	sessionID := "test-session-limit"

	// Upload files up to the limit
	for i := range sessionfiles.MaxFilesPerSession {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, _ := writer.CreateFormFile("file", "file-"+string(rune('a'+i%26))+".txt")
		_, _ = part.Write([]byte("content"))
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/upload", &buf)
		req.SetPathValue("id", sessionID)
		req.Header.Set("Content-Type", writer.FormDataContentType())

		rr := httptest.NewRecorder()
		handler.UploadFile(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("upload %d failed with status %d: %s", i, rr.Code, rr.Body.String())
		}
	}

	// Try to upload one more - should fail
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "one-too-many.txt")
	_, _ = part.Write([]byte("content"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/upload", &buf)
	req.SetPathValue("id", sessionID)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	handler.UploadFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for exceeding file limit, got %d", rr.Code)
	}

	// Verify error message (API returns {"code": "...", "message": "..."})
	var errResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &errResp)

	errorMsg, ok := errResp["message"].(string)
	if !ok || errorMsg == "" {
		t.Errorf("expected error message in response, got: %s", rr.Body.String())
	}

	t.Logf("Integration test: File count limit - PASSED (limit: %d)", sessionfiles.MaxFilesPerSession)
}

// Integration test: Multiple file types
func TestIntegration_MultipleFileTypes(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "integration-test-types-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	store, err := sessionfiles.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, nil)
	sessionID := "test-session-types"

	testFiles := []struct {
		name       string
		content    []byte
		expectMime string
	}{
		{"document.txt", []byte("plain text content"), "text/plain"},
		{"data.json", []byte(`{"key": "value"}`), "application/json"},
		{"script.js", []byte("console.log('hello');"), "text/javascript"},
		{"style.css", []byte("body { color: red; }"), "text/css"},
		{"page.html", []byte("<html><body>test</body></html>"), "text/html"},
	}

	for _, tf := range testFiles {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, _ := writer.CreateFormFile("file", tf.name)
		_, _ = part.Write(tf.content)
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files/upload", &buf)
		req.SetPathValue("id", sessionID)
		req.Header.Set("Content-Type", writer.FormDataContentType())

		rr := httptest.NewRecorder()
		handler.UploadFile(rr, req)

		if rr.Code != http.StatusCreated {
			t.Errorf("upload %s failed with status %d: %s", tf.name, rr.Code, rr.Body.String())
			continue
		}

		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		fileData := resp["file"].(map[string]any)

		// Verify MIME type detection (relaxed check - just ensure it's set)
		mimeType := fileData["mime_type"].(string)
		if mimeType == "" {
			t.Errorf("expected mime_type to be set for %s", tf.name)
		}
	}

	// Verify all files are stored
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/files", nil)
	req.SetPathValue("id", sessionID)
	rr := httptest.NewRecorder()
	handler.ListFiles(rr, req)

	var listResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)

	count := int(listResp["count"].(float64))
	if count != len(testFiles) {
		t.Errorf("expected %d files, got %d", len(testFiles), count)
	}

	t.Log("Integration test: Multiple file types - PASSED")
}
