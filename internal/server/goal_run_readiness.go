package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type goalSetupStatusReader interface {
	Status(context.Context, string) (setupwizard.Status, error)
}

// checkGoalSetup is deliberately separate from dialog visibility. Status can
// persist regressions and fire setup-completion bookkeeping, so callers must
// never invoke it while holding a workspace mutation lock.
func checkGoalSetup(ctx context.Context, ws *workspace.Workspace, service goalSetupStatusReader) *workspace.MissionBlockedError {
	p := ws.GetTemplateProvenance()
	if p == nil || p.SetupWizard == nil {
		return nil
	}
	if service == nil {
		return goalSetupUnavailable()
	}
	status, err := service.Status(ctx, ws.ID)
	if err != nil || !status.Applicable || status.Diagnostic != "" {
		return goalSetupUnavailable()
	}
	if !status.Ready() {
		return &workspace.MissionBlockedError{Code: "goal_setup_required", Reason: "Complete or repair workspace setup, then choose Run now to get goal suggestions."}
	}
	return nil
}

func goalSetupUnavailable() *workspace.MissionBlockedError {
	return &workspace.MissionBlockedError{Code: "goal_setup_unavailable", Reason: "Required workspace setup could not be checked. Restore setup support, then choose Run now."}
}

func (b *ServerBuilder) goalRunReadiness(ctx context.Context, ws *workspace.Workspace) *workspace.MissionBlockedError {
	if b.setupWizardService == nil {
		if blocked := checkGoalSetup(ctx, ws, nil); blocked != nil {
			return blocked
		}
	} else if blocked := checkGoalSetup(ctx, ws, b.setupWizardService); blocked != nil {
		return blocked
	}
	p := ws.GetTemplateProvenance()
	if p != nil {
		// A successful wizard evaluates its declared domain requirements live.
		// A requirement outside that wizard cannot borrow its ready verdict.
		covered := func(key string) bool {
			if p.SetupWizard == nil {
				return false
			}
			for _, step := range p.SetupWizard.Steps {
				if step.Required && step.Adapter != "" && strings.EqualFold(step.RequirementKey, key) {
					return true
				}
			}
			return false
		}
		for _, requirement := range p.CapabilityRequirements {
			if covered(requirement.Key) {
				continue
			}
			if strings.EqualFold(requirement.Key, workspace.CapabilityEmail) && b.emailReadiness != nil {
				if _, blocked := b.emailReadiness.EvaluateTaskCapability(ws.ID, requirement.Key); blocked != nil {
					return &workspace.MissionBlockedError{Code: "goal_capability_required", Reason: blocked.Reason + " Then choose Run now."}
				}
				continue
			}
			return goalSetupUnavailable()
		}
		for _, requirement := range p.DirectoryRequirements {
			if !covered(requirement.Key) {
				return goalSetupUnavailable()
			}
		}
		for _, requirement := range p.IntakeRequirements {
			if !covered(requirement.Key) {
				return goalSetupUnavailable()
			}
		}
		if p.RuntimeRequirements != nil {
			if b.runtimeCapabilityService == nil {
				return goalSetupUnavailable()
			}
			status, err := b.runtimeCapabilityService.Status(ctx, ws.ID)
			if err != nil || !status.Applicable {
				return goalSetupUnavailable()
			}
			if status.DurableState != runtimecapability.DurableConfigured || status.FirstBlocker != nil {
				return &workspace.MissionBlockedError{Code: "goal_runtime_required", Reason: "Complete workspace runtime setup, then choose Run now."}
			}
			for _, requirement := range status.Requirements {
				claimed, blocked := b.runtimeCapabilityService.EvaluateTaskCapabilityForTask(ws.ID, workspace.Task{To: ws.EntryAgentName()}, requirement.Key)
				if !claimed {
					return goalSetupUnavailable()
				}
				if blocked != nil {
					return &workspace.MissionBlockedError{Code: "goal_runtime_required", Reason: blocked.Reason + " Then choose Run now."}
				}
			}
		}
	}
	// Validate only the approved directory's identity. No project file is read,
	// and a missing/damaged binding never falls back to another folder.
	if _, bound, err := workspace.SoftwareProjectFolder(ws, projectlibrary.DirectoryIdentity); bound && err != nil {
		return &workspace.MissionBlockedError{Code: "goal_folder_unavailable", Reason: "Reconnect the approved project folder, then choose Run now. No project contents were read."}
	}
	return nil
}
