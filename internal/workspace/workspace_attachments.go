package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AddAttachment adds an attachment node to the workspace
func (w *Workspace) AddAttachment(att Attachment) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if att.Title == "" {
		return fmt.Errorf("attachment title is required")
	}
	if att.Type != AttachmentTypeDoc && att.Type != AttachmentTypeImage && att.Type != AttachmentTypeOther {
		return fmt.Errorf("invalid attachment type %s", att.Type)
	}

	if att.ID == "" {
		att.ID = uuid.New().String()
	}
	now := time.Now()
	if att.CreatedAt.IsZero() {
		att.CreatedAt = now
	}
	att.UpdatedAt = now
	att.WorkspaceID = w.ID

	w.Attachments = append(w.Attachments, att)
	w.UpdatedAt = now
	return nil
}

// UpdateAttachment updates an existing attachment in the workspace
func (w *Workspace) UpdateAttachment(att Attachment) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.Attachments {
		if w.Attachments[i].ID == att.ID {
			att.UpdatedAt = time.Now()
			att.WorkspaceID = w.ID
			w.Attachments[i] = att
			w.UpdatedAt = att.UpdatedAt
			return nil
		}
	}

	return fmt.Errorf("attachment %s not found in workspace", att.ID)
}

// DeleteAttachment removes an attachment from the workspace
func (w *Workspace) DeleteAttachment(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.Attachments {
		if w.Attachments[i].ID == id {
			w.Attachments = append(w.Attachments[:i], w.Attachments[i+1:]...)
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("attachment %s not found in workspace", id)
}

// GetAttachment retrieves an attachment by ID
func (w *Workspace) GetAttachment(id string) (*Attachment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for i := range w.Attachments {
		if w.Attachments[i].ID == id {
			attCopy := w.Attachments[i]
			return &attCopy, nil
		}
	}

	return nil, fmt.Errorf("attachment %s not found in workspace", id)
}
