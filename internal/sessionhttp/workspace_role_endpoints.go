package sessionhttp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// assistantRoleUnstaffer clears one role on an assistant-program workspace.
// Injected by the server for the same reason as the staffer: the session
// package holds no dependency on the setup journey.
type assistantRoleUnstaffer func(ctx context.Context, workspaceID, roleID string) error

// SetAssistantRoleUnstaffer supplies the per-role clear callback.
func (h *Handler) SetAssistantRoleUnstaffer(clear func(ctx context.Context, workspaceID, roleID string) error) {
	h.assistantRoleUnstaffer = clear
}

// PutWorkspaceRole handles PUT /api/workspaces/{workspaceID}/roles/{roleID}:
// fill one role, by creating an agent for it or by assigning one the user
// already has (FR53).
//
// Filling from the workspace takes effect immediately — there is no separate
// review-then-commit for the user to work through (FR36). The staffing adapter
// still runs its own Review/Commit pair underneath for an assistant-program
// blueprint, so there remains exactly one code path that binds a role (FR54).
func (h *Handler) PutWorkspaceRole(w http.ResponseWriter, r *http.Request) {
	workspaceID, roleID, ok := workspaceRolePath(w, r)
	if !ok {
		return
	}
	// Decoded straight into the shared input so a field added to the wire
	// format reaches this endpoint too. Re-listing them here is what dropped
	// the Create form's system prompt and agent type on the way through.
	var request roleStaffingInput
	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}
	request.RoleID = roleID
	staffing, err := normalizeRoleStaffing([]roleStaffingInput{request})
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	current, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil || current == nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	// A role already filled is replaced by clearing it first, so "fill" never
	// silently attaches a second agent to one slot.
	if holder := currentRoleHolder(current, roleID); holder != "" {
		_ = orihttp.RespondConflict(w, fmt.Sprintf("%q already fills this role. Clear it first.", holder))
		return
	}

	if h.workspaceIsAssistantProgram(current) {
		item := staffing[roleID]
		fills := []RoleStaffingFill{{
			RoleID: item.RoleID, Mode: item.Mode, Name: item.Name,
			Provider: item.Provider, Model: item.Model,
		}}
		if h.assistantWorkspaceRoleStaffer == nil {
			_ = orihttp.RespondInternalError(w, "Role staffing is unavailable")
			return
		}
		if err := h.assistantWorkspaceRoleStaffer(r.Context(), workspaceID, fills); err != nil {
			logger.Warn("Filling a workspace role failed",
				logger.Fields{"workspace_id": workspaceID, "role_id": roleID, "error": err})
			_ = orihttp.RespondConflict(w, "That role could not be filled; reload and try again.")
			return
		}
		h.respondWorkspaceRoster(w, workspaceID)
		return
	}

	if err := h.fillOrdinaryWorkspaceRole(current, staffing[roleID]); err != nil {
		_ = orihttp.RespondConflict(w, err.Error())
		return
	}
	h.respondWorkspaceRoster(w, workspaceID)
}

// DeleteWorkspaceRole handles DELETE /api/workspaces/{workspaceID}/roles/{roleID}.
//
// Clearing a role UNBINDS it. The agent definition is never deleted, whether it
// was created for this role or assigned from the user's own agents (FR8, FR66);
// it stays on /agents and can fill this or another role again.
func (h *Handler) DeleteWorkspaceRole(w http.ResponseWriter, r *http.Request) {
	workspaceID, roleID, ok := workspaceRolePath(w, r)
	if !ok {
		return
	}
	current, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil || current == nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if h.workspaceIsAssistantProgram(current) {
		if h.assistantRoleUnstaffer == nil {
			_ = orihttp.RespondInternalError(w, "Role staffing is unavailable")
			return
		}
		if err := h.assistantRoleUnstaffer(r.Context(), workspaceID, roleID); err != nil {
			logger.Warn("Clearing a workspace role failed",
				logger.Fields{"workspace_id": workspaceID, "role_id": roleID, "error": err})
			_ = orihttp.RespondConflict(w, "That role could not be cleared; reload and try again.")
			return
		}
		h.respondWorkspaceRoster(w, workspaceID)
		return
	}
	if err := h.clearOrdinaryWorkspaceRole(workspaceID, roleID); err != nil {
		_ = orihttp.RespondConflict(w, err.Error())
		return
	}
	h.respondWorkspaceRoster(w, workspaceID)
}

