package sessionhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
)

const workspaceDescriptionNoteName = "Workspace Description"

func workspaceBootstrapMap(sharedData map[string]interface{}) map[string]interface{} {
	if sharedData == nil {
		return nil
	}

	raw, ok := sharedData["workspace_bootstrap"]
	if !ok || raw == nil {
		return nil
	}

	bootstrap, _ := raw.(map[string]interface{})
	return bootstrap
}

func workspaceBootstrapStringValue(sharedData map[string]interface{}, key string) string {
	bootstrap := workspaceBootstrapMap(sharedData)
	if bootstrap == nil {
		return ""
	}

	value, ok := bootstrap[key]
	if !ok || value == nil {
		return ""
	}

	return strings.TrimSpace(fmt.Sprint(value))
}

func mergeWorkspaceBootstrapForUpdate(sharedData map[string]interface{}, description string, descriptionTouched bool, input *workspaceBootstrapRequest) map[string]interface{} {
	if input != nil {
		goal := strings.TrimSpace(input.Goal)
		if descriptionTouched {
			goal = strings.TrimSpace(description)
		}
		if goal == "" {
			goal = strings.TrimSpace(description)
		}
		return normalizeWorkspaceBootstrap(&workspaceBootstrapRequest{
			Goal:         goal,
			Systems:      strings.TrimSpace(input.Systems),
			Capabilities: strings.TrimSpace(input.Capabilities),
			Context:      strings.TrimSpace(input.Context),
		})
	}

	if !descriptionTouched {
		return workspaceBootstrapMap(sharedData)
	}

	return normalizeWorkspaceBootstrap(&workspaceBootstrapRequest{
		Goal:         strings.TrimSpace(description),
		Systems:      workspaceBootstrapStringValue(sharedData, "systems"),
		Capabilities: workspaceBootstrapStringValue(sharedData, "capabilities"),
		Context:      workspaceBootstrapStringValue(sharedData, "context"),
	})
}

func markdownOptionalIntentValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "_Not specified._"
	}
	return trimmed
}

func buildWorkspaceDescriptionNoteContent(workspace *session.Workspace) string {
	if workspace == nil {
		return ""
	}

	description := strings.TrimSpace(workspace.Description)
	if description == "" {
		description = workspaceBootstrapStringValue(workspace.SharedData, "goal")
	}

	return fmt.Sprintf(
		"# Workspace Description\n\n## Description\n%s\n\n## Apps and Systems\n%s\n\n## Key Files or Context\n%s\n\n## Special Capabilities or Workflows\n%s\n",
		markdownOptionalIntentValue(description),
		markdownOptionalIntentValue(workspaceBootstrapStringValue(workspace.SharedData, "systems")),
		markdownOptionalIntentValue(workspaceBootstrapStringValue(workspace.SharedData, "context")),
		markdownOptionalIntentValue(workspaceBootstrapStringValue(workspace.SharedData, "capabilities")),
	)
}

func normalizeWorkspaceIntentNoteToken(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range strings.TrimSpace(value) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if r >= 'A' && r <= 'Z' {
				r = r + ('a' - 'A')
			}
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func isWorkspaceIntentNoteName(name string) bool {
	switch normalizeWorkspaceIntentNoteToken(name) {
	case "workspacedescription", "workspacebrief":
		return true
	default:
		return false
	}
}

func (h *Handler) syncWorkspaceDescriptionNote(ctx context.Context, workspace *session.Workspace) error {
	if h == nil || h.store == nil || workspace == nil || workspace.IsGroup() {
		return nil
	}

	notes, err := h.store.ListNotesByWorkspace(ctx, workspace.ID)
	if err != nil {
		return err
	}

	existingID := ""
	existingName := ""
	bestScore := 0
	for _, note := range notes {
		if !isWorkspaceIntentNoteName(note.Name) {
			continue
		}
		score := 1
		if strings.EqualFold(strings.TrimSpace(note.Name), workspaceDescriptionNoteName) {
			score = 2
		}
		if score > bestScore {
			bestScore = score
			existingID = note.ID
			existingName = note.Name
		}
	}

	content := buildWorkspaceDescriptionNoteContent(workspace)
	now := time.Now()
	if existingID != "" {
		existing, err := h.store.GetNote(ctx, existingID)
		if err != nil {
			return err
		}
		oldName := existingName
		existing.Name = workspaceDescriptionNoteName
		existing.Content = content
		existing.UpdatedAt = now
		if err := h.store.UpdateNote(ctx, existing); err != nil {
			return err
		}
		h.syncNoteToFileAfterRename(existing, oldName)
		return nil
	}

	note := &session.WorkspaceNote{
		ID:          uuid.New().String(),
		WorkspaceID: workspace.ID,
		Name:        workspaceDescriptionNoteName,
		Content:     content,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.store.CreateNote(ctx, note); err != nil {
		return err
	}
	h.syncNoteToFile(note)
	return nil
}
