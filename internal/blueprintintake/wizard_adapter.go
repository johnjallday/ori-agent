package blueprintintake

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
)

const SetupAdapterID = "blueprint_intake"

// SetupState supplies the intake-specific readiness and confirmation behavior.
// The compiled adapter remains registered even before the intake service is
// available, in which case it reports an honest unavailable state.
type SetupState interface {
	EvaluateSetup(context.Context, setupwizard.StepRequest) (setupwizard.StepReadiness, error)
	ConfirmSetup(context.Context, setupwizard.StepRequest, setupwizard.StepAction) (setupwizard.StepReadiness, error)
}

type SetupAdapter struct {
	state SetupState
}

func NewSetupAdapter(state SetupState) *SetupAdapter {
	return &SetupAdapter{state: state}
}

func (a *SetupAdapter) ID() string { return SetupAdapterID }

func (a *SetupAdapter) Evaluate(ctx context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if a != nil && a.state != nil {
		return a.state.EvaluateSetup(ctx, req)
	}
	return setupwizard.StepReadiness{
		Blocked:       true,
		Summary:       "Blueprint intake is unavailable in this build.",
		ErrorCategory: setupwizard.ErrorCategoryUnavailable,
	}, nil
}

func (a *SetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, action setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if a != nil && a.state != nil {
		return a.state.ConfirmSetup(ctx, req, action)
	}
	return a.Evaluate(ctx, req)
}
