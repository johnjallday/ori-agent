package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// staffCreatedProject fills the required roles of a project a setup quest just
// created, as that quest's review listed them: the Home's missing roles get new
// agents, and the project's role gets the Home's shared agent (made on the
// first project, bound on every later one). The reviewed create is the consent.
//
// It never fails the create. Whatever it could not fill (no model for a new
// agent, a name taken in between) is still offered by the quest's own staffing
// step, which reads the same bindings.
//
// The decision, the staffing and the record are one turn of the Home's staffing
// lock, so a library setup or Open on the same Home cannot slip a second shared
// create between this project's reservation and its create.
func (b *ServerBuilder) staffCreatedProject(ctx context.Context, projectID string) {
	if b == nil || b.projectStaffing == nil || b.assistantStaffing == nil {
		return
	}
	homeID := ""
	if project, err := b.workspaceStore.Get(projectID); err == nil && project != nil {
		if link := project.GetAssistantProjectLink(); link != nil {
			homeID = link.StationWorkspaceID
		}
	}
	_ = b.projectStaffing.WithHome(homeID, func(tx *projectstaffing.Tx) error {
		fills, err := tx.FillCreated(projectID, nil,
			&projectstaffing.Grant{Source: workspace.ProjectStaffingConsentReviewedCreate}, b.systemModelAvailable())
		if err != nil || len(fills) == 0 {
			return nil
		}
		roles := make([]setupjourney.RoleFill, 0, len(fills))
		for _, fill := range fills {
			mode := setupjourney.StaffingModeCreate
			if fill.Mode == projectstaffing.ModeBind {
				mode = setupjourney.StaffingModeBind
			}
			roles = append(roles, setupjourney.RoleFill{RoleID: fill.RoleID, Mode: mode, Name: fill.Name})
		}
		if err := b.assistantStaffing.StaffRolesFromReviewedWorkspaceSetup(ctx, projectID, roles); err != nil {
			logger.Warn("A created project's required roles were not all filled",
				logger.Fields{"workspace_id": projectID, "error": err.Error()})
		}
		if err := tx.Settle(projectID); err != nil {
			logger.Warn("A created project's shared agent was not recorded",
				logger.Fields{"workspace_id": projectID, "error": err.Error()})
		}
		return nil
	})
	// The project's starter tasks were seeded before it had an entry agent. Give
	// them to the one staffing just set, or the Home counts each as waiting for
	// an assignee.
	if err := b.workspaceStore.Update(projectID, func(project *workspace.Workspace) error {
		if project.ClaimUnassignedTasksForCoordinator() == 0 {
			return errNoStarterTasksClaimed
		}
		return nil
	}); err != nil && !errors.Is(err, errNoStarterTasksClaimed) {
		logger.Warn("A created project's starter tasks were not assigned",
			logger.Fields{"workspace_id": projectID, "error": err.Error()})
	}
}

// errNoStarterTasksClaimed leaves the workspace unwritten when nothing changed.
var errNoStarterTasksClaimed = errors.New("no starter tasks to assign")
