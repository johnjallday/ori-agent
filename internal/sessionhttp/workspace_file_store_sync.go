package sessionhttp

import (
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
	target.Agents = append([]string(nil), source.Agents...)
	target.AgentInstances = append([]agentworkspace.AgentInstance(nil), source.AgentInstances...)
	target.SharedData = source.SharedData
	target.Messages = source.Messages
	target.Tasks = source.Tasks
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
