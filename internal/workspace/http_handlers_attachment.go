package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/vaultref"
)

// CreateAttachmentRequest represents the request to create an attachment
type CreateAttachmentRequest struct {
	Title    string              `json:"title"`
	Body     string              `json:"body"`
	Type     AttachmentType      `json:"type"`
	Color    string              `json:"color"`
	LinkURL  string              `json:"link_url"`
	File     *AttachmentFileMeta `json:"file_meta"`
	VaultRef *vaultref.Reference `json:"vault_reference"`
	X        float64             `json:"x"`
	Y        float64             `json:"y"`
}

// inferAttachmentType picks a sensible type when not provided by the client.
func inferAttachmentType(req *CreateAttachmentRequest) AttachmentType {
	if req == nil {
		return AttachmentTypeDoc
	}

	normalized := AttachmentType(strings.ToLower(string(req.Type)))
	if normalized == AttachmentTypeDoc || normalized == AttachmentTypeImage || normalized == AttachmentTypeOther {
		return normalized
	}

	// Infer from file metadata
	if req.File != nil {
		mime := strings.ToLower(req.File.Mime)
		if strings.HasPrefix(mime, "image/") {
			return AttachmentTypeImage
		}
		name := strings.ToLower(req.File.Name)
		if strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") ||
			strings.HasSuffix(name, ".gif") || strings.HasSuffix(name, ".webp") || strings.HasSuffix(name, ".bmp") ||
			strings.HasSuffix(name, ".tif") || strings.HasSuffix(name, ".tiff") || strings.HasSuffix(name, ".svg") {
			return AttachmentTypeImage
		}
	}

	// Infer from link url
	if req.LinkURL != "" {
		link := strings.ToLower(req.LinkURL)
		if strings.HasSuffix(link, ".png") || strings.HasSuffix(link, ".jpg") || strings.HasSuffix(link, ".jpeg") ||
			strings.HasSuffix(link, ".gif") || strings.HasSuffix(link, ".webp") || strings.HasSuffix(link, ".bmp") ||
			strings.HasSuffix(link, ".tif") || strings.HasSuffix(link, ".tiff") || strings.HasSuffix(link, ".svg") {
			return AttachmentTypeImage
		}
	}

	return AttachmentTypeDoc
}

