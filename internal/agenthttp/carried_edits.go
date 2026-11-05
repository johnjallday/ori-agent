package agenthttp

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// EditCarrier brings an edit of one of the user's agents into the workspaces
// whose own copy of it Ori keeps in step: the projects a Home's standing
// consent staffed with that one agent. A copy changed in its workspace keeps
// the change and is reported, never overwritten.
type EditCarrier interface {
	CarryEdit(agentName string) (CarriedEdit, error)
}

// CarriedEdit is where an edit went.
type CarriedEdit struct {
	// Updated are the workspaces whose copy now has the edit.
	Updated []workspace.WorkspaceRef `json:"updated"`
	// Customised are the workspaces whose copy was changed there; they keep it.
	Customised []workspace.WorkspaceRef `json:"customised"`
	// Error says some copies could not be brought up to date. The agent's own
	// edit is saved either way; a later save or the workspace itself repairs
	// the copies. Empty when every tracked copy was handled.
	Error string `json:"error,omitempty"`
}

// CopyStepReader names the workspaces whose copy of an agent Ori keeps in step
// and is still unchanged there. Such a copy is the agent itself, so the detail
// page does not list it as a customisation.
type CopyStepReader interface {
	InStep(agentName string) map[string]bool
}

// SetCopyStepReader wires the reader for the agent detail response.
func (h *DashboardHandler) SetCopyStepReader(reader CopyStepReader) {
	h.copySteps = reader
}

// withoutCopiesInStep drops the in-step copies from an origin's customisations.
func (h *DashboardHandler) withoutCopiesInStep(agentName string, origin *store.AgentOrigin) *store.AgentOrigin {
	if h.copySteps == nil || origin == nil || len(origin.CustomisedIn) == 0 {
		return origin
	}
	inStep := h.copySteps.InStep(agentName)
	if len(inStep) == 0 {
		return origin
	}
	filtered := *origin
	filtered.CustomisedIn = nil
	for _, ref := range origin.CustomisedIn {
		if !inStep[ref.ID] {
			filtered.CustomisedIn = append(filtered.CustomisedIn, ref)
		}
	}
	return &filtered
}

// SetEditCarrier wires the carrier. Unwired, an edit changes only the user's
// agent, as before.
func (h *Handler) SetEditCarrier(carrier EditCarrier) {
	h.editCarrier = carrier
}

// carryEdit runs the carrier after a saved edit. The edit itself is already
// saved, so a carrier failure never undoes it — but it is reported, even when
// no copy was updated, so the editor can say the copies may be behind.
func (h *Handler) carryEdit(agentName string) *CarriedEdit {
	if h.editCarrier == nil {
		return nil
	}
	carried, err := h.editCarrier.CarryEdit(agentName)
	if err != nil {
		logger.Warn("Agent edit could not reach every workspace copy", logger.Fields{"agent": agentName, "error": err.Error()})
		carried.Error = "Some workspace copies of this agent could not be updated."
	}
	if len(carried.Updated) == 0 && len(carried.Customised) == 0 && carried.Error == "" {
		return nil
	}
	if carried.Updated == nil {
		carried.Updated = []workspace.WorkspaceRef{}
	}
	if carried.Customised == nil {
		carried.Customised = []workspace.WorkspaceRef{}
	}
	return &carried
}
