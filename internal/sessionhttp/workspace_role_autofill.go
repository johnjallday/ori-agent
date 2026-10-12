package sessionhttp

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// How the host fills a required role the wizard left empty.
const (
	roleAutoFillCreate   = "create"
	roleAutoFillExisting = "existing"
)

// roleAutoFillNeedsModel is the one reason the host knows before the create
// that it cannot make an agent.
const roleAutoFillNeedsModel = "no model is set up for new agents. Choose one in Settings, or assign an agent you already have"

// templateRoleAutoFill is how one required role is filled when the workspace is
// created and the user chose nobody for it: a new agent made for them, or an
// agent the group already has.
type templateRoleAutoFill struct {
	RoleID    string `json:"role_id"`
	Label     string `json:"label"`
	Scope     string `json:"scope"`
	Outcome   string `json:"outcome"`
	AgentName string `json:"agent_name"`
	// Blocked is why the host cannot fill the role; it then needs the user.
	Blocked string `json:"blocked,omitempty"`
}

// planRoleAutoFills previews, for the Create Workspace review, how the
// blueprint's required roles are filled on create. It reads only. Blueprints
// without scoped assistant roles, and installs without the staffing seams,
// have none: their roles stay the user's to fill.
func (h *Handler) planRoleAutoFills(
	sourceTemplate, effectiveTemplate projecttemplates.Template,
	composition, ownerUserID string,
) []templateRoleAutoFill {
	if h == nil || h.projectStaffing == nil || h.assistantRoleStaffer == nil || h.workspaceTaskStore == nil {
		return nil
	}
	declaration := templateProgramDeclaration(effectiveTemplate)
	if declaration == nil || declaration.SchemaVersion < 2 {
		return nil
	}
	preview := projectstaffing.CreatePreview{
		Roles: declaration.Roles, DefaultPrimaryName: declaration.DefaultPrimaryName,
		Held: map[string]string{},
	}
	if key, ok := templateAssistantProgramKey(ownerUserID, effectiveTemplate); ok {
		if station, err := agentworkspace.NewAssistantProgramStore(h.workspaceTaskStore).FindStation(key); err == nil && station != nil {
			preview.Home = station
			for _, role := range declaration.Roles {
				if role.Scope != agentworkspace.AssistantRoleScopeHome {
					continue
				}
				if holder, state := h.verifiedAssistantHomeRoleHolder(&key, station, role.ID); state == templateGroupRoleHolderVerified && holder != nil {
					preview.Held[role.ID] = holder.Name
				}
			}
		}
	}
	if effectiveTemplate.AssistantProject != nil && h.groupRequirements != nil && strings.TrimSpace(ownerUserID) != "" {
		preview.Provider = h.groupRequirements.Evaluate(grouprequirements.Input{
			OwnerUserID: strings.TrimSpace(ownerUserID), OperationKind: grouprequirements.OperationCreateWorkspace,
			Template: sourceTemplate, Composition: strings.TrimSpace(composition),
		}).ProjectOwner
	}
	modelReady := h.staffingModelReady != nil && h.staffingModelReady()
	fills := h.projectStaffing.PreviewCreate(preview)
	rows := make([]templateRoleAutoFill, 0, len(fills))
	for _, fill := range fills {
		row := templateRoleAutoFill{
			RoleID: fill.RoleID, Label: fill.Label, Scope: string(fill.Scope),
			Outcome: roleAutoFillExisting, AgentName: fill.Name,
		}
		if fill.Mode == projectstaffing.ModeCreate {
			row.Outcome = roleAutoFillCreate
			if !modelReady {
				row.Blocked = roleAutoFillNeedsModel
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// autoRoleFills decides, once the created workspace is linked to its Home, how
// the required roles the user left empty are filled. The reviewed create is
// the consent for a project role's shared agent (hire once, assign many).
func (h *Handler) autoRoleFills(workspaceID string, explicit map[string]bool, grant *projectstaffing.Grant) []RoleStaffingFill {
	if h == nil || h.projectStaffing == nil || grant == nil {
		return nil
	}
	modelReady := h.staffingModelReady != nil && h.staffingModelReady()
	created, err := h.projectStaffing.FillCreated(workspaceID, explicit, grant, modelReady)
	if err != nil {
		if !errors.Is(err, projectstaffing.ErrNotShared) {
			logger.Warn("Required roles could not be decided for the created workspace",
				logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		}
		return nil
	}
	fills := make([]RoleStaffingFill, 0, len(created))
	for _, fill := range created {
		mode := roleStaffingModeCreate
		if fill.Mode == projectstaffing.ModeBind {
			mode = roleStaffingModeAssign
		}
		fills = append(fills, RoleStaffingFill{RoleID: fill.RoleID, Mode: mode, Name: fill.Name})
	}
	return fills
}
