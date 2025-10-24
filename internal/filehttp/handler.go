package filehttp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/fileparser"
)

type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Decode base64 content
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		json.NewEncoder(w).Encode(ParseFileResponse{
			Error: "Failed to decode file content: " + err.Error(),
		})
		return
	}

	// Validate file size
	if err := fileparser.ValidateFileSize(int64(len(data))); err != nil {
		json.NewEncoder(w).Encode(ParseFileResponse{
			Error: err.Error(),
		})
		return
	}

	// Parse file
	text, err := fileparser.ParseFile(req.Filename, data)
	if err != nil {
		json.NewEncoder(w).Encode(ParseFileResponse{
			Error: "Failed to parse file: " + err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(ParseFileResponse{
		Text: text,
	})
}
