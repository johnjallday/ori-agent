package calendarhttp

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// SetupAdapterID is the registry key a blueprint manifest may name for Calendar
// Ops. It matches projecttemplates.ValidSetupWizardAdapters.
const SetupAdapterID = "calendar_ops"

// SetupAdapter answers the Setup Wizard's questions about a Calendar Ops
// workspace.
//
// Calendar already has a setup state machine — connector_missing,
// auth_required, mapping_required, validation_failed, ready, degraded — and
// this adapter's whole job is to translate it, not to re-derive it. Every
// answer comes from deriveSetupState, the same function the Calendar setup card
// reads, so the wizard and the card can never disagree about where a workspace
// is up to.
//
// It commits nothing. Choosing a connector, signing in, editing the mapping,
// running the connection test, and saving preferences all happen at Calendar's
// own endpoints, which already enforce their own validation and never expose a
// mutation the user did not map. The wizard's part is to say what is missing,
// send the user to the control that fixes it, and re-read the state afterwards.
type SetupAdapter struct {
	handler *Handler
	// workspaces reads the canonical workspace record the binding lives on.
	workspaces FolderStore
}

// NewSetupAdapter builds the adapter over the Calendar Ops handler.
func NewSetupAdapter(handler *Handler, workspaces FolderStore) *SetupAdapter {
	return &SetupAdapter{handler: handler, workspaces: workspaces}
}

// ID implements setupwizard.Adapter.
func (a *SetupAdapter) ID() string { return SetupAdapterID }

// Evaluate reports where each step stands. Read-only: no connector is created,
// authorized, mapped, or tested by looking.
func (a *SetupAdapter) Evaluate(_ context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if a == nil || a.handler == nil || a.workspaces == nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Calendar setup is unavailable in this build.",
			ErrorCategory: setupwizard.ErrorCategoryUnavailable,
		}, nil
	}
	ws, err := a.workspaces.GetFolderWorkspace(req.WorkspaceID)
	if err != nil || ws == nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Ori could not read this workspace's calendar settings.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}, nil
	}
	state := a.handler.deriveSetupState(ws).state
	switch req.Step.Kind {
	case agentworkspace.SetupStepKindCapabilityConnect:
		return connectReadiness(state), nil
	case agentworkspace.SetupStepKindCapabilityConfigure:
		return configureReadiness(state), nil
	default:
		return overallReadiness(state), nil
	}
}

// Confirm re-reads the state.
//
// There is deliberately nothing to commit here. Every Calendar mutation —
// adding a connector, authorizing it, saving a mapping, running the test —
// belongs to an endpoint that validates it properly, and routing any of them
// through a generic wizard action would be a second, weaker door into the same
// capability. A step advances when the domain says it is satisfied.
func (a *SetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, _ setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if a == nil || a.handler == nil {
		return setupwizard.StepReadiness{}, fmt.Errorf("calendar setup is unavailable")
	}
	return a.Evaluate(ctx, req)
}

// connectReadiness maps the state onto "is there a working connection?".
func connectReadiness(state calendar.SetupState) setupwizard.StepReadiness {
	switch state {
	case calendar.SetupConnectorMissing:
		return setupwizard.StepReadiness{
			Summary:       "No calendar connector is set up for this workspace yet.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	case calendar.SetupAuthRequired:
		return setupwizard.StepReadiness{
			Summary:       "The connector is added but not signed in yet.",
			ErrorCategory: setupwizard.ErrorCategoryPermissionRequired,
		}
	case calendar.SetupDegraded:
		// Degraded is deliberately not auth_required: the connection worked and
		// has stopped, which is a repair rather than an unfinished setup.
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "The calendar connector is not responding. Reconnect it to carry on.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}
	default:
		return setupwizard.StepReadiness{Ready: true, Summary: "Connected to your calendar."}
	}
}

// configureReadiness maps the state onto "is the mapping saved and tested?".
func configureReadiness(state calendar.SetupState) setupwizard.StepReadiness {
	switch state {
	case calendar.SetupConnectorMissing, calendar.SetupAuthRequired:
		return setupwizard.StepReadiness{
			Summary:       "Connect a calendar first.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	case calendar.SetupMappingRequired:
		return setupwizard.StepReadiness{
			Summary:       "Ori still needs to know which tools list your calendars and your events.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	case calendar.SetupValidationFailed:
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "The connection test has not passed for this mapping yet.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}
	case calendar.SetupDegraded:
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "The connector stopped responding, so the mapping cannot be confirmed.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}
	default:
		return setupwizard.StepReadiness{
			Ready:   true,
			Summary: "Your calendars and events are mapped, and the connection test passed.",
		}
	}
}

// overallReadiness is the readiness and summary steps' verdict: Calendar Ops is
// usable exactly when its own state machine says ready.
func overallReadiness(state calendar.SetupState) setupwizard.StepReadiness {
	switch state {
	case calendar.SetupReady:
		return setupwizard.StepReadiness{Ready: true, Summary: "Calendar Ops is connected and tested."}
	case calendar.SetupDegraded:
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "The calendar connection stopped working.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}
	default:
		return setupwizard.StepReadiness{
			Summary:       "Setup is not finished yet.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	}
}