// fillOrdinaryWorkspaceRole staffs one role on an ordinary blueprint workspace.
// The agent definition is created (or, for an assign, left exactly as it is)
// and the attachment records which role it fills.
func (h *Handler) fillOrdinaryWorkspaceRole(current *agentworkspace.Workspace, requested roleStaffingInput) error {
	provenance := h.workspaceTemplateProvenance(current)
	if provenance == nil || strings.TrimSpace(provenance.TemplateID) == "" {
		return fmt.Errorf("this workspace declares no blueprint roles")
	}
	tpl, err := h.resolveProjectTemplate(provenance.TemplateID, "")
	if err != nil {
		return fmt.Errorf("this workspace's blueprint could not be read")
	}
	roleIDs := projecttemplates.AgentRoleIDs(tpl.Agents)
	index := -1
	for i, id := range roleIDs {
		if id == requested.RoleID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("this blueprint does not declare the role %q", requested.RoleID)
	}
	spec := tpl.Agents[index]

	_, exists := h.agentStore.GetAgent(requested.Name)
	created := false
	if requested.Mode == roleStaffingModeAssign {
		if !exists {
			return fmt.Errorf("the agent %q no longer exists", requested.Name)
		}
	} else {
		if exists {
			return fmt.Errorf("you already have an agent named %q; rename this one or assign the agent you have", requested.Name)
		}
		config, _ := h.templateAgentCreateConfig(roleStaffedSpec(spec, requested))
		if err := h.agentStore.CreateAgent(requested.Name, config); err != nil {
			return fmt.Errorf("agent %q could not be created", requested.Name)
		}
		created = true
	}

	source := agentworkspace.RoleSourceCreated
	if requested.Mode == roleStaffingModeAssign {
		source = agentworkspace.RoleSourceAssigned
	}
	err = h.workspaceTaskStore.Update(current.ID, func(ws *agentworkspace.Workspace) error {
		instances := ws.GetAgentInstances()
		for i := range instances {
			if strings.EqualFold(strings.TrimSpace(instances[i].Name), requested.Name) {
				instances[i].RoleID = requested.RoleID
				instances[i].RoleSource = source
				ws.AgentInstances = instances
				return nil
			}
		}
		instance := agentworkspace.AgentInstancesFromNames(requested.Name)[0]
		instance.Role = spec.Name
		instance.RoleID = requested.RoleID
		instance.RoleSource = source
		ws.AgentInstances = append(instances, instance)
		// The primary role's holder is the entry agent; any other role only
		// takes the slot when nothing holds it yet (D3).
		if index == 0 || strings.TrimSpace(ws.EntryAgentName()) == "" {
			return ws.SetEntryAgentName(requested.Name)
		}
		return nil
	})
	if err != nil {
		// Undo exactly what this request made. An assigned agent is the user's
		// own and is never rolled back.
		if created {
			_ = h.agentStore.DeleteAgent(requested.Name)
		}
		return fmt.Errorf("the role could not be filled")
	}
	return nil
}

// clearOrdinaryWorkspaceRole unbinds a role and detaches its agent. The
// definition is untouched.
func (h *Handler) clearOrdinaryWorkspaceRole(workspaceID, roleID string) error {
	cleared := false
	err := h.workspaceTaskStore.Update(workspaceID, func(ws *agentworkspace.Workspace) error {
		kept := make([]agentworkspace.AgentInstance, 0, len(ws.GetAgentInstances()))
		removedName := ""
		for _, instance := range ws.GetAgentInstances() {
			if strings.EqualFold(strings.TrimSpace(instance.RoleID), roleID) {
				removedName = instance.Name
				cleared = true
				continue
			}
			kept = append(kept, instance)
		}
		if !cleared {
			return nil
		}
		ws.AgentInstances = kept
		if strings.EqualFold(ws.EntryAgentName(), removedName) {
			next := ""
			if len(kept) > 0 {
				next = kept[0].Name
			}
			return ws.SetEntryAgentName(next)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("the role could not be cleared")
	}
	if !cleared {
		return fmt.Errorf("that role is already empty")
	}
	return nil
}

func currentRoleHolder(current *agentworkspace.Workspace, roleID string) string {
	for _, instance := range current.GetAgentInstances() {
		if strings.EqualFold(strings.TrimSpace(instance.RoleID), roleID) {
			return instance.Name
		}
	}
	return ""
}

func (h *Handler) workspaceIsAssistantProgram(current *agentworkspace.Workspace) bool {
	return current.GetAssistantProjectLink() != nil || current.GetAssistantProgramState() != nil
}

// respondWorkspaceRoster returns the roster as it now stands, so the caller
// renders the committed truth rather than its own optimistic guess.
func (h *Handler) respondWorkspaceRoster(w http.ResponseWriter, workspaceID string) {
	current, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil || current == nil {
		_ = orihttp.RespondInternalError(w, "Workspace could not be read back")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"roles": h.buildWorkspaceRoster(current)})
}

func workspaceRolePath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	roleID := strings.ToLower(strings.TrimSpace(r.PathValue("roleID")))
	if workspaceID == "" || roleID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace id and role id are required")
		return "", "", false
	}
	return workspaceID, roleID, true
}
