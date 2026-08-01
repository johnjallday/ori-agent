package workspacecapability

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Removal error codes, distinct from the install codes so a caller can tell a
// refusal apart from a partial teardown.
const (
	CodeRemovalIncomplete = "removal_incomplete"
	CodeRemovalFailed     = "removal_failed"
)

// RemovalDescriber is an optional Runtime capability that supplies the
// human-facing facts a removal confirmation needs — above all, which folder is
// about to lose access.
//
// A confirmation that cannot name the folder is one the user cannot evaluate,
// so a runtime that does not implement this reports no folder rather than
// letting the dialog invent one (FR-25).
type RemovalDescriber interface {
	DescribeCapabilityRemoval(workspaceID string) (RemovalFacts, error)
}

// RemovalFacts is what a runtime knows about its own teardown.
type RemovalFacts struct {
	// ManagedFolder is a display name, never an absolute path.
	ManagedFolder string `json:"managed_folder,omitempty"`
	// Automation names what stops, in the user's words ("folder watching",
	// "the daily catch-up scan").
	Automation []string `json:"automation,omitempty"`
	// RetainedAudit describes what survives removal for the record.
	RetainedAudit []string `json:"retained_audit,omitempty"`
}

// RemovalSummary is the dry run. It is computed without mutating anything, so
// the confirmation the user reads is derived from the same resolution the
// removal will perform rather than from a guess about it (FR-24, FR-25).
type RemovalSummary struct {
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name"`
	Installed    bool   `json:"installed"`

	ManagedFolder   string   `json:"managed_folder,omitempty"`
	StopsAutomation []string `json:"stops_automation,omitempty"`
	RetainedAudit   []string `json:"retained_audit,omitempty"`

	// Releases are resources this capability owns exclusively and will give up.
	Releases []workspace.CapabilityResource `json:"releases,omitempty"`
	// Shared are resources it will merely stop being associated with, because
	// something else owns them too (FR-27).
	Shared []workspace.CapabilityResource `json:"shared,omitempty"`

	// Companion, when set, is an agent removal would offer to remove — a
	// SEPARATE choice the user makes, never a side effect.
	Companion *CompanionRemoval `json:"companion,omitempty"`

	// MovesFiles is always false and is stated rather than implied: the single
	// most important thing a user needs to know before uninstalling something
	// that has been moving their files is that this does not move any (FR-28).
	MovesFiles bool `json:"moves_files"`
}

// CompanionRemoval describes a companion agent's fate.
type CompanionRemoval struct {
	AgentInstanceID string `json:"agent_instance_id"`
	// Removable is false for an agent File Janitor adopted rather than created.
	// Deleting it would take away something that existed before the capability
	// and will outlive it.
	Removable bool   `json:"removable"`
	Reason    string `json:"reason,omitempty"`
}

// RemoveOptions carries the separate decisions a removal can involve.
type RemoveOptions struct {
	// RemoveCompanion removes the capability-owned companion agent. It defaults
	// to false: uninstalling a capability is not consent to delete an agent
	// (FR-27).
	RemoveCompanion bool
}

// RemovalResult reports what actually happened.
type RemovalResult struct {
	Removed          bool                           `json:"removed"`
	AlreadyRemoved   bool                           `json:"already_removed"`
	Released         []workspace.CapabilityResource `json:"released,omitempty"`
	KeptShared       []workspace.CapabilityResource `json:"kept_shared,omitempty"`
	CompanionRemoved bool                           `json:"companion_removed"`
}

// RemovalPlan computes the summary without changing anything.
func (s *Service) RemovalPlan(workspaceID, capabilityID string) (RemovalSummary, error) {
	if s == nil || s.registry == nil {
		return RemovalSummary{}, &Error{Code: CodeCapabilityUnavailable, Message: "Workspace capabilities are not available."}
	}
	ws, err := s.loadWorkspace(workspaceID)
	if err != nil {
		return RemovalSummary{}, err
	}
	resolved, ok := s.resolveInstalled(ws, capabilityID)
	if !ok {
		return RemovalSummary{
			CapabilityID: capabilityID,
			Installed:    false,
			MovesFiles:   false,
		}, nil
	}

	summary := RemovalSummary{
		CapabilityID: resolved.Record.ID,
		DisplayName:  resolved.Definition.Display.Name,
		Installed:    true,
		MovesFiles:   false,
	}
	if summary.DisplayName == "" {
		summary.DisplayName = resolved.Record.ID
	}

	// Split what is given up from what is merely disassociated. Ownership is
	// read from the record's own metadata, never inferred from a name.
	for _, resource := range resolved.Record.OwnedResources {
		if resource.Kind == workspace.ResourceCompanionAgent {
			summary.Companion = &CompanionRemoval{
				AgentInstanceID: resource.ID,
				Removable:       !resource.Shared,
			}
			if resource.Shared {
				summary.Companion.Reason = "This agent existed before File Janitor was installed, so removing the capability leaves it alone."
			}
			continue
		}
		if resource.Shared {
			summary.Shared = append(summary.Shared, resource)
			continue
		}
		summary.Releases = append(summary.Releases, resource)
	}

	// Runtime-supplied facts: which folder, what stops, what is kept.
	if runtime, bound := s.registry.Runtime(resolved.Record.ID); bound {
		if describer, canDescribe := runtime.(RemovalDescriber); canDescribe {
			facts, factErr := describer.DescribeCapabilityRemoval(workspaceID)
			if factErr == nil {
				summary.ManagedFolder = facts.ManagedFolder
				summary.StopsAutomation = facts.Automation
				summary.RetainedAudit = facts.RetainedAudit
			}
		}
	}
	return summary, nil
}