// CreateAttachment handles POST /api/workspaces/:id/attachments
func (h *HTTPHandler) CreateAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	var req CreateAttachmentRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Title == "" {
		orihttp.BadRequest(w, "Attachment title is required")
		return
	}

	attType := inferAttachmentType(&req)
	if attType != AttachmentTypeDoc && attType != AttachmentTypeImage && attType != AttachmentTypeOther {
		orihttp.BadRequest(w, "Attachment type must be one of: doc, image, other")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}
	fileMeta := sanitizeAttachmentFileMeta(workspaceID, req.File)
	if fileMeta != nil && fileMeta.RelativePath != "" && strings.TrimSpace(fileMeta.URL) == "" {
		fileMeta.URL = workspaceFileURL(workspaceID, fileMeta.RelativePath)
	}

	attachment := Attachment{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Title:       req.Title,
		Body:        req.Body,
		Type:        attType,
		Color:       req.Color,
		LinkURL:     req.LinkURL,
		File:        fileMeta,
		VaultRef:    vaultref.Normalize(req.VaultRef),
		X:           req.X,
		Y:           req.Y,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := workspace.AddAttachment(attachment); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Failed to add attachment: %v", err))
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	// Publish event for live updates
	createdAttachment, err := workspace.GetAttachment(attachment.ID)
	if err != nil {
		logger.Debug("Could not retrieve created attachment, using original", logger.Fields{"attachment_id": attachment.ID, "error": err})
		createdAttachment = &attachment
	} else if createdAttachment == nil {
		createdAttachment = &attachment
	}

	if createdAttachment != nil {
		hydratedAttachment := HydrateAttachment(*createdAttachment, h.store)
		createdAttachment = &hydratedAttachment
	}

	if h.eventBus != nil && createdAttachment != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentCreated,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"attachment": createdAttachment,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":    "Attachment created successfully",
		"attachment": createdAttachment,
		"workspace":  workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateAttachment handles PATCH /api/workspaces/:id/attachments/:attachment_id
func (h *HTTPHandler) UpdateAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	attachmentID := parts[2]

	var req struct {
		Title    *string             `json:"title,omitempty"`
		Body     *string             `json:"body,omitempty"`
		Type     *AttachmentType     `json:"type,omitempty"`
		Color    *string             `json:"color,omitempty"`
		LinkURL  *string             `json:"link_url,omitempty"`
		File     *AttachmentFileMeta `json:"file_meta,omitempty"`
		VaultRef *vaultref.Reference `json:"vault_reference,omitempty"`
		X        *float64            `json:"x,omitempty"`
		Y        *float64            `json:"y,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	attachment, err := workspace.GetAttachment(attachmentID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	// Apply updates
	if req.Title != nil {
		attachment.Title = *req.Title
	}
	if req.Body != nil {
		attachment.Body = *req.Body
	}
	if req.Type != nil {
		if *req.Type != AttachmentTypeDoc && *req.Type != AttachmentTypeImage && *req.Type != AttachmentTypeOther {
			orihttp.BadRequest(w, "Attachment type must be one of: doc, image, other")
			return
		}
		attachment.Type = *req.Type
	} else if attachment.Type == "" {
		// If type is missing entirely, infer from current data
		inferred := inferAttachmentType(&CreateAttachmentRequest{
			Type:    attachment.Type,
			LinkURL: attachment.LinkURL,
			File:    attachment.File,
		})
		attachment.Type = inferred
	}
	if req.Color != nil {
		attachment.Color = *req.Color
	}
	if req.LinkURL != nil {
		attachment.LinkURL = *req.LinkURL
	}
	if req.File != nil {
		fileMeta := sanitizeAttachmentFileMeta(workspaceID, req.File)
		if fileMeta != nil && fileMeta.RelativePath != "" && strings.TrimSpace(fileMeta.URL) == "" {
			fileMeta.URL = workspaceFileURL(workspaceID, fileMeta.RelativePath)
		}
		attachment.File = fileMeta
	}
	if req.VaultRef != nil {
		attachment.VaultRef = vaultref.Normalize(req.VaultRef)
	}
	if req.X != nil {
		attachment.X = *req.X
	}
	if req.Y != nil {
		attachment.Y = *req.Y
	}

	if err := workspace.UpdateAttachment(*attachment); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update attachment: %v", err))
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	updatedAttachment, err := workspace.GetAttachment(attachmentID)
	if err != nil {
		logger.Debug("Could not retrieve updated attachment, using original", logger.Fields{"attachment_id": attachmentID, "error": err})
		updatedAttachment = attachment
	} else if updatedAttachment == nil {
		updatedAttachment = attachment
	}

	if updatedAttachment != nil {
		hydratedAttachment := HydrateAttachment(*updatedAttachment, h.store)
		updatedAttachment = &hydratedAttachment
	}

	if h.eventBus != nil && updatedAttachment != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentUpdated,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"attachment": updatedAttachment,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":    "Attachment updated successfully",
		"attachment": updatedAttachment,
		"workspace":  workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RelinkAttachmentFile handles POST /api/workspaces/:id/attachments/:attachment_id/relink
// by copying a replacement file into the workspace-owned files directory.
func (h *HTTPHandler) RelinkAttachmentFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	attachmentID := parts[2]

	r.Body = http.MaxBytesReader(w, r.Body, MaxWorkspaceFileSize)
	if err := r.ParseMultipartForm(MaxWorkspaceFileSize); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			orihttp.BadRequest(w, fmt.Sprintf("File too large. Maximum size is %d MB", MaxWorkspaceFileSize/(1<<20)))
			return
		}
		orihttp.BadRequest(w, "Failed to parse upload: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		orihttp.BadRequest(w, "File is required: "+err.Error())
		return
	}
	defer func() { _ = file.Close() }()

	filename, ok := orihttp.ValidateUploadFilename(w, header.Filename)
	if !ok {
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	attachment, err := workspace.GetAttachment(attachmentID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	targetFolder, suppliedTargetFolder := workspaceFolderFormValue(r)
	if !suppliedTargetFolder {
		targetFolder = workspaceFolderFromRelativePath(extractAttachmentRelativePath(workspaceID, attachment.File))
	}

	filesPath := h.store.GetFilesPath(workspaceID)
	storedFile, err := storeWorkspaceFile(filesPath, file, filename, targetFolder)
	if err != nil {
		if strings.Contains(err.Error(), "invalid folder path") {
			orihttp.BadRequest(w, err.Error())
			return
		}
		orihttp.InternalError(w, "Failed to save replacement file: "+err.Error())
		return
	}

	oldFile := attachment.File
	attachment.File = buildWorkspaceOwnedAttachmentFileMeta(workspaceID, *storedFile, "")
	attachment.UpdatedAt = time.Now()

	if err := workspace.UpdateAttachment(*attachment); err != nil {
		removeWorkspaceOwnedAttachmentFile(h.store, workspaceID, attachment.File, "")
		orihttp.InternalError(w, fmt.Sprintf("Failed to update attachment: %v", err))
		return
	}

	if err := h.store.Save(workspace); err != nil {
		removeWorkspaceOwnedAttachmentFile(h.store, workspaceID, attachment.File, "")
		attachment.File = oldFile
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	removeWorkspaceOwnedAttachmentFile(h.store, workspaceID, oldFile, attachment.File.RelativePath)

	hydratedAttachment := HydrateAttachment(*attachment, h.store)
	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentUpdated,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"attachment": hydratedAttachment,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":    "Attachment file relinked successfully",
		"attachment": hydratedAttachment,
		"workspace":  workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteAttachment handles DELETE /api/workspaces/:id/attachments/:attachment_id
func (h *HTTPHandler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	attachmentID := parts[2]

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	if err := workspace.DeleteAttachment(attachmentID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentDeleted,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"attachment_id": attachmentID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":       "Attachment deleted successfully",
		"attachment_id": attachmentID,
		"workspace":     workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// MoveToTrash handles PATCH /api/workspaces/:id/attachments/:attachment_id/trash
// Soft-deletes an attachment by setting its DeletedAt timestamp
func (h *HTTPHandler) MoveToTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	attachmentID := parts[2]

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	attachment, err := workspace.GetAttachment(attachmentID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	// Set DeletedAt timestamp
	now := time.Now()
	attachment.DeletedAt = &now
	attachment.UpdatedAt = now

	if err := workspace.UpdateAttachment(*attachment); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to move attachment to trash: %v", err))
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentUpdated,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"attachment":    attachment,
				"action":        "moved_to_trash",
				"attachment_id": attachmentID,
			},
		})
	}

	logger.Info("Attachment moved to trash", logger.Fields{"attachment_id": attachmentID, "workspace_id": workspaceID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":       "Attachment moved to trash",
		"attachment_id": attachmentID,
		"workspace":     workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RestoreFromTrash handles PATCH /api/workspaces/:id/attachments/:attachment_id/restore
// Restores a soft-deleted attachment by clearing its DeletedAt timestamp
func (h *HTTPHandler) RestoreFromTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	attachmentID := parts[2]

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	attachment, err := workspace.GetAttachment(attachmentID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	if attachment.DeletedAt == nil {
		orihttp.BadRequest(w, "Attachment is not in trash")
		return
	}

	// Clear DeletedAt timestamp to restore
	attachment.DeletedAt = nil
	attachment.UpdatedAt = time.Now()

	if err := workspace.UpdateAttachment(*attachment); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to restore attachment: %v", err))
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentUpdated,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"attachment":    attachment,
				"action":        "restored_from_trash",
				"attachment_id": attachmentID,
			},
		})
	}

	logger.Info("Attachment restored from trash", logger.Fields{"attachment_id": attachmentID, "workspace_id": workspaceID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":       "Attachment restored from trash",
		"attachment_id": attachmentID,
		"attachment":    attachment,
		"workspace":     workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListTrash handles GET /api/workspaces/:id/trash
// Returns all soft-deleted attachments for a workspace
func (h *HTTPHandler) ListTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Filter for trashed attachments
	trashedAttachments := []Attachment{}
	for _, attachment := range workspace.Attachments {
		if attachment.DeletedAt != nil {
			trashedAttachments = append(trashedAttachments, attachment)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"attachments": trashedAttachments,
		"count":       len(trashedAttachments),
		"workspace":   workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// EmptyTrash handles DELETE /api/workspaces/:id/trash/:attachment_id
// Permanently deletes a single attachment from trash
func (h *HTTPHandler) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	attachmentID := parts[2]

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Verify the attachment exists and is in trash
	attachment, err := workspace.GetAttachment(attachmentID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	if attachment.DeletedAt == nil {
		orihttp.BadRequest(w, "Attachment is not in trash. Use the trash endpoint first.")
		return
	}

	// Permanently delete
	if err := workspace.DeleteAttachment(attachmentID); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to permanently delete attachment: %v", err))
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentDeleted,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"attachment_id": attachmentID,
				"action":        "permanently_deleted",
			},
		})
	}

	logger.Info("Attachment permanently deleted from trash", logger.Fields{"attachment_id": attachmentID, "workspace_id": workspaceID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":       "Attachment permanently deleted",
		"attachment_id": attachmentID,
		"workspace":     workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// BulkMoveToTrash handles POST /api/workspaces/:id/attachments/bulk-trash
// Moves multiple attachments to trash at once
func (h *HTTPHandler) BulkMoveToTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	var req struct {
		AttachmentIDs []string `json:"attachment_ids"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if len(req.AttachmentIDs) == 0 {
		orihttp.BadRequest(w, "attachment_ids is required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	now := time.Now()
	successCount := 0
	failedCount := 0
	var errors []string

	for _, attachmentID := range req.AttachmentIDs {
		attachment, err := workspace.GetAttachment(attachmentID)
		if err != nil {
			failedCount++
			errors = append(errors, fmt.Sprintf("%s: %v", attachmentID, err))
			continue
		}

		attachment.DeletedAt = &now
		attachment.UpdatedAt = now

		if err := workspace.UpdateAttachment(*attachment); err != nil {
			failedCount++
			errors = append(errors, fmt.Sprintf("%s: %v", attachmentID, err))
			continue
		}

		successCount++
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventWorkspaceUpdated,
			WorkspaceID: workspaceID,
			Source:      "api",
			Data: map[string]any{
				"action":        "bulk_moved_to_trash",
				"success_count": successCount,
				"failed_count":  failedCount,
			},
		})
	}

	logger.Info("Bulk move to trash completed", logger.Fields{
		"workspace_id":  workspaceID,
		"success_count": successCount,
		"failed_count":  failedCount,
	})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":       "Bulk move to trash completed",
		"success_count": successCount,
		"failed_count":  failedCount,
		"errors":        errors,
		"workspace":     workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
