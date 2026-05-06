package agenthttp

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

const (
	// AvatarDir is the directory where agent avatars are stored
	AvatarDir = "agent_avatars"
	// MaxAvatarSize is the maximum file size for avatar uploads (5MB)
	MaxAvatarSize = 5 << 20 // 5 MB
)

// Allowed image MIME types for avatar uploads
var allowedImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// AvatarHandler handles avatar upload and removal operations
type AvatarHandler struct {
	State store.Store
}

// NewAvatarHandler creates a new AvatarHandler
func NewAvatarHandler(state store.Store) *AvatarHandler {
	return &AvatarHandler{
		State: state,
	}
}

// ServeHTTP handles avatar-related requests
func (h *AvatarHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract agent name from path: /api/agents/{name}/avatar
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/api/agents/"), "/")
	if len(parts) < 2 || parts[1] != "avatar" {
		orihttp.BadRequest(w, "Invalid avatar path")
		return
	}
	agentName := parts[0]

	if agentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.uploadAvatar(w, r, agentName)
	case http.MethodDelete:
		h.removeAvatar(w, agentName)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// uploadAvatar handles POST /api/agents/{name}/avatar
func (h *AvatarHandler) uploadAvatar(w http.ResponseWriter, r *http.Request, agentName string) {
	// Verify agent exists
	agent, ok := h.State.GetAgent(agentName)
	if !ok || agent == nil {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	// Parse multipart form with max size
	if err := r.ParseMultipartForm(MaxAvatarSize); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("File too large or invalid form: %v", err))
		return
	}

	// Get the uploaded file
	file, header, err := r.FormFile("avatar")
	if err != nil {
		orihttp.BadRequest(w, "No avatar file provided")
		return
	}
	defer func() { _ = file.Close() }()

	// Validate file size
	if header.Size > MaxAvatarSize {
		orihttp.BadRequest(w, "File too large (max 5MB)")
		return
	}

	// Read the first 512 bytes to detect content type
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to read file", err)
		return
	}

	// Detect content type
	contentType := http.DetectContentType(buffer[:n])
	ext, ok := allowedImageTypes[contentType]
	if !ok {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid image type: %s. Allowed: PNG, JPG, GIF, WebP", contentType))
		return
	}

	// Reset file reader to beginning
	if _, err := file.Seek(0, 0); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to process file", err)
		return
	}

	// Create avatars directory if it doesn't exist
	if err := os.MkdirAll(AvatarDir, 0755); err != nil {
		logger.Error("Failed to create avatar directory", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to create avatar directory", err)
		return
	}

	// Remove any existing avatar for this agent
	h.removeExistingAvatar(agentName)

	// Sanitize agent name for filename (replace spaces with underscores)
	safeAgentName := strings.ReplaceAll(agentName, " ", "_")
	filename := fmt.Sprintf("%s%s", safeAgentName, ext)
	avatarPath := filepath.Join(AvatarDir, filename)

	// Create the destination file
	dst, err := os.Create(avatarPath)
	if err != nil {
		logger.Error("Failed to create avatar file", logger.Fields{"error": err, "path": avatarPath})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save avatar", err)
		return
	}
	defer func() { _ = dst.Close() }()

	// Copy the uploaded file to destination
	if _, err := io.Copy(dst, file); err != nil {
		logger.Error("Failed to write avatar file", logger.Fields{"error": err, "path": avatarPath})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save avatar", err)
		return
	}

	// Update agent metadata with avatar path
	if agent.Metadata == nil {
		agent.Metadata = &types.AgentMetadata{}
	}
	agent.Metadata.AvatarImage = filename

	if err := h.State.SetAgent(agentName, agent); err != nil {
		logger.Error("Failed to update agent metadata", logger.Fields{"error": err, "agent": agentName})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
		return
	}

	logger.Info("Avatar uploaded successfully", logger.Fields{"agent": agentName, "filename": filename})

	orihttp.WriteJSON(w, map[string]any{
		"success":    true,
		"avatar_url": "/avatars/" + filename,
		"message":    "Avatar uploaded successfully",
	})
}

// removeAvatar handles DELETE /api/agents/{name}/avatar
func (h *AvatarHandler) removeAvatar(w http.ResponseWriter, agentName string) {
	// Verify agent exists
	agent, ok := h.State.GetAgent(agentName)
	if !ok || agent == nil {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	// Remove existing avatar file
	h.removeExistingAvatar(agentName)

	// Update agent metadata
	if agent.Metadata != nil {
		agent.Metadata.AvatarImage = ""
		if err := h.State.SetAgent(agentName, agent); err != nil {
			logger.Error("Failed to update agent metadata", logger.Fields{"error": err, "agent": agentName})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
			return
		}
	}

	logger.Info("Avatar removed successfully", logger.Fields{"agent": agentName})

	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Avatar removed",
	})
}

// removeExistingAvatar removes any existing avatar file for the given agent
func (h *AvatarHandler) removeExistingAvatar(agentName string) {
	safeAgentName := strings.ReplaceAll(agentName, " ", "_")

	// Try to remove files with any supported extension
	for _, ext := range allowedImageTypes {
		filename := fmt.Sprintf("%s%s", safeAgentName, ext)
		avatarPath := filepath.Join(AvatarDir, filename)
		if err := os.Remove(avatarPath); err == nil {
			logger.Debug("Removed existing avatar", logger.Fields{"path": avatarPath})
		}
	}
}
