package sessionhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceroles"
)

// GetWorkspaceRoles handles GET /api/workspaces/{workspaceID}/roles.
//
// It returns the one roster projection both surfaces render (FR55): every role
// the workspace's blueprint declares, whether each is empty or filled, who
// fills it, and which agents are in the workspace without holding a role. It
// reads state and never staffs anything.
func (h *Handler) GetWorkspaceRoles(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace id is required")
		return
	}
	if h == nil || h.workspaceTaskStore == nil {
		_ = orihttp.RespondInternalError(w, "Workspace storage is unavailable")
		return
	}
	current, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil || current == nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	roster := h.buildWorkspaceRoster(current)
	_ = orihttp.RespondSuccess(w, map[string]any{"roles": roster})
}

// buildWorkspaceRoster assembles the roster input for one workspace: where its
// declared roles come from, who currently holds them, and how to resolve an
// agent's identity.
func (h *Handler) buildWorkspaceRoster(current *workspace.Workspace) workspaceroles.Roster {
	roles, bindings, groupID := h.declaredWorkspaceRoles(current)
	return workspaceroles.Build(workspaceroles.Input{
		WorkspaceID:      current.ID,
		ViewScope:        workspaceRosterViewScope(current),
		GroupWorkspaceID: groupID,
		Roles:            roles,
		Attachments:      workspaceRosterAttachments(current),
		Bindings:         bindings,
		Lookup:           h.lookupRoleAgentIdentity,
	})
}

// declaredWorkspaceRoles resolves the roles a workspace's blueprint declares,
// plus any legacy role→agent bindings that predate role ids on attachments.
//
// An assistant-program workspace declares slots directly. An ordinary
// blueprint declares a roster of agents, which FromTemplateAgents reads as
// slots. A workspace created from neither declares no roles at all — its
// agents all list under "Also in this workspace", which is the correct answer
// rather than an error.
func (h *Handler) declaredWorkspaceRoles(current *workspace.Workspace) ([]workspaceroles.Role, map[string]string, string) {
	if station, _, err := h.assistantProgramStation(current.ID); err == nil && station != nil {
		state := station.GetAssistantProgramState()
		if state != nil && state.Declaration != nil {
			bindings := make(map[string]string, len(state.HomeBindings.Bindings))
			for _, binding := range state.HomeBindings.Bindings {
				bindings[binding.RoleID] = binding.AgentName
			}
			if link := current.GetAssistantProjectLink(); link != nil {
				for _, binding := range link.ProjectBindings.Bindings {
					bindings[binding.RoleID] = binding.AgentName
				}
			}
			return workspaceroles.FromAssistantProgram(state.Declaration), bindings, station.ID
		}
	}

	provenance := h.workspaceTemplateProvenance(current)
	if provenance == nil || strings.TrimSpace(provenance.TemplateID) == "" {
		return nil, nil, ""
	}
	// Strict Blank workspaces retain their host-owned synthetic declaration so
	// an intentionally empty Ask Ori role remains visible and fillable later.
	if strings.TrimSpace(provenance.TemplateID) == blankWorkspaceTemplateID {
		return workspaceroles.FromTemplateAgents(blankWorkspaceTemplate().Agents), nil, ""
	}
	// The ordinary roster is not snapshotted on the workspace, so it is read
	// back from the library. A blueprint that has since been removed or edited
	// simply yields fewer roles; the workspace's agents remain listed.
	tpl, err := h.resolveProjectTemplate(provenance.TemplateID, "")
	if err != nil {
		logger.Debug("Workspace roster could not resolve its blueprint",
			logger.Fields{"workspace": current.ID, "template": provenance.TemplateID, "error": err})
		return nil, nil, ""
	}
	return workspaceroles.FromTemplateAgents(tpl.Agents), nil, ""
}

// workspaceTemplateProvenance reads which blueprint a workspace came from.
//
// It must go through the canonical workspace.json record: TemplateProvenance
// has no SQLite column, so the primary-backed Get every other read uses
// reports it nil for every workspace, and the roster would show a blueprint
// workspace as declaring no roles at all. The plain record is still tried
// first so a store that cannot reach the folder (or a workspace built in
// memory by a test) keeps working.
func (h *Handler) workspaceTemplateProvenance(current *workspace.Workspace) *workspace.TemplateProvenance {
	if provenance := current.GetTemplateProvenance(); provenance != nil {
		return provenance
	}
	for _, candidate := range []workspace.Store{h.workspaceTaskStore, h.workspaceStore} {
		reader, ok := candidate.(folderWorkspaceReader)
		// A store field can hold a typed nil pointer, which satisfies the
		// interface and then panics on first use.
		if !ok || isNilPointer(reader) {
			continue
		}
		stored, err := reader.GetFolderWorkspace(current.ID)
		if err != nil || stored == nil {
			continue
		}
		if provenance := stored.GetTemplateProvenance(); provenance != nil {
			return provenance
		}
	}
	return nil
}

// workspaceRosterViewScope reports which role scope this workspace owns. Only
// a station (a workspace holding assistant-program state) owns home roles; a
// project sees them read-only (D2).
func workspaceRosterViewScope(current *workspace.Workspace) workspaceroles.Scope {
	if current.GetAssistantProgramState() != nil {
		return workspaceroles.ScopeHome
	}
	return workspaceroles.ScopeProject
}

func workspaceRosterAttachments(current *workspace.Workspace) []workspaceroles.Attachment {
	instances := current.GetAgentInstances()
	attachments := make([]workspaceroles.Attachment, 0, len(instances))
	for _, instance := range instances {
		attachments = append(attachments, workspaceroles.Attachment{
			Name:       instance.Name,
			RoleID:     instance.RoleID,
			RoleSource: instance.RoleSource,
			EntryPoint: instance.EntryPoint,
		})
	}
	return attachments
}

// lookupRoleAgentIdentity resolves a saved agent definition's identity. A name
// with no definition behind it reports missing, which is how a role whose
// agent was deleted on /agents shows as empty again (FR42).
func (h *Handler) lookupRoleAgentIdentity(name string) (workspaceroles.AgentIdentity, bool) {
	if h == nil || h.agentStore == nil {
		return workspaceroles.AgentIdentity{}, false
	}
	ag, found := h.agentStore.GetAgent(name)
	if !found || ag == nil {
		return workspaceroles.AgentIdentity{}, false
	}
	return workspaceroles.AgentIdentity{
		Name:       name,
		Role:       string(ag.Role),
		Type:       ag.Type,
		Appearance: ag.Appearance.Clone(),
	}, true
}
