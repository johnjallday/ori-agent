package blueprintintakewizard

import (
	"context"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/blueprintintake"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const SetupAdapterID = "blueprint_intake"

type SourceState interface {
	ListSources(workspaceID, intakeKey string) ([]blueprintintake.SourceRecord, error)
	ConsentStatus(context.Context, string, string) (blueprintintake.ConsentStatus, error)
}

type ReviewState interface {
	Proposal(workspaceID, intakeKey string) (blueprintintake.Proposal, error)
}

type AutomationState interface {
	Status(workspaceID, intakeKey string) (approved, registered bool, err error)
	Approve(workspaceID, intakeKey string) error
}

type SetupAdapter struct {
	sources    SourceState
	review     ReviewState
	automation AutomationState
}

func NewSetupAdapter(sources SourceState, review ReviewState) *SetupAdapter {
	return &SetupAdapter{sources: sources, review: review}
}

func (a *SetupAdapter) ID() string { return SetupAdapterID }

func (a *SetupAdapter) SetAutomation(automation AutomationState) {
	if a != nil {
		a.automation = automation
	}
}

func (a *SetupAdapter) Evaluate(ctx context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if a == nil || a.sources == nil {
		return unavailableReadiness(), nil
	}
	if req.Step.Kind == "intake_review" {
		return a.evaluateReview(req)
	}
	if req.Step.Kind == "automation_review" {
		return a.evaluateAutomation(req)
	}
	if req.Intake == nil {
		return setupwizard.StepReadiness{}, errors.New("intake requirement is missing from the workspace snapshot")
	}
	sources, err := a.sources.ListSources(req.WorkspaceID, req.Intake.Key)
	if err != nil {
		return setupwizard.StepReadiness{}, err
	}
	if len(sources) == 0 {
		return setupwizard.StepReadiness{Summary: "Add at least one source before continuing.", ErrorCategory: setupwizard.ErrorCategoryNotConfigured}, nil
	}
	consent, err := a.sources.ConsentStatus(ctx, req.WorkspaceID, req.Intake.Key)
	if err != nil {
		return setupwizard.StepReadiness{Blocked: true, Summary: "Ori could not determine this workspace's model provider.", ErrorCategory: setupwizard.ErrorCategoryUnavailable}, nil
	}
	if !consent.Accepted {
		return setupwizard.StepReadiness{Summary: "Review and accept the content-reading statement before continuing.", ErrorCategory: setupwizard.ErrorCategoryPermissionRequired}, nil
	}
	return setupwizard.StepReadiness{Ready: true, Summary: "Sources added and content reading accepted."}, nil
}

func (a *SetupAdapter) evaluateReview(req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if req.Intake == nil {
		return setupwizard.StepReadiness{Blocked: true, Summary: "The intake declaration is unavailable.", ErrorCategory: setupwizard.ErrorCategoryUnsupported}, nil
	}
	if a.review == nil {
		return unavailableReadiness(), nil
	}
	proposal, err := a.review.Proposal(req.WorkspaceID, req.Intake.Key)
	if errors.Is(err, blueprintintake.ErrProposalNotFound) {
		return setupwizard.StepReadiness{Summary: "Run the intake skill, then review what it proposes.", ErrorCategory: setupwizard.ErrorCategoryNotConfigured}, nil
	}
	if err != nil {
		return setupwizard.StepReadiness{}, err
	}
	if proposal.Status == "applied" || proposal.Status == "skipped" {
		return setupwizard.StepReadiness{Ready: true, Summary: "The intake proposal was reviewed."}, nil
	}
	return setupwizard.StepReadiness{Summary: "Review the proposed items before continuing.", ErrorCategory: setupwizard.ErrorCategoryNotConfigured}, nil
}

func (a *SetupAdapter) evaluateAutomation(req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	resolver, ok := a.sources.(interface {
		RequirementForDirectory(workspaceID, directoryKey string) (workspace.IntakeRequirement, error)
	})
	if !ok || a.automation == nil {
		return unavailableReadiness(), nil
	}
	requirement, err := resolver.RequirementForDirectory(req.WorkspaceID, req.Step.RequirementKey)
	if err != nil {
		return setupwizard.StepReadiness{Blocked: true, Summary: "The intake automation declaration is unavailable.", ErrorCategory: setupwizard.ErrorCategoryUnsupported}, nil
	}
	approved, registered, err := a.automation.Status(req.WorkspaceID, requirement.Key)
	if err != nil {
		return setupwizard.StepReadiness{}, err
	}
	if approved && registered {
		return setupwizard.StepReadiness{Ready: true, Summary: "Folder watching and the daily link check are enabled."}, nil
	}
	if approved {
		return setupwizard.StepReadiness{Blocked: true, Summary: "Approval was saved, but folder watching is not running. Try again.", ErrorCategory: setupwizard.ErrorCategoryDomainError}, nil
	}
	return setupwizard.StepReadiness{Summary: "Approve folder watching and a daily link check. Changes create a proposal for your review; they never change records automatically.", ErrorCategory: setupwizard.ErrorCategoryPermissionRequired}, nil
}

func (a *SetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, _ setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if req.Step.Kind == "automation_review" {
		resolver, ok := a.sources.(interface {
			RequirementForDirectory(workspaceID, directoryKey string) (workspace.IntakeRequirement, error)
		})
		if !ok || a.automation == nil {
			return unavailableReadiness(), errors.New("re-intake automation is unavailable")
		}
		requirement, err := resolver.RequirementForDirectory(req.WorkspaceID, req.Step.RequirementKey)
		if err != nil {
			return setupwizard.StepReadiness{}, err
		}
		if err := a.automation.Approve(req.WorkspaceID, requirement.Key); err != nil {
			return setupwizard.StepReadiness{}, err
		}
	}
	readiness, err := a.Evaluate(ctx, req)
	if err != nil {
		return readiness, err
	}
	if !readiness.Ready {
		return readiness, fmt.Errorf("%w: %s", setupwizard.ErrStepRejected, readiness.Summary)
	}
	return readiness, nil
}

func unavailableReadiness() setupwizard.StepReadiness {
	return setupwizard.StepReadiness{Blocked: true, Summary: "Blueprint intake is unavailable in this build.", ErrorCategory: setupwizard.ErrorCategoryUnavailable}
}
