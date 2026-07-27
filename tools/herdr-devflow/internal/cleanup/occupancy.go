package cleanup

import (
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// Occupant is one live agent whose working directory resolves into the target
// worktree. Occupancy answers the question cleanup actually cares about — is
// anything running in this directory right now — rather than whether a saved
// record from days ago still resolves.
type Occupant struct {
	PaneID      string
	WorkspaceID string
	Agent       string
	Status      model.AgentStatus
	Cwd         string
}

// Active reports whether this occupant is doing work that removal would
// interrupt. Idle and done agents are settled; anything unrecognised is
// treated as active, because an unknown state is not evidence of safety.
func (o Occupant) Active() bool {
	switch o.Status {
	case model.AgentIdle, model.AgentDone:
		return false
	case model.AgentMissing:
		// A pane Herdr can no longer see is not running anything.
		return false
	default:
		return true
	}
}

// Describe renders one occupant for an operator: what it is, what it is doing,
// and where, so a blocked cleanup can be acted on rather than merely obeyed.
func (o Occupant) Describe() string {
	parts := []string{}
	if o.Agent != "" {
		parts = append(parts, o.Agent)
	}
	if o.Status != "" {
		parts = append(parts, string(o.Status))
	}
	if o.PaneID != "" {
		parts = append(parts, "pane "+o.PaneID)
	}
	if o.Cwd != "" {
		parts = append(parts, "in "+o.Cwd)
	}
	return strings.Join(parts, " ")
}

// occupantsInWorktree returns the live agents whose working directory resolves
// inside worktreePath.
//
// Matching is by path rather than by saved workspace ID: a workspace closed
// and reopened, or a pane a human opened, still occupies the directory, and a
// stale saved ID is not evidence that anything is running.
func occupantsInWorktree(worktreePath string, live []herdr.AgentInfo, workspaces []herdr.WorkspaceInfo) []Occupant {
	if strings.TrimSpace(worktreePath) == "" {
		return nil
	}
	var occupants []Occupant
	for _, agent := range live {
		cwd := agentWorkingDirectory(agent, workspaces)
		if !worktree.Contains(worktreePath, cwd) {
			continue
		}
		// A pane with no agent running holds no work to interrupt.
		if agent.Agent == "" {
			continue
		}
		occupants = append(occupants, Occupant{
			PaneID:      agent.PaneID,
			WorkspaceID: agent.WorkspaceID,
			Agent:       agent.Agent,
			Status:      normalizeAgentStatus(agent.AgentStatus),
			Cwd:         cwd,
		})
	}
	return occupants
}

// agentWorkingDirectory prefers the pane's own working directory and falls
// back to its workspace's recorded checkout path.
func agentWorkingDirectory(agent herdr.AgentInfo, workspaces []herdr.WorkspaceInfo) string {
	if agent.Cwd != "" {
		return agent.Cwd
	}
	if agent.ForegroundCwd != "" {
		return agent.ForegroundCwd
	}
	for _, workspace := range workspaces {
		if workspace.WorkspaceID != agent.WorkspaceID {
			continue
		}
		if workspace.Worktree != nil && workspace.Worktree.CheckoutPath != "" {
			return workspace.Worktree.CheckoutPath
		}
		return workspace.Cwd
	}
	return ""
}

// activeOccupants filters to the occupants that block removal.
func activeOccupants(occupants []Occupant) []Occupant {
	var active []Occupant
	for _, occupant := range occupants {
		if occupant.Active() {
			active = append(active, occupant)
		}
	}
	return active
}

// describeOccupants renders the blocking set for an operator message.
func describeOccupants(occupants []Occupant) string {
	if len(occupants) == 0 {
		return ""
	}
	described := make([]string, 0, len(occupants))
	for _, occupant := range occupants {
		described = append(described, occupant.Describe())
	}
	return strconv.Itoa(len(occupants)) + " active agent(s): " + strings.Join(described, "; ")
}

// normalizeAgentStatus maps an empty or unrecognised status onto unknown, so a
// blank never reads as idle — and therefore never reads as safe.
func normalizeAgentStatus(status model.AgentStatus) model.AgentStatus {
	switch status {
	case model.AgentIdle, model.AgentWorking, model.AgentBlocked, model.AgentDone, model.AgentMissing:
		return status
	default:
		return model.AgentUnknown
	}
}
