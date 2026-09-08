package sessionhttp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// seedRoleStaffedAgents fills exactly the roles the user chose on an ordinary
// blueprint, and nothing else. A role left empty creates no agent and attaches
// nothing — the whole point of the vacancy model.
//
// It is the counterpart of seedTemplateAgents, which creates the blueprint's
// entire roster. Both attach agents to the workspace record before it is
// persisted, so creation stays atomic.
//
// Rollback discipline (FR51): only a CREATE records its name in OwnedNames. An
// assigned agent is one the user already had, and deleting it to unwind a
// failed create is the single unrecoverable failure this path could cause.
func (h *Handler) seedRoleStaffedAgents(
	ws *session.Workspace,
	tpl projecttemplates.Template,
	staffing map[string]roleStaffingInput,
) (seedAgentsResult, error) {
	var result seedAgentsResult
	if h == nil || h.agentStore == nil || ws == nil {
		return result, fmt.Errorf("agent storage is unavailable")
	}
	if len(staffing) == 0 {
		return result, nil
	}

	roleIDs := projecttemplates.AgentRoleIDs(tpl.Agents)
	specByRole := make(map[string]projecttemplates.AgentSpec, len(tpl.Agents))
	primaryRole := ""
	for index, spec := range tpl.Agents {
		specByRole[roleIDs[index]] = spec
		if index == 0 {
			primaryRole = roleIDs[index]
		}
	}
	// A role id the blueprint no longer declares means the template changed
	// between the wizard drawing the roster and this request. Refuse rather
	// than create an agent bound to a slot that does not exist.
	for roleID := range staffing {
		if _, declared := specByRole[roleID]; !declared {
			return result, fmt.Errorf("this blueprint no longer declares the role %q; reopen the blueprint and choose again", roleID)
		}
	}

	// Declaration order, so the entry agent resolves the same way it always
	// has and the created agents land in a predictable order.
	for index, spec := range tpl.Agents {
		roleID := roleIDs[index]
		requested, filled := staffing[roleID]
		if !filled {
			continue
		}
		_, exists := h.agentStore.GetAgent(requested.Name)
		switch requested.Mode {
		case roleStaffingModeAssign:
			if !exists {
				return result, fmt.Errorf("the agent %q assigned to %q no longer exists", requested.Name, spec.Name)
			}
			result.ReuseNotices = append(result.ReuseNotices, fmt.Sprintf(
				"Assigned your saved agent %q to %s — its own prompt, model, and tools are used.",
				requested.Name, spec.Name))
		default:
			if exists {
				return result, fmt.Errorf("you already have an agent named %q; rename this one or assign the agent you have", requested.Name)
			}
			config, _ := h.templateAgentCreateConfig(roleStaffedSpec(spec, requested))
			if err := h.agentStore.CreateAgent(requested.Name, config); err != nil {
				return result, fmt.Errorf("agent %q could not be created: %w", requested.Name, err)
			}
			// Recorded immediately after the create succeeds, so a later
			// failure undoes exactly this set and nothing the user owned.
			result.OwnedNames = append(result.OwnedNames, requested.Name)
			if !spec.Tools.IsEmpty() {
				result.Created = append(result.Created, createdAgent{Name: requested.Name, Tools: spec.Tools})
			}
		}
		attachRoleStaffedAgent(ws, requested, roleID)
	}

	// Entry agent resolves primary-first, then the first filled role in
	// declaration order, so a workspace with agents in it is never dead just
	// because its primary slot is empty (D3).
	if entry := roleStaffedEntryAgent(tpl, roleIDs, staffing, primaryRole); entry != "" {
		setWorkspaceEntryAgent(ws, entry)
		result.EntrySet = true
	}
	return result, nil
}

// roleStaffedSpec applies the user's per-role choices to the blueprint's spec.
// The role's prompt, tools, and type come from the blueprint; only the name and
// the optional model selection are the user's.
func roleStaffedSpec(spec projecttemplates.AgentSpec, requested roleStaffingInput) projecttemplates.AgentSpec {
	spec.Name = requested.Name
	if requested.Provider != "" || requested.Model != "" {
		spec.Provider = requested.Provider
		spec.Model = requested.Model
	}
	return spec
}

func attachRoleStaffedAgent(ws *session.Workspace, requested roleStaffingInput, roleID string) {
	attachWorkspaceSpecialist(ws, requested.Name)
	source := agentworkspace.RoleSourceCreated
	if requested.Mode == roleStaffingModeAssign {
		source = agentworkspace.RoleSourceAssigned
	}
	// The binding lives on the attachment, so the workspace roster can report
	// which slot each agent holds without re-deriving it from names.
	for i := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(ws.AgentInstances[i].Name), requested.Name) {
			ws.AgentInstances[i].RoleID = roleID
			ws.AgentInstances[i].RoleSource = source
			break
		}
	}
}

func roleStaffedEntryAgent(
	tpl projecttemplates.Template,
	roleIDs []string,
	staffing map[string]roleStaffingInput,
	primaryRole string,
) string {
	if primary, filled := staffing[primaryRole]; filled {
		return primary.Name
	}
	for index := range tpl.Agents {
		if requested, filled := staffing[roleIDs[index]]; filled {
			return requested.Name
		}
	}
	return ""
}

// staffAssistantRoles commits the roles the user filled on an assistant-program
// blueprint, through the staffing adapter's ordinary Review/Commit pair.
//
// It returns a WARNING rather than failing the request: the workspace exists by
// now, and tearing it down because one role could not be staffed would be worse
// than handing the user a workspace whose roster still shows that role empty —
// which is exactly the state the roster is built to explain and to fix.
func (h *Handler) staffAssistantRoles(ctx context.Context, workspaceID string, items []roleStaffingInput) string {
	if h == nil || h.assistantRoleStaffer == nil || len(items) == 0 {
		return ""
	}
	staffing, err := normalizeRoleStaffing(items)
	if err != nil {
		return err.Error()
	}
	fills := make([]RoleStaffingFill, 0, len(staffing))
	for _, item := range staffing {
		fills = append(fills, RoleStaffingFill{
			RoleID: item.RoleID, Mode: item.Mode, Name: item.Name,
			Provider: item.Provider, Model: item.Model,
		})
	}
	// Deterministic order so a failure is reproducible and the created agents
	// land in a stable sequence.
	sort.Slice(fills, func(i, j int) bool { return fills[i].RoleID < fills[j].RoleID })

	if err := h.assistantRoleStaffer(ctx, workspaceID, fills); err != nil {
		logger.Warn("Assistant role staffing failed after workspace creation",
			logger.Fields{"workspace_id": workspaceID, "error": err})
		return "some roles could not be staffed; fill them from the workspace roster"
	}
	return ""
}

// respondRoleStaffingError rolls back what this request created and reports the
// failure. Nothing the user already owned is touched.
func (h *Handler) respondRoleStaffingError(seed seedAgentsResult, err error) []string {
	cleanupErrors := h.rollbackSeededAgents(seed)
	logger.Warn("Role staffing failed during workspace creation",
		logger.Fields{"error": err, "cleanup_errors": cleanupErrors})
	return cleanupErrors
}
