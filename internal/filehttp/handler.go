package filehttp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/fileparser"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

type Handler struct {
	uploadsDir string
}

func NewHandler() *Handler {
	uploadsDir := "uploads"
	// Ensure uploads directory exists
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		logger.Error("Failed to create uploads directory", logger.Fields{"error": err})
	}
	return &Handler{uploadsDir: uploadsDir}
}

type ParseFileRequest struct {
	Filename string `json:"filename"`
	Content  string `json:"content"` // base64 encoded
}

type ParseFileResponse struct {
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

// ParseFileHandler handles file parsing requests
func (h *Handler) ParseFileHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req ParseFileRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Decode base64 content

	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		if err := json.NewEncoder(w).Encode(ParseFileResponse{
			Error: "Failed to decode file content: " + err.Error(),
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Validate file size
	if err := fileparser.ValidateFileSize(int64(len(data))); err != nil {
		if err := json.NewEncoder(w).Encode(ParseFileResponse{
			Error: err.Error(),
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Parse file
	text, err := fileparser.ParseFile(req.Filename, data)
	if err != nil {
		if err := json.NewEncoder(w).Encode(ParseFileResponse{
			Error: "Failed to parse file: " + err.Error(),
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	if err := json.NewEncoder(w).Encode(ParseFileResponse{
		Text: text,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// UploadFileRequest represents a file upload request
type UploadFileRequest struct {
	Filename string `json:"filename"`
	Content  string `json:"content"` // base64 encoded
	MimeType string `json:"mime_type"`
}

// UploadFileResponse represents a file upload response
type UploadFileResponse struct {
	Path  string `json:"path"`
	Error string `json:"error,omitempty"`
}

// UploadFileHandler handles file uploads to the uploads/ folder
func (h *Handler) UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req UploadFileRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Decode base64 content
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		_ = json.NewEncoder(w).Encode(UploadFileResponse{Error: "Failed to decode file content"})
		return
	}

	// Create date-based subdirectory
	dateDir := time.Now().Format("2006-01")
	uploadPath := filepath.Join(h.uploadsDir, dateDir)
	if err := os.MkdirAll(uploadPath, 0755); err != nil {
		_ = json.NewEncoder(w).Encode(UploadFileResponse{Error: "Failed to create upload directory"})
		return
	}

	// Sanitize filename and make unique
	safeFilename := sanitizeFilename(req.Filename)
	finalPath := filepath.Join(uploadPath, safeFilename)

	// If file exists, add timestamp to make unique
	if _, err := os.Stat(finalPath); err == nil {
		ext := filepath.Ext(safeFilename)
		name := strings.TrimSuffix(safeFilename, ext)
		safeFilename = fmt.Sprintf("%s_%d%s", name, time.Now().UnixNano(), ext)
		finalPath = filepath.Join(uploadPath, safeFilename)
	}

	// Write file
	if err := os.WriteFile(finalPath, data, 0644); err != nil {
		_ = json.NewEncoder(w).Encode(UploadFileResponse{Error: "Failed to save file"})
		return
	}

	// Return relative path
	relativePath := filepath.Join(dateDir, safeFilename)
	_ = json.NewEncoder(w).Encode(UploadFileResponse{Path: relativePath})
}

// GetFileRequest represents a file read request
type GetFileRequest struct {
	Path string `json:"path"`
}

// GetFileResponse represents a file read response
type GetFileResponse struct {
	Content  string `json:"content"` // base64 encoded
	MimeType string `json:"mime_type"`
	Filename string `json:"filename"`
	Error    string `json:"error,omitempty"`
}

// GetFileHandler reads a file from the uploads/ folder
func (h *Handler) GetFileHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Query().Get("path")
	if path == "" {
		_ = json.NewEncoder(w).Encode(GetFileResponse{Error: "Path parameter required"})
		return
	}

	// Security: prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.Contains(cleanPath, "..") {
		_ = json.NewEncoder(w).Encode(GetFileResponse{Error: "Invalid path"})
		return
	}

	fullPath := filepath.Join(h.uploadsDir, cleanPath)

	// Read file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		_ = json.NewEncoder(w).Encode(GetFileResponse{Error: "File not found"})
		return
	}

	// Encode to base64
	content := base64.StdEncoding.EncodeToString(data)

	// Determine mime type from extension
	mimeType := getMimeType(filepath.Ext(fullPath))

	_ = json.NewEncoder(w).Encode(GetFileResponse{
		Content:  content,
		MimeType: mimeType,
		Filename: filepath.Base(fullPath),
	})
}

// sanitizeFilename removes unsafe characters from filename
func sanitizeFilename(filename string) string {
	// Replace unsafe characters
	unsafe := []string{"/", "\\", "..", ":", "*", "?", "\"", "<", ">", "|"}
	result := filename
	for _, char := range unsafe {
		result = strings.ReplaceAll(result, char, "_")
	}
	// Limit length
	if len(result) > 200 {
		ext := filepath.Ext(result)
		result = result[:200-len(ext)] + ext
	}
	return result
}

// getMimeType returns MIME type based on file extension
func getMimeType(ext string) string {
	mimeTypes := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".pdf":  "application/pdf",
		".txt":  "text/plain",
		".md":   "text/markdown",
		".json": "application/json",
		".xml":  "application/xml",
		".html": "text/html",
		".css":  "text/css",
		".js":   "application/javascript",
		".csv":  "text/csv",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".zip":  "application/zip",
	}
	if mime, ok := mimeTypes[strings.ToLower(ext)]; ok {
		return mime
	}
	return "application/octet-stream"
}
