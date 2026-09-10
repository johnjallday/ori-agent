package workspace

import (
	"errors"
	"strings"
)

var ErrGroupRequirementProtected = errors.New("template group requirement requires an explicit lifecycle review")

const (
	GroupRequirementStatusReadyGrouped    = "ready_grouped"
	GroupRequirementStatusReadyStandalone = "ready_standalone"
	GroupRequirementStatusUnfulfilled     = "group_requirement_unfulfilled"
	GroupRequirementStatusInvalid         = "contract_invalid"
)

// GroupRequirementLifecycleStatus is a read-only projection of one persisted
// creation contract. It never repairs topology or resolves identity by name.
type GroupRequirementLifecycleStatus struct {
	State               string   `json:"state"`
	Policy              string   `json:"policy"`
	SelectedComposition string   `json:"selected_composition"`
	HomeWorkspaceID     string   `json:"home_workspace_id,omitempty"`
	Summary             string   `json:"summary"`
	Actions             []string `json:"actions,omitempty"`
}

// HasRequiredGroupRequirement reports the narrow protection marker used by
// ordinary move/delete owners. Malformed snapshots fail closed when they still
// declare Required rather than becoming unrestricted through corruption.
func HasRequiredGroupRequirement(candidate *Workspace) bool {
	if candidate == nil {
		return false
	}
	provenance := candidate.GetTemplateProvenance()
	return provenance != nil && provenance.GroupRequirement != nil &&
		strings.EqualFold(strings.TrimSpace(provenance.GroupRequirement.Policy), "required")
}

// EvaluateGroupRequirementLifecycle compares the snapshot, ordinary parent,
// typed link, and reciprocal Home state. None of those facts substitutes for
// another and the lookup is by the snapshot's stable Home ID only.
func EvaluateGroupRequirementLifecycle(project *Workspace, lookup func(string) (*Workspace, error)) *GroupRequirementLifecycleStatus {
	if project == nil {
		return nil
	}
	provenance := project.GetTemplateProvenance()
	if provenance == nil || provenance.GroupRequirement == nil {
		return nil
	}
	snapshot := provenance.GroupRequirement
	status := &GroupRequirementLifecycleStatus{
		Policy: strings.TrimSpace(snapshot.Policy), SelectedComposition: strings.TrimSpace(snapshot.SelectedComposition),
		HomeWorkspaceID: strings.TrimSpace(snapshot.HomeWorkspaceID),
	}
	if !snapshot.StructurallyValid() {
		status.State = GroupRequirementStatusInvalid
		status.Summary = "The recorded template group contract is invalid."
		status.Actions = []string{"recreate_workspace", "reconnect_project"}
		return status
	}
	if snapshot.SelectedComposition == GroupRequirementCompositionStandalone {
		if project.GetAssistantProjectLink() != nil || project.GetAssistantProgramState() != nil {
			status.State = GroupRequirementStatusUnfulfilled
			status.Summary = "This standalone project has unexpected Assistant Program state."
			status.Actions = []string{"open_guided_setup", "retry"}
			return status
		}
		status.State = GroupRequirementStatusReadyStandalone
		status.Summary = "This project is standalone and has no Assistant Program Home membership."
		return status
	}
	link := project.GetAssistantProjectLink()
	if lookup == nil || snapshot.ProgramKey == nil || project.ParentID != snapshot.HomeWorkspaceID || link == nil ||
		link.ID != snapshot.ProjectLinkID || link.StationWorkspaceID != snapshot.HomeWorkspaceID ||
		link.Key.Normalize() != snapshot.ProgramKey.Normalize() {
		return unfulfilledGroupRequirementStatus(status)
	}
	home, err := lookup(snapshot.HomeWorkspaceID)
	if err != nil || home == nil || home.Kind != "group" || home.Status == StatusTrashed || home.Status == StatusMissing {
		return unfulfilledGroupRequirementStatus(status)
	}
	homeState := home.GetAssistantProgramState()
	if homeState == nil || homeState.Key.Normalize() != snapshot.ProgramKey.Normalize() || !containsAssistantProjectID(homeState.LinkedProjectIDs, project.ID) {
		return unfulfilledGroupRequirementStatus(status)
	}
	status.State = GroupRequirementStatusReadyGrouped
	status.Summary = "This project is connected to its recorded Assistant Program Home."
	return status
}

func unfulfilledGroupRequirementStatus(status *GroupRequirementLifecycleStatus) *GroupRequirementLifecycleStatus {
	status.State = GroupRequirementStatusUnfulfilled
	status.Summary = "The recorded template group requirement is not currently fulfilled."
	status.Actions = []string{"open_guided_setup", "reconnect_project", "recreate_workspace"}
	return status
}

func containsAssistantProjectID(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func reviewedGroupRequirementOperationOwned(candidate *Workspace, operationDigest, operationStatus string) bool {
	switch strings.TrimSpace(operationStatus) {
	case "claimed", "home_ready", "child_ready", "reconcile_required":
	default:
		return false
	}
	if candidate == nil || strings.TrimSpace(operationDigest) == "" || candidate.GetAssistantProjectLink() != nil || candidate.GetAssistantProgramState() != nil {
		return false
	}
	provenance := candidate.GetTemplateProvenance()
	return provenance != nil && provenance.GroupRequirement != nil && provenance.GroupRequirement.StructurallyValid() &&
		provenance.GroupRequirement.OperationDigest == strings.TrimSpace(operationDigest)
}

func requiredGroupRequirementSubtree(workspaces map[string]*Workspace, rootID string) bool {
	pending := map[string]bool{rootID: true}
	for changed := true; changed; {
		changed = false
		for id, candidate := range workspaces {
			if candidate != nil && pending[candidate.ParentID] && !pending[id] {
				pending[id] = true
				changed = true
			}
		}
	}
	for id := range pending {
		if HasRequiredGroupRequirement(workspaces[id]) {
			return true
		}
	}
	return false
}
