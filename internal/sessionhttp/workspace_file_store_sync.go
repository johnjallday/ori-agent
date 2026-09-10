package sessionhttp

import (
	"encoding/json"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// syncWorkspacePortableStateToFileStore updates the existing workspace.json with
// the latest portable workspace state from SQLite without changing the folder
// location on disk. Structural operations like rename/move still use dedicated
// handlers.
func (h *Handler) syncWorkspacePortableStateToFileStore(workspace *session.Workspace) error {
	if h == nil || h.workspaceStore == nil || workspace == nil {
		return nil
	}

	existing, err := h.workspaceStore.Get(workspace.ID)
	if err != nil || existing == nil {
		// Some workspaces are DB-only; skip file sync when no tracked workspace
		// folder exists rather than creating a new folder unexpectedly.
		return nil
	}

	hydrated := h.hydrateWorkspaceMetadataFromFileStore(workspace)
	portablySynced, err := buildFileStoreWorkspace(hydrated)
	if err != nil {
		return err
	}

	mergePortableWorkspaceState(existing, portablySynced)
	// An absent field is a partial/legacy row; an explicit empty envelope is
	// authoritative (including a reviewed disconnect). Never resurrect a link.
	if len(hydrated.AssistantProgramJSON) > 0 {
		existing.AssistantProgramState = agentworkspace.CloneAssistantProgramState(portablySynced.AssistantProgramState)
		existing.AssistantProjectLink = agentworkspace.CloneAssistantProjectLink(portablySynced.AssistantProjectLink)
	}
	return h.workspaceStore.Save(existing)
}

// The session row wraps the two portable fields in one JSON column. Keep this
// decode strict: treating malformed topology as empty would permit deletion.
type workspaceAssistantState struct {
	State *agentworkspace.AssistantProgramState `json:"state,omitempty"`
	Link  *agentworkspace.AssistantProjectLink  `json:"link,omitempty"`
}

func decodeWorkspaceAssistantState(raw json.RawMessage) (workspaceAssistantState, error) {
	var state workspaceAssistantState
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &state); err != nil {
			return state, fmt.Errorf("failed to decode workspace assistant state: %w", err)
		}
	}
	return state, nil
}

func (h *Handler) syncWorkspaceTagsToFileStore(workspace *session.Workspace) error {
	if h == nil || h.workspaceStore == nil || workspace == nil {
		return nil
	}

	existing, err := h.workspaceStore.Get(workspace.ID)
	if err != nil || existing == nil {
		return nil
	}
	existing.Tags = append([]string(nil), workspace.Tags...)
	existing.UpdatedAt = workspace.UpdatedAt
	return h.workspaceStore.Save(existing)
}

func mergePortableWorkspaceState(target, source *agentworkspace.Workspace) {
	if target == nil || source == nil {
		return
	}

	target.Name = source.Name
	target.Kind = source.Kind
	target.Description = source.Description
	target.ProjectPath = source.ProjectPath
	target.Tags = append([]string(nil), source.Tags...)
	target.AgentInstances = append([]agentworkspace.AgentInstance(nil), source.AgentInstances...)
	target.SharedData = source.SharedData
	target.Messages = source.Messages
	// Tasks: nil means the session row carried no task data at all (its
	// TasksJSON was empty), not "zero tasks" — clobbering the folder store's
	// tasks with nil would erase tasks that live only on disk (e.g. seeded
	// through the folder store when no SyncStore is wired). A known-empty
	// task list decodes to a non-nil empty slice and still syncs through.
	if source.Tasks != nil {
		target.Tasks = source.Tasks
	}
	// Capability installs follow the Tasks rule above for the same reason: a nil
	// collection means the session row carried no capability data, not "this
	// workspace has no capabilities". Assigning it unconditionally would let a
	// partial session record uninstall File Janitor from workspace.json on an
	// unrelated sync (PRD FR-144).
	if source.InstalledCapabilities != nil {
		target.InstalledCapabilities = source.InstalledCapabilities
	}
	target.Attachments = source.Attachments
	target.ScheduledTasks = source.ScheduledTasks
	target.StoreNodes = source.StoreNodes
	target.DirectoryReferences = source.DirectoryReferences
	target.MCPBindings = source.MCPBindings
	target.AgentMCPAccess = source.AgentMCPAccess
	target.SkillBindings = source.SkillBindings
	target.AgentSkillAccess = source.AgentSkillAccess
	target.Workflows = source.Workflows
	target.Layout = source.Layout
	target.Status = source.Status
	target.CreatedAt = source.CreatedAt
	target.UpdatedAt = source.UpdatedAt
}
