package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMockClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			return recorder.Result(), nil
		}),
	}
}

func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{"valid simple", "test.txt", false},
		{"valid with spaces", "my file.txt", false},
		{"valid with dots", "file.backup.txt", false},
		{"empty", "", true},
		{"directory traversal dotdot", "../secret.txt", true},
		{"directory traversal nested", "foo/../bar", true},
		{"absolute path unix", "/etc/passwd", true},
		{"path separator slash", "foo/bar.txt", true},
		{"path separator backslash", "foo\\bar.txt", true},
		{"null byte", "file\x00.txt", true},
		{"colon", "file:name.txt", true},
		{"asterisk", "file*.txt", true},
		{"question mark", "file?.txt", true},
		{"quotes", "file\".txt", true},
		{"less than", "file<.txt", true},
		{"greater than", "file>.txt", true},
		{"pipe", "file|.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilename(tt.filename)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateFilename(%q) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
			}
		})
	}
}

func TestIsBinaryContent(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		content  []byte
		want     bool
	}{
		{"text plain", "text/plain", []byte("hello world"), false},
		{"text html", "text/html", []byte("<html></html>"), false},
		{"application json", "application/json", []byte(`{"key":"value"}`), false},
		{"application xml", "application/xml", []byte("<xml></xml>"), false},
		{"application javascript", "application/javascript", []byte("function(){}"), false},
		{"image png", "image/png", []byte{0x89, 0x50, 0x4E, 0x47}, true},
		{"application octet-stream", "application/octet-stream", []byte("data"), true},
		{"text with null byte", "text/plain", []byte("hello\x00world"), false}, // MIME type takes precedence
		{"binary pdf", "application/pdf", []byte("%PDF-1.4"), true},
		{"unknown with null byte", "application/unknown", []byte("data\x00binary"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBinaryContent(tt.mimeType, tt.content)
			if got != tt.want {
				t.Errorf("isBinaryContent(%q, %v) = %v, want %v", tt.mimeType, tt.content, got, tt.want)
			}
		})
	}
}

func TestSessionFilesParams_JSON(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   SessionFilesParams
		errMsg string
	}{
		{
			name:  "list operation",
			input: `{"operation":"list","session_id":"sess-123"}`,
			want: SessionFilesParams{
				Operation: "list",
				SessionID: "sess-123",
			},
		},
		{
			name:  "read operation",
			input: `{"operation":"read","session_id":"sess-123","file_id":"file-456","encoding":"text"}`,
			want: SessionFilesParams{
				Operation: "read",
				SessionID: "sess-123",
				FileID:    "file-456",
				Encoding:  "text",
			},
		},
		{
			name:  "write operation",
			input: `{"operation":"write","session_id":"sess-123","filename":"test.txt","content":"hello"}`,
			want: SessionFilesParams{
				Operation: "write",
				SessionID: "sess-123",
				Filename:  "test.txt",
				Content:   "hello",
			},
		},
		{
			name:  "modify operation with mode",
			input: `{"operation":"modify","session_id":"sess-123","file_id":"file-456","content":"appended","mode":"append"}`,
			want: SessionFilesParams{
				Operation: "modify",
				SessionID: "sess-123",
				FileID:    "file-456",
				Content:   "appended",
				Mode:      "append",
			},
		},
		{
			name:  "delete operation",
			input: `{"operation":"delete","session_id":"sess-123","file_id":"file-456"}`,
			want: SessionFilesParams{
				Operation: "delete",
				SessionID: "sess-123",
				FileID:    "file-456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got SessionFilesParams
			err := json.Unmarshal([]byte(tt.input), &got)
			if err != nil {
				t.Fatalf("JSON unmarshal failed: %v", err)
			}

			if got.Operation != tt.want.Operation {
				t.Errorf("Operation = %q, want %q", got.Operation, tt.want.Operation)
			}
			if got.SessionID != tt.want.SessionID {
				t.Errorf("SessionID = %q, want %q", got.SessionID, tt.want.SessionID)
			}
			if got.FileID != tt.want.FileID {
				t.Errorf("FileID = %q, want %q", got.FileID, tt.want.FileID)
			}
			if got.Filename != tt.want.Filename {
				t.Errorf("Filename = %q, want %q", got.Filename, tt.want.Filename)
			}
			if got.Content != tt.want.Content {
				t.Errorf("Content = %q, want %q", got.Content, tt.want.Content)
			}
			if got.Encoding != tt.want.Encoding {
				t.Errorf("Encoding = %q, want %q", got.Encoding, tt.want.Encoding)
			}
			if got.Mode != tt.want.Mode {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.want.Mode)
			}
		})
	}
}

func TestFileEntry_JSON(t *testing.T) {
	input := `{
		"id": "file-123",
		"name": "test.txt",
		"path": "/sessions/sess-1/files/test.txt",
		"size": 1024,
		"mime_type": "text/plain",
		"is_link": true,
		"original_path": "/home/user/test.txt",
		"status": "ok",
		"added_at": "2024-01-15T10:30:00Z"
	}`

	var entry FileEntry
	if err := json.Unmarshal([]byte(input), &entry); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if entry.ID != "file-123" {
		t.Errorf("ID = %q, want %q", entry.ID, "file-123")
	}
	if entry.Name != "test.txt" {
		t.Errorf("Name = %q, want %q", entry.Name, "test.txt")
	}
	if entry.Size != 1024 {
		t.Errorf("Size = %d, want %d", entry.Size, 1024)
	}
	if entry.MimeType != "text/plain" {
		t.Errorf("MimeType = %q, want %q", entry.MimeType, "text/plain")
	}
	if !entry.IsLink {
		t.Errorf("IsLink = %v, want %v", entry.IsLink, true)
	}
	if entry.Status != "ok" {
		t.Errorf("Status = %q, want %q", entry.Status, "ok")
	}
}

