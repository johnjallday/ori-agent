package agentstudio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// CreateAttachmentRequest represents the request to create an attachment
type CreateAttachmentRequest struct {
	Title   string              `json:"title"`
	Body    string              `json:"body"`
	Type    AttachmentType      `json:"type"`
	Color   string              `json:"color"`
	LinkURL string              `json:"link_url"`
	File    *AttachmentFileMeta `json:"file_meta"`
	X       float64             `json:"x"`
	Y       float64             `json:"y"`
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

// CreateAttachment handles POST /api/studios/:id/attachments
func (h *HTTPHandler) CreateAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

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

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	attachment := Attachment{
		ID:          uuid.New().String(),
		WorkspaceID: studioID,
		Title:       req.Title,
		Body:        req.Body,
		Type:        attType,
		Color:       req.Color,
		LinkURL:     req.LinkURL,
		File:        req.File,
		X:           req.X,
		Y:           req.Y,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := studio.AddAttachment(attachment); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Failed to add attachment: %v", err))
		return
	}

	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	// Publish event for live updates
	createdAttachment, err := studio.GetAttachment(attachment.ID)
	if err != nil {
		logger.Debug("Could not retrieve created attachment, using original", logger.Fields{"attachment_id": attachment.ID, "error": err})
		createdAttachment = &attachment
	} else if createdAttachment == nil {
		createdAttachment = &attachment
	}

	if h.eventBus != nil && createdAttachment != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentCreated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment": createdAttachment,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Attachment created successfully",
		"attachment": createdAttachment,
		"studio":     studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateAttachment handles PATCH /api/studios/:id/attachments/:attachment_id
func (h *HTTPHandler) UpdateAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	attachmentID := parts[2]

	var req struct {
		Title   *string             `json:"title,omitempty"`
		Body    *string             `json:"body,omitempty"`
		Type    *AttachmentType     `json:"type,omitempty"`
		Color   *string             `json:"color,omitempty"`
		LinkURL *string             `json:"link_url,omitempty"`
		File    *AttachmentFileMeta `json:"file_meta,omitempty"`
		X       *float64            `json:"x,omitempty"`
		Y       *float64            `json:"y,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	attachment, err := studio.GetAttachment(attachmentID)
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
		attachment.File = req.File
	}
	if req.X != nil {
		attachment.X = *req.X
	}
	if req.Y != nil {
		attachment.Y = *req.Y
	}

	if err := studio.UpdateAttachment(*attachment); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update attachment: %v", err))
		return
	}

	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	updatedAttachment, err := studio.GetAttachment(attachmentID)
	if err != nil {
		logger.Debug("Could not retrieve updated attachment, using original", logger.Fields{"attachment_id": attachmentID, "error": err})
		updatedAttachment = attachment
	} else if updatedAttachment == nil {
		updatedAttachment = attachment
	}

	if h.eventBus != nil && updatedAttachment != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentUpdated,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment": updatedAttachment,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Attachment updated successfully",
		"attachment": updatedAttachment,
		"studio":     studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteAttachment handles DELETE /api/studios/:id/attachments/:attachment_id
func (h *HTTPHandler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	attachmentID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	if err := studio.DeleteAttachment(attachmentID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	if h.eventBus != nil {
		h.eventBus.Publish(Event{
			Type:        EventAttachmentDeleted,
			WorkspaceID: studioID,
			Source:      "api",
			Data: map[string]interface{}{
				"attachment_id": attachmentID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":       "Attachment deleted successfully",
		"attachment_id": attachmentID,
		"studio":        studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
