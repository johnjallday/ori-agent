package server

import (
	"errors"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The folder agent's Toolbox pin (PRD FR9, decision 3A). During the first
// look its own runtime sees only the anchor binding's listing tools; the
// content tools join in the same step that records Set it up or Look
// further. The binding itself never changes: the pin is what narrows, and the
// runtime intersects it with the binding's current allowlist.
var (
	folderAgentListingTools = []string{
		"list_directory", "list_directory_with_sizes", "directory_tree", "search_files",
		"get_file_info", "list_allowed_directories",
	}
	folderAgentContentTools = []string{"read_file", "read_text_file", "read_media_file", "read_multiple_files"}
)

const (
	folderAgentToolboxName       = "First look"
	folderAgentToolboxProvenance = "folder_agent"
)

var errFolderAgentNoAnchorBinding = errors.New("the workspace has no anchor binding")

// pinFolderAgentToolbox pins the agent's First look Toolbox: listing tools
// only, or listing plus content tools once the owner chose Set it up or Look
// further. It is idempotent; a widened pin is never narrowed again.
func pinFolderAgentToolbox(ws *workspace.Workspace, agentName string, readContents bool) error {
	if ws == nil {
		return errors.New("no workspace")
	}
	instance, ok := ws.FindAgentInstance(agentName, "")
	if !ok || instance == nil {
		return errors.New("the folder's agent is not in this workspace")
	}
	bindingID := ""
	for _, binding := range ws.GetMCPBindings() {
		if binding.Alias == workspace.AnchorFolderMCPAlias {
			bindingID = binding.ID
			break
		}
	}
	if bindingID == "" {
		return errFolderAgentNoAnchorBinding
	}
	tools := append([]string(nil), folderAgentListingTools...)
	if readContents {
		tools = append(tools, folderAgentContentTools...)
	}
	toolboxID := "folder-agent-" + instance.ID
	refs := []workspace.ToolboxMCPRef{{BindingID: bindingID, AllowedTools: tools, Required: true}}

	current, exists := ws.GetToolbox(toolboxID)
	switch {
	case !exists:
		name := folderAgentToolboxName
		if _, taken := ws.FindToolboxByName(name); taken {
			name = folderAgentToolboxName + " · " + strings.TrimSpace(agentName)
		}
		created, err := ws.CreateToolbox(workspace.ToolboxDefinition{
			ID: toolboxID, Name: name, Status: workspace.ToolboxStatusActive,
			Description: "What " + agentName + " may use on the folder: listing only until you choose Set it up or Look further.",
			MCPBindings: refs, Provenance: folderAgentToolboxProvenance, Actor: "Ori",
		})
		if err != nil {
			return err
		}
		current = created
	case readContents && !pinReadsContents(current):
		updated, err := ws.SaveToolboxVersion(toolboxID, current.Skills, refs, folderAgentToolboxProvenance, "Ori")
		if err != nil {
			return err
		}
		current = updated
	}
	_, err := ws.SetToolboxAssignment(workspace.AgentToolboxAssignment{
		AgentInstanceID: instance.ID, ToolboxID: current.ID, ToolboxVersion: current.Version,
		Provenance: folderAgentToolboxProvenance, Actor: "Ori",
	})
	return err
}

func pinReadsContents(def *workspace.ToolboxDefinition) bool {
	for _, ref := range def.MCPBindings {
		if slices.Contains(ref.AllowedTools, folderAgentContentTools[0]) {
			return true
		}
	}
	return false
}

type folderAgentPins struct{ store workspace.Store }

func (p folderAgentPins) Pin(workspaceID, agentName string, readContents bool) error {
	return p.store.Update(workspaceID, func(ws *workspace.Workspace) error {
		return pinFolderAgentToolbox(ws, agentName, readContents)
	})
}
