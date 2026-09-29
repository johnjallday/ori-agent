package sessionhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
)

// The handler performs the marker edits a reviewed assistant-recovery fix
// needs. Each edit changes only a Personal Assistant ownership marker: never an
// agent's prompt, model or tools, or a workspace's content.
var _ personalassistant.RecoveryRecordWriter = (*Handler)(nil)

// SetPersonalHQMarker names assistantID in the workspace's Personal HQ marker.
// An existing marker keeps its other fields; a missing or unreadable one is
// written fresh, as HQ creation writes it.
func (h *Handler) SetPersonalHQMarker(ctx context.Context, workspaceID, assistantID, requestID string) error {
	assistantID = strings.TrimSpace(assistantID)
	requestID = strings.TrimSpace(requestID)
	if assistantID == "" || requestID == "" {
		return fmt.Errorf("personal hq marker needs an assistant and a request")
	}
	return h.editPersonalHQMarker(ctx, workspaceID, func(ws *session.Workspace) {
		existing, ok := ws.SharedData[personalAssistantSupportSharedDataKey].(map[string]any)
		if !ok || existing == nil {
			markPersonalAssistantPresentation(ws, personalhq.AssistantCreationOptions{
				AssistantID: assistantID, RequestID: requestID,
			})
			return
		}
		next := make(map[string]any, len(existing)+2)
		for key, value := range existing {
			next[key] = value
		}
		next["assistant_id"] = assistantID
		next["request_id"] = requestID
		ws.SharedData[personalAssistantSupportSharedDataKey] = next
	})
}

// ClearPersonalHQMarker removes the Personal HQ marker. The workspace stays an
// ordinary workspace with everything in it.
func (h *Handler) ClearPersonalHQMarker(ctx context.Context, workspaceID string) error {
	return h.editPersonalHQMarker(ctx, workspaceID, func(ws *session.Workspace) {
		delete(ws.SharedData, personalAssistantSupportSharedDataKey)
	})
}

// editPersonalHQMarker applies edit to the database row, then writes the
// resulting marker to both stores. The folder is written first: workspace.json
// is the copy a fresh database is rebuilt from, so a marker fixed only in the
// database would come back broken.
func (h *Handler) editPersonalHQMarker(ctx context.Context, workspaceID string, edit func(*session.Workspace)) error {
	if h == nil || h.store == nil {
		return fmt.Errorf("workspace store is unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	ws, err := h.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	edit(ws)
	marker, present := ws.SharedData[personalAssistantSupportSharedDataKey]

	if h.workspaceStore != nil {
		folder, err := h.workspaceStore.Get(workspaceID)
		if err != nil {
			return err
		}
		if folder.SharedData == nil {
			folder.SharedData = map[string]any{}
		}
		if present {
			folder.SharedData[personalAssistantSupportSharedDataKey] = marker
		} else {
			delete(folder.SharedData, personalAssistantSupportSharedDataKey)
		}
		folder.UpdatedAt = time.Now()
		if err := h.workspaceStore.Save(folder); err != nil {
			return err
		}
	}
	return h.store.UpdateWorkspace(ctx, ws)
}

// SetProfileMarkers replaces any assistant markers on the profile with exactly
// this pair, keeping every other tag.
func (h *Handler) SetProfileMarkers(name, assistantID, hireRequestID string) error {
	return h.editProfileMarkers(name, func(tags []string) ([]string, error) {
		return personalassistant.EnsureProfileMarkers(
			personalassistant.WithoutProfileMarkers(tags), assistantID, hireRequestID,
		)
	})
}

// ClearProfileMarkers removes the assistant markers. The agent stays an
// ordinary agent with everything else unchanged.
func (h *Handler) ClearProfileMarkers(name string) error {
	return h.editProfileMarkers(name, func(tags []string) ([]string, error) {
		return personalassistant.WithoutProfileMarkers(tags), nil
	})
}

func (h *Handler) editProfileMarkers(name string, edit func([]string) ([]string, error)) error {
	if h == nil || h.agentStore == nil {
		return fmt.Errorf("agent store is unavailable")
	}
	name = strings.TrimSpace(name)
	return h.agentStore.UpdateAgent(name, func(record *agent.Agent) error {
		if record == nil {
			return fmt.Errorf("agent %q disappeared before its markers could be changed", name)
		}
		if record.Metadata == nil {
			record.Metadata = &types.AgentMetadata{}
		}
		tags, err := edit(record.Metadata.Tags)
		if err != nil {
			return err
		}
		record.Metadata.Tags = tags
		return nil
	})
}
