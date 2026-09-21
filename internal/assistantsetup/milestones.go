package assistantsetup

import "strings"

const (
	existingTeamMilestoneID   = "existing_team"
	existingTeamMilestoneName = "Existing team kept as is"
)

// milestoneInput is everything the derivation may read. It is a snapshot of
// durable rows plus one in-process fact (a live preparation attempt); nothing
// here is a clock, a timer, or a display string.
type milestoneInput struct {
	Run       *Run
	Operation *Operation // the workspace operation; nil when absent
	Resources []Resource
	// InFlight reports a preparation attempt currently executing in this
	// process. It is the only source of creating/reusing.
	InFlight bool
	// WorkspaceName is the resolved target's name, preferred once receipted.
	WorkspaceName string
}

// deriveMilestones returns the list-driven milestone sequence for one run.
//
// Created and Reused are reported only when a matching receipt exists
// (`workspace` receipts for the workspace, `agent_instance` receipts for the
// team). Everything else is derived from the durable operation state, so a
// reopened database yields the same answer with no preparer call. List order is
// for reading, not an execution claim: agents are actually seeded before the
// workspace is persisted.
func deriveMilestones(in milestoneInput) []Milestone {
	run := in.Run
	if run == nil {
		return nil
	}
	milestones := []Milestone{{
		ID: string(MilestoneWorkspace), Kind: MilestoneWorkspace, Name: WorkspaceName,
		Action: workspaceAction(run.TargetMode), BlueprintID: run.BlueprintID, BlueprintVersion: run.BlueprintVersion,
	}}
	switch {
	case run.TargetMode == TargetAdopt:
		milestones = append(milestones, Milestone{
			ID: existingTeamMilestoneID, Kind: MilestoneExistingTeam, Name: existingTeamMilestoneName, Action: "reuse",
		})
	case strings.TrimSpace(run.TeamRole.RoleID) != "":
		role := run.TeamRole
		milestones = append(milestones, Milestone{
			ID: "role:" + role.RoleID, Kind: MilestoneAgentRole, Name: role.Name, RoleID: role.RoleID,
			Action: role.Action, BlueprintID: run.BlueprintID, BlueprintVersion: run.BlueprintVersion,
		})
	}

	receipts := receiptsByKind(in)
	_, profileReceipted := receipts[ResourceAgentProfile]
	unreceipted := false
	for index := range milestones {
		milestone := &milestones[index]
		receipt := receiptFor(milestone.Kind, receipts)
		if receipt != nil {
			applyReceipt(milestone, receipt, in)
			continue
		}
		if milestone.Kind == MilestoneAgentRole && profileReceipted && !in.InFlight {
			// The profile provably exists but is not attached to the workspace:
			// that is never Created.
			milestone.Status = MilestoneNeedsReview
			milestone.ErrorCode = codeAgentNotAttached
			unreceipted = true
			continue
		}
		milestone.Status = pendingStatus(milestone, in, !unreceipted)
		if milestone.Status == MilestoneNeedsReview || milestone.Status == MilestoneFailed {
			milestone.ErrorCode = milestoneErrorCode(in)
		}
		if milestone.Status != MilestoneCreating && milestone.Status != MilestoneReusing {
			unreceipted = true
		}
	}
	return milestones
}

func workspaceAction(mode TargetMode) string {
	if mode == TargetAdopt {
		return "reuse"
	}
	return "create"
}

// receiptsByKind keeps only receipts written by the workspace operation, so a
// later folder or monitoring receipt can never be mistaken for a milestone.
func receiptsByKind(in milestoneInput) map[string]Resource {
	receipts := map[string]Resource{}
	if in.Operation == nil {
		return receipts
	}
	for _, resource := range in.Resources {
		if resource.OperationID != in.Operation.ID {
			continue
		}
		switch resource.Kind {
		case ResourceWorkspace, ResourceAgentInstance, ResourceAgentProfile:
			if _, exists := receipts[resource.Kind]; !exists {
				receipts[resource.Kind] = resource
			}
		}
	}
	return receipts
}

func receiptFor(kind MilestoneKind, receipts map[string]Resource) *Resource {
	key := ResourceAgentInstance
	if kind == MilestoneWorkspace {
		key = ResourceWorkspace
	}
	if resource, ok := receipts[key]; ok {
		return &resource
	}
	return nil
}

func applyReceipt(milestone *Milestone, receipt *Resource, in milestoneInput) {
	recorded := receipt.CreatedAt
	milestone.ResourceID = receipt.ResourceID
	milestone.Ownership = receipt.Ownership
	milestone.RecordedAt = &recorded
	if receipt.Ownership == OwnershipCreated {
		milestone.Status = MilestoneCreated
	} else {
		milestone.Status = MilestoneReused
	}
	switch milestone.Kind {
	case MilestoneWorkspace:
		milestone.Placement = PlacementWorkspaceDirectory
		if name := strings.TrimSpace(in.WorkspaceName); name != "" {
			milestone.Name = name
		}
	default:
		milestone.Placement = PlacementAgentRoster
		milestone.NeedsModel = milestone.Kind == MilestoneAgentRole && !in.Run.TeamRole.ModelConfigured
	}
}

// pendingStatus classifies a milestone with no receipt. first reports whether
// every earlier milestone in the list has a receipt or is currently in flight;
// only the first un-receipted milestone may carry a failure or review state.
func pendingStatus(milestone *Milestone, in milestoneInput, first bool) MilestoneStatus {
	if in.InFlight {
		if milestone.Action == "reuse" {
			return MilestoneReusing
		}
		return MilestoneCreating
	}
	run, operation := in.Run, in.Operation
	if !first || operation == nil {
		return MilestonePending
	}
	switch {
	case operation.Status == OperationFailed:
		return MilestoneFailed
	case operation.Status == OperationUnresolved || run.Status == RunReconcileRequired:
		return MilestoneNeedsReview
	case operation.Status == OperationSucceeded:
		// A succeeded operation whose receipt is missing cannot be proven.
		return MilestoneNeedsReview
	case operation.Status == OperationRunning:
		// Running with no live attempt in this process: the process stopped
		// mid-preparation. The work is unproven, not lost and not repeated.
		// (A claimed operation was never started, so it provably did nothing
		// and stays pending.)
		return MilestoneNeedsReview
	}
	return MilestonePending
}

func milestoneErrorCode(in milestoneInput) string {
	if in.Operation != nil && in.Operation.Status == OperationRunning {
		return codePreparationInterrupted
	}
	if in.Operation != nil && strings.TrimSpace(in.Operation.SafeErrorCode) != "" {
		return in.Operation.SafeErrorCode
	}
	if in.Run != nil && strings.TrimSpace(in.Run.LastErrorCode) != "" {
		return in.Run.LastErrorCode
	}
	return ""
}
