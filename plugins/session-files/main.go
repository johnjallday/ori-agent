package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/oriagent/ori-pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// sessionFilesTool provides file management operations for agent sessions
type sessionFilesTool struct {
	pluginapi.BasePlugin
	httpClient *http.Client
}

// SessionFilesParams represents the parameters for this plugin
type SessionFilesParams struct {
	Operation string `json:"operation"`
	SessionID string `json:"session_id"`
	FileID    string `json:"file_id,omitempty"`
	Filename  string `json:"filename,omitempty"`
	Content   string `json:"content,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

// FileEntry represents a file in the session
type FileEntry struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	MimeType     string `json:"mime_type"`
	IsLink       bool   `json:"is_link"`
	OriginalPath string `json:"original_path,omitempty"`
	Status       string `json:"status"`
	AddedAt      string `json:"added_at"`
}

// ListFilesResponse represents the response from list files API
type ListFilesResponse struct {
	SessionID string      `json:"session_id"`
	Files     []FileEntry `json:"files"`
	Count     int         `json:"count"`
}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(t *sessionFilesTool, ctx context.Context, params *SessionFilesParams) (string, error)

// operationRegistry maps operation names to their handler functions
var operationRegistry = map[string]OperationHandler{
	"list":   handleList,
	"read":   handleRead,
	"write":  handleWrite,
	"modify": handleModify,
	"delete": handleDelete,
}

// Call implements the PluginTool interface
func (t *sessionFilesTool) Call(ctx context.Context, args string) (string, error) {
	var params SessionFilesParams

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Validate required fields
	if params.Operation == "" {
		return "", fmt.Errorf("required field 'operation' is missing")
	}
	if params.SessionID == "" {
		return "", fmt.Errorf("required field 'session_id' is missing")
	}

	// Initialize HTTP client if needed
	if t.httpClient == nil {
		t.httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return t.Execute(ctx, &params)
}

// Execute contains the business logic
func (t *sessionFilesTool) Execute(ctx context.Context, params *SessionFilesParams) (string, error) {
	// Audit logging
	t.logAudit(params)

	// Look up handler in registry
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s. Valid operations: list, read, write, modify, delete", params.Operation)
	}

	// Execute the handler
	return handler(t, ctx, params)
}

// logAudit logs the operation for audit purposes
func (t *sessionFilesTool) logAudit(params *SessionFilesParams) {
	sm := t.Settings()
	if sm == nil {
		return
	}

	auditEnabled, _ := sm.GetBool("audit_logging")
	if !auditEnabled {
		return
	}

	// Get agent context for logging
	agentCtx := t.GetAgentContext()
	agentName := agentCtx.Name
	if agentName == "" {
		agentName = "unknown"
	}

	logEntry := fmt.Sprintf("[%s] Agent=%s Session=%s Operation=%s FileID=%s Filename=%s\n",
		time.Now().Format(time.RFC3339),
		agentName,
		params.SessionID,
		params.Operation,
		params.FileID,
		params.Filename,
	)

	// Log to stderr (captured by plugin system)
	log.Print(logEntry)
}

// getServerURL returns the server URL from settings or default
func (t *sessionFilesTool) getServerURL() string {
	sm := t.Settings()
	if sm != nil {
		if url, err := sm.GetString("server_url"); err == nil && url != "" {
			return url
		}
	}
	return "http://localhost:8765"
}

// Operation handlers

func handleList(t *sessionFilesTool, ctx context.Context, params *SessionFilesParams) (string, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/files", t.getServerURL(), params.SessionID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to list files: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var listResp ListFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Format the response nicely
	if listResp.Count == 0 {
		return "No files in session.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files in session %s (%d files):\n\n", params.SessionID, listResp.Count))

	for _, f := range listResp.Files {
		sb.WriteString(fmt.Sprintf("- **%s** (ID: %s)\n", f.Name, f.ID))
		sb.WriteString(fmt.Sprintf("  - Size: %d bytes\n", f.Size))
		sb.WriteString(fmt.Sprintf("  - Type: %s\n", f.MimeType))
		if f.IsLink {
			sb.WriteString(fmt.Sprintf("  - Linked from: %s\n", f.OriginalPath))
			if f.Status == "broken" {
				sb.WriteString("  - Status: BROKEN LINK\n")
			}
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func handleRead(t *sessionFilesTool, ctx context.Context, params *SessionFilesParams) (string, error) {
	if params.FileID == "" {
		return "", fmt.Errorf("file_id is required for read operation")
	}

	// First get file metadata to check type and size
	metaURL := fmt.Sprintf("%s/api/sessions/%s/files/%s", t.getServerURL(), params.SessionID, params.FileID)
	metaReq, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	metaResp, err := t.httpClient.Do(metaReq)
	if err != nil {
		return "", fmt.Errorf("failed to get file metadata: %w", err)
	}
	defer func() { _ = metaResp.Body.Close() }()

	if metaResp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("file not found: %s", params.FileID)
	}
	if metaResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(metaResp.Body)
		return "", fmt.Errorf("server error (%d): %s", metaResp.StatusCode, string(body))
	}

	var fileEntry FileEntry
	if err := json.NewDecoder(metaResp.Body).Decode(&fileEntry); err != nil {
		return "", fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Check if file is broken link
	if fileEntry.Status == "broken" {
		return "", fmt.Errorf("cannot read file: symlink is broken (original file no longer exists)")
	}

	// Check max read size
	maxSize := int64(10 * 1024 * 1024) // Default 10MB
	sm := t.Settings()
	if sm != nil {
		if maxSizeVal, err := sm.GetInt("max_read_size"); err == nil && maxSizeVal > 0 {
			maxSize = int64(maxSizeVal)
		}
	}

	if fileEntry.Size > maxSize {
		return "", fmt.Errorf("file too large (%d bytes, max %d bytes)", fileEntry.Size, maxSize)
	}

	// Download the file content
	downloadURL := fmt.Sprintf("%s/api/sessions/%s/files/%s/download", t.getServerURL(), params.SessionID, params.FileID)
	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	downloadResp, err := t.httpClient.Do(downloadReq)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer func() { _ = downloadResp.Body.Close() }()

	if downloadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(downloadResp.Body)
		return "", fmt.Errorf("download error (%d): %s", downloadResp.StatusCode, string(body))
	}

	content, err := io.ReadAll(downloadResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read content: %w", err)
	}

	// Determine if we should return as base64
	isBinary := isBinaryContent(fileEntry.MimeType, content)
	wantBase64 := params.Encoding == "base64" || (params.Encoding == "" && isBinary)

	if wantBase64 {
		encoded := base64.StdEncoding.EncodeToString(content)
		return fmt.Sprintf("File: %s\nMIME Type: %s\nEncoding: base64\nContent:\n%s", fileEntry.Name, fileEntry.MimeType, encoded), nil
	}

	return fmt.Sprintf("File: %s\nMIME Type: %s\nContent:\n%s", fileEntry.Name, fileEntry.MimeType, string(content)), nil
}

func handleWrite(t *sessionFilesTool, ctx context.Context, params *SessionFilesParams) (string, error) {
	if params.Filename == "" {
		return "", fmt.Errorf("filename is required for write operation")
	}
	if params.Content == "" {
		return "", fmt.Errorf("content is required for write operation")
	}

	// Validate filename (prevent directory traversal)
	if err := validateFilename(params.Filename); err != nil {
		return "", err
	}

	// Decode content if base64
	var content []byte
	if params.Encoding == "base64" {
		var err error
		content, err = base64.StdEncoding.DecodeString(params.Content)
		if err != nil {
			return "", fmt.Errorf("invalid base64 content: %w", err)
		}
	} else {
		content = []byte(params.Content)
	}

	// Create multipart form request
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", params.Filename)
	if err != nil {
		return "", fmt.Errorf("failed to create form: %w", err)
	}

	if _, err := part.Write(content); err != nil {
		return "", fmt.Errorf("failed to write content: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close form: %w", err)
	}

	// Upload the file
	url := fmt.Sprintf("%s/api/sessions/%s/files/upload", t.getServerURL(), params.SessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	fileID := ""
	if f, ok := result["file"].(map[string]interface{}); ok {
		if id, ok := f["id"].(string); ok {
			fileID = id
		}
	}

	return fmt.Sprintf("File '%s' created successfully (ID: %s)", params.Filename, fileID), nil
}

func handleModify(t *sessionFilesTool, ctx context.Context, params *SessionFilesParams) (string, error) {
	if params.FileID == "" {
		return "", fmt.Errorf("file_id is required for modify operation")
	}
	if params.Content == "" {
		return "", fmt.Errorf("content is required for modify operation")
	}

	mode := params.Mode
	if mode == "" {
		mode = "replace"
	}

	if mode != "replace" && mode != "append" && mode != "prepend" {
		return "", fmt.Errorf("invalid mode: %s. Valid modes: replace, append, prepend", mode)
	}

	// For append/prepend, we need to read existing content first
	var newContent []byte

	if mode != "replace" {
		// Read existing content
		readParams := &SessionFilesParams{
			Operation: "read",
			SessionID: params.SessionID,
			FileID:    params.FileID,
			Encoding:  "text",
		}
		existingResult, err := handleRead(t, ctx, readParams)
		if err != nil {
			return "", fmt.Errorf("failed to read existing file: %w", err)
		}

		// Extract just the content part (after "Content:\n")
		parts := strings.SplitN(existingResult, "Content:\n", 2)
		existingContent := ""
		if len(parts) > 1 {
			existingContent = parts[1]
		}

		// Combine content based on mode
		var addContent []byte
		if params.Encoding == "base64" {
			addContent, _ = base64.StdEncoding.DecodeString(params.Content)
		} else {
			addContent = []byte(params.Content)
		}

		if mode == "append" {
			newContent = append([]byte(existingContent), addContent...)
		} else { // prepend
			newContent = append(addContent, []byte(existingContent)...)
		}
	} else {
		// Replace mode - just use the new content
		if params.Encoding == "base64" {
			var err error
			newContent, err = base64.StdEncoding.DecodeString(params.Content)
			if err != nil {
				return "", fmt.Errorf("invalid base64 content: %w", err)
			}
		} else {
			newContent = []byte(params.Content)
		}
	}

	// Get the file info to get the filename
	metaURL := fmt.Sprintf("%s/api/sessions/%s/files/%s", t.getServerURL(), params.SessionID, params.FileID)
	metaReq, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	metaResp, err := t.httpClient.Do(metaReq)
	if err != nil {
		return "", fmt.Errorf("failed to get file metadata: %w", err)
	}
	defer func() { _ = metaResp.Body.Close() }()

	if metaResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("file not found: %s", params.FileID)
	}

	var fileEntry FileEntry
	if err := json.NewDecoder(metaResp.Body).Decode(&fileEntry); err != nil {
		return "", fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Delete the old file
	deleteURL := fmt.Sprintf("%s/api/sessions/%s/files/%s", t.getServerURL(), params.SessionID, params.FileID)
	deleteReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create delete request: %w", err)
	}

	deleteResp, err := t.httpClient.Do(deleteReq)
	if err != nil {
		return "", fmt.Errorf("failed to delete old file: %w", err)
	}
	_ = deleteResp.Body.Close()

	// Create the new file with the same name
	writeParams := &SessionFilesParams{
		Operation: "write",
		SessionID: params.SessionID,
		Filename:  fileEntry.Name,
		Content:   string(newContent),
		Encoding:  "text",
	}

	result, err := handleWrite(t, ctx, writeParams)
	if err != nil {
		return "", fmt.Errorf("failed to write modified file: %w", err)
	}

	return fmt.Sprintf("File '%s' modified successfully (%s mode)\n%s", fileEntry.Name, mode, result), nil
}

func handleDelete(t *sessionFilesTool, ctx context.Context, params *SessionFilesParams) (string, error) {
	if params.FileID == "" {
		return "", fmt.Errorf("file_id is required for delete operation")
	}

	url := fmt.Sprintf("%s/api/sessions/%s/files/%s", t.getServerURL(), params.SessionID, params.FileID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to delete file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("file not found: %s", params.FileID)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("delete failed (%d): %s", resp.StatusCode, string(body))
	}

	return fmt.Sprintf("File '%s' deleted successfully", params.FileID), nil
}

// Helper functions

func validateFilename(filename string) error {
	// Check for empty filename
	if filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}

	// Check for directory traversal attempts
	if strings.Contains(filename, "..") {
		return fmt.Errorf("filename cannot contain '..'")
	}

	// Check for absolute paths
	if filepath.IsAbs(filename) {
		return fmt.Errorf("filename cannot be an absolute path")
	}

	// Check for path separators
	if strings.ContainsAny(filename, "/\\") {
		return fmt.Errorf("filename cannot contain path separators")
	}

	// Check for invalid characters
	invalidChars := []string{"\x00", ":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range invalidChars {
		if strings.Contains(filename, char) {
			return fmt.Errorf("filename contains invalid character: %q", char)
		}
	}

	return nil
}

func isBinaryContent(mimeType string, content []byte) bool {
	// Check MIME type first
	textTypes := []string{
		"text/",
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-yaml",
		"application/yaml",
	}

	// If it's a known text type, it's not binary
	for _, t := range textTypes {
		if strings.HasPrefix(mimeType, t) {
			return false
		}
	}

	// Known binary MIME types
	binaryTypes := []string{
		"image/",
		"audio/",
		"video/",
		"application/octet-stream",
		"application/pdf",
		"application/zip",
		"application/gzip",
		"application/x-tar",
		"application/x-executable",
	}

	for _, t := range binaryTypes {
		if strings.HasPrefix(mimeType, t) {
			return true
		}
	}

	// For unknown types, check content for binary indicators (null bytes)
	for _, b := range content {
		if b == 0 {
			return true
		}
	}

	return false
}

func main() {
	pluginapi.ServeGRPCPlugin(&sessionFilesTool{}, configYAML)
}
