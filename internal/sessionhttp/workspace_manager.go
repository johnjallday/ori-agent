package sessionhttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacemanager"
)

// A Blank workspace's manager is an ordinary agent profile that the workspace's
// own create operation makes for it (see internal/workspacemanager). Three
// things keep it bound to that one workspace rather than to a display name:
//
//   - its proposed name is free when proposed, so Blank never reuses an
//     existing agent by name match;
//   - creation stamps agent.WorkspaceManagerProvenance with the workspace ID,
//     which survives renaming either the agent or the workspace;
//   - its role has a stable host-owned id ("manager"), so the roster slot does
//     not move when the workspace is renamed.
const (
	guidedPurposeKey            = "guided_purpose"
	blankManagerRoleID          = workspacemanager.RoleID
	blankManagerRoleLabel       = workspacemanager.RoleLabel
	blankWorkspaceManagerPrompt = workspacemanager.Prompt
)

// legacyBlankRoleIDs are role ids an earlier Blank roster derived from the
// names of Ori's retired protected assistant. Reading an existing Blank
// workspace's roster maps them onto the manager slot so a holder recorded under
// one is not listed as unassigned. This is a read-time roster mapping only: no
// record is rewritten and no agent name is routed anywhere.
var legacyBlankRoleIDs = map[string]struct{}{
	"ask-ori":           {},
	"workspace-manager": {},
}

func isLegacyBlankRoleID(roleID string) bool {
	_, legacy := legacyBlankRoleIDs[strings.ToLower(strings.TrimSpace(roleID))]
	return legacy
}

// markBlankManagerAttachment records that name holds a Blank workspace's
// manager slot, as the role-staffed create already does. created says whether
// this request made the profile (it then counts as the workspace's own) or the
// review deliberately attached an existing agent.
func markBlankManagerAttachment(ws *session.Workspace, name string, created bool) {
	source := agentworkspace.RoleSourceAssigned
	if created {
		source = agentworkspace.RoleSourceCreated
	}
	for i := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(ws.AgentInstances[i].Name), strings.TrimSpace(name)) {
			ws.AgentInstances[i].RoleID = blankManagerRoleID
			ws.AgentInstances[i].RoleSource = source
			if strings.TrimSpace(ws.AgentInstances[i].Role) == "" {
				ws.AgentInstances[i].Role = blankManagerRoleLabel
			}
			return
		}
	}
}

// A retained purpose only describes the Blank slot. Attachments and manager
// provenance, not this value or a folder name, identify its actual holder.
func (h *Handler) storedBlankWorkspaceTeam(ws *agentworkspace.Workspace) projecttemplates.Template {
	raw, _ := ws.GetSharedData(guidedPurposeKey)
	purpose, _ := raw.(string)
	return h.blankWorkspaceTeam(ws.Name, purpose)
}

// workspaceManagerBaseName derives "<Workspace> Manager" from a workspace name.
func workspaceManagerBaseName(workspaceName string) string {
	return workspacemanager.BaseName(workspaceName)
}

// proposeWorkspaceManagerName returns the first free "<Workspace> Manager"
// name among the saved agents. An empty result means no free name was found
// and the caller must not seed a manager.
func (h *Handler) proposeWorkspaceManagerName(workspaceName string) string {
	var existing []string
	if h != nil && h.agentStore != nil {
		existing = h.agentStore.ListAgents()
	}
	return workspacemanager.Propose(workspaceName, workspacemanager.TakenIn(existing))
}