// Remove uninstalls a capability from a workspace.
//
// The order is what makes it safe (FR-26):
//
//  1. Stop automation, so no scan, watch, or scheduled run is in flight while
//     access is being taken away.
//  2. Let the runtime tear down its own active state — releasing access,
//     stripping credentials, keeping the audit journal.
//  3. Give up exclusively-owned resources; keep shared ones and drop only this
//     capability's association with them.
//  4. Remove the install record LAST.
//
// Step 4 being last is what makes a failed removal safe to retry: while the
// record is still there the capability reports itself installed and unhealthy,
// which is visible and repairable. Dropping the record first would leave live
// watchers behind with nothing recording that they exist (FR-15, FR-145).
func (s *Service) Remove(workspaceID, capabilityID string, opts RemoveOptions) (RemovalResult, error) {
	if s == nil || s.registry == nil {
		return RemovalResult{}, &Error{Code: CodeCapabilityUnavailable, Message: "Workspace capabilities are not available."}
	}
	ws, err := s.loadWorkspace(workspaceID)
	if err != nil {
		return RemovalResult{}, err
	}
	resolved, ok := s.resolveInstalled(ws, capabilityID)
	if !ok {
		// Removing something that is not installed is success, not an error:
		// a retry after a partial failure must be able to finish (FR-15).
		return RemovalResult{AlreadyRemoved: true}, nil
	}
	record := resolved.Record

	// 1 & 2. Stop automation, then let the runtime release its active state.
	// An unavailable runtime is not a reason to refuse removal — a capability
	// this build cannot resolve is exactly one a user should be able to get rid
	// of — but its resources must then be left alone rather than guessed at.
	runtime, bound := s.registry.Runtime(record.ID)
	if bound {
		if controller, canStop := runtime.(AutomationController); canStop {
			if stopErr := controller.StopCapabilityAutomation(workspaceID); stopErr != nil {
				return RemovalResult{}, &Error{
					Code:    CodeRemovalIncomplete,
					Message: "Ori could not stop this capability's background work, so it did not remove it.",
					Repair:  "Try again in a moment.",
					Err:     stopErr,
				}
			}
		}
		if remover, canRemove := runtime.(Remover); canRemove {
			if removeErr := remover.OnCapabilityRemove(workspaceID); removeErr != nil {
				return RemovalResult{}, &Error{
					Code:    CodeRemovalIncomplete,
					Message: "Ori stopped the background work but could not release this capability's access.",
					Repair:  "Try removing it again.",
					Err:     removeErr,
				}
			}
		}
	}

	result := RemovalResult{}

	// 3. Companion, only when the user made that separate choice AND the
	// capability actually created the agent.
	if opts.RemoveCompanion {
		for _, resource := range record.ResourcesOfKind(workspace.ResourceCompanionAgent) {
			if resource.Shared {
				// Adopted, not created. Removing it would take away an agent
				// that existed before this capability.
				continue
			}
			if s.companions == nil {
				break
			}
			remover, canRemove := s.companions.(CompanionRemover)
			if !canRemove {
				break
			}
			if removeErr := remover.RemoveCompanionAgent(workspaceID, resource.ID); removeErr != nil {
				return RemovalResult{}, &Error{
					Code:    CodeRemovalIncomplete,
					Message: "Ori could not remove the companion agent.",
					Repair:  "Try again, or remove the agent from the workspace's team.",
					Err:     removeErr,
				}
			}
			result.CompanionRemoved = true
		}
	}

	for _, resource := range record.OwnedResources {
		if resource.Shared {
			result.KeptShared = append(result.KeptShared, resource)
			continue
		}
		result.Released = append(result.Released, resource)
	}

	// 4. Drop the record last.
	if err := s.store.Update(workspaceID, func(current *workspace.Workspace) error {
		if !current.RemoveInstalledCapability(record.ID) {
			return nil
		}
		return nil
	}); err != nil {
		return RemovalResult{}, &Error{
			Code:    CodeRemovalIncomplete,
			Message: "Ori released this capability's access but could not finish removing it.",
			Repair:  "Try removing it again.",
			Err:     err,
		}
	}

	result.Removed = true
	return result, nil
}

// CompanionRemover is the optional counterpart to CompanionProvisioner. A
// provisioner that cannot remove agents simply leaves the companion in place,
// which is the safe direction to fail.
type CompanionRemover interface {
	RemoveCompanionAgent(workspaceID, agentInstanceID string) error
}

// resolveInstalled finds a workspace's record for one capability ID.
func (s *Service) resolveInstalled(ws *workspace.Workspace, capabilityID string) (Resolved, bool) {
	id := strings.TrimSpace(strings.ToLower(capabilityID))
	if id == "" || ws == nil {
		return Resolved{}, false
	}
	for _, resolved := range s.registry.ResolveAll(ws.GetInstalledCapabilities()) {
		if strings.EqualFold(resolved.Record.ID, id) {
			return resolved, true
		}
	}
	return Resolved{}, false
}