func TestListFilesResponse_JSON(t *testing.T) {
	input := `{
		"session_id": "sess-123",
		"files": [
			{"id": "f1", "name": "file1.txt", "size": 100, "mime_type": "text/plain", "status": "ok"},
			{"id": "f2", "name": "file2.txt", "size": 200, "mime_type": "text/plain", "status": "ok"}
		],
		"count": 2
	}`

	var resp ListFilesResponse
	if err := json.Unmarshal([]byte(input), &resp); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if resp.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", resp.SessionID, "sess-123")
	}
	if resp.Count != 2 {
		t.Errorf("Count = %d, want %d", resp.Count, 2)
	}
	if len(resp.Files) != 2 {
		t.Errorf("len(Files) = %d, want %d", len(resp.Files), 2)
	}
}

func TestBase64Encoding(t *testing.T) {
	original := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	encoded := base64.StdEncoding.EncodeToString(original)
	decoded, err := base64.StdEncoding.DecodeString(encoded)

	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Errorf("decoded length = %d, want %d", len(decoded), len(original))
	}

	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("decoded[%d] = %d, want %d", i, decoded[i], original[i])
		}
	}
}

// TestHandleListWithMockServer tests the list operation with a mock server
func TestHandleListWithMockServer(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		if r.URL.Path != "/api/sessions/test-session/files" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"session_id": "test-session",
			"files": [
				{"id": "f1", "name": "test.txt", "size": 100, "mime_type": "text/plain", "status": "ok"}
			],
			"count": 1
		}`))
	})

	tool := &sessionFilesTool{
		httpClient: newMockClient(handler),
	}

	result, err := tool.Call(context.Background(), `{"operation":"list","session_id":"test-session"}`)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	if !strings.Contains(result, "Files in session test-session") {
		t.Errorf("expected session header in response, got: %s", result)
	}

	if !strings.Contains(result, "test.txt") {
		t.Errorf("expected file name in response, got: %s", result)
	}
}

// TestHandleDeleteWithMockServer tests the delete operation with a mock server
func TestHandleDeleteWithMockServer(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}

		if r.URL.Path != "/api/sessions/test-session/files/file-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message": "File deleted"}`))
	})

	tool := &sessionFilesTool{
		httpClient: newMockClient(handler),
	}

	result, err := tool.Call(context.Background(), `{"operation":"delete","session_id":"test-session","file_id":"file-123"}`)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	if !strings.Contains(result, "deleted successfully") {
		t.Errorf("expected delete confirmation, got: %s", result)
	}
}

// TestOperationValidation tests that operations are properly validated
func TestOperationValidation(t *testing.T) {
	tool := &sessionFilesTool{
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	tests := []struct {
		name      string
		operation string
		wantErr   bool
	}{
		{"valid list", "list", false},
		{"valid read", "read", false},
		{"valid write", "write", false},
		{"valid modify", "modify", false},
		{"valid delete", "delete", false},
		{"invalid op", "invalid", true},
		{"empty op", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := `{"operation":"` + tt.operation + `","session_id":"test"}`
			if tt.operation == "" {
				args = `{"session_id":"test"}`
			}

			_, err := tool.Call(context.Background(), args)

			// All operations will fail because there's no actual server,
			// but invalid operations should fail with a specific error message
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "operation") {
					t.Errorf("expected operation error, got: %v", err)
				}
			}
		})
	}
}

// TestMissingSessionID tests that missing session_id is properly handled
func TestMissingSessionID(t *testing.T) {
	tool := &sessionFilesTool{
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	_, err := tool.Call(context.Background(), `{"operation":"list"}`)
	if err == nil {
		t.Error("expected error for missing session_id")
	}

	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("expected session_id error, got: %v", err)
	}
}

// TestMissingRequiredParams tests that required params are validated for each operation
func TestMissingRequiredParams(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "read without file_id",
			args:    `{"operation":"read","session_id":"test"}`,
			wantErr: "file_id is required",
		},
		{
			name:    "write without filename",
			args:    `{"operation":"write","session_id":"test","content":"data"}`,
			wantErr: "filename is required",
		},
		{
			name:    "write without content",
			args:    `{"operation":"write","session_id":"test","filename":"test.txt"}`,
			wantErr: "content is required",
		},
		{
			name:    "modify without file_id",
			args:    `{"operation":"modify","session_id":"test","content":"data"}`,
			wantErr: "file_id is required",
		},
		{
			name:    "modify without content",
			args:    `{"operation":"modify","session_id":"test","file_id":"123"}`,
			wantErr: "content is required",
		},
		{
			name:    "delete without file_id",
			args:    `{"operation":"delete","session_id":"test"}`,
			wantErr: "file_id is required",
		},
	}

	tool := &sessionFilesTool{
		httpClient: newMockClient(handler),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Call(context.Background(), tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
				return
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}
