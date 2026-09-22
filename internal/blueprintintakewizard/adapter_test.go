package blueprintintakewizard

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintintake"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeSources struct {
	records  []blueprintintake.SourceRecord
	accepted bool
}

func (f *fakeSources) ListSources(string, string) ([]blueprintintake.SourceRecord, error) {
	return f.records, nil
}

func (f *fakeSources) ConsentStatus(context.Context, string, string) (blueprintintake.ConsentStatus, error) {
	return blueprintintake.ConsentStatus{Accepted: f.accepted}, nil
}

func (f *fakeSources) RequirementForDirectory(string, string) (workspace.IntakeRequirement, error) {
	return workspace.IntakeRequirement{Key: "materials"}, nil
}

type fakeAutomation struct {
	approved, registered bool
	approveCalls         int
}

func (f *fakeAutomation) Status(string, string) (bool, bool, error) {
	return f.approved, f.registered, nil
}
func (f *fakeAutomation) Approve(string, string) error {
	f.approveCalls++
	f.approved, f.registered = true, true
	return nil
}

type fakeReview struct{ proposal blueprintintake.Proposal }

func (f fakeReview) Proposal(string, string) (blueprintintake.Proposal, error) {
	if f.proposal.Status == "" {
		return blueprintintake.Proposal{}, blueprintintake.ErrProposalNotFound
	}
	return f.proposal, nil
}

func TestIntakeSetupCannotCompleteWithoutSourcesAndConsent(t *testing.T) {
	sources := &fakeSources{}
	adapter := NewSetupAdapter(sources, fakeReview{})
	requirement := workspace.IntakeRequirement{Key: "materials"}
	req := setupwizard.StepRequest{WorkspaceID: "ws", Intake: &requirement, Step: workspace.SetupWizardStep{Kind: workspace.SetupStepKindIntake}}

	readiness, err := adapter.Evaluate(context.Background(), req)
	if err != nil || readiness.Ready || readiness.ErrorCategory != setupwizard.ErrorCategoryNotConfigured {
		t.Fatalf("empty readiness = %+v, %v", readiness, err)
	}
	sources.records = []blueprintintake.SourceRecord{{ID: "one", Status: blueprintintake.SourceStatusParsed}}
	readiness, err = adapter.Evaluate(context.Background(), req)
	if err != nil || readiness.Ready || readiness.ErrorCategory != setupwizard.ErrorCategoryPermissionRequired {
		t.Fatalf("unconsented readiness = %+v, %v", readiness, err)
	}
	if _, err := adapter.Confirm(context.Background(), req, setupwizard.StepAction{Type: setupwizard.ActionConfirm}); !errors.Is(err, setupwizard.ErrStepRejected) {
		t.Fatalf("Confirm completed without consent: %v", err)
	}
	sources.accepted = true
	readiness, err = adapter.Confirm(context.Background(), req, setupwizard.StepAction{Type: setupwizard.ActionConfirm})
	if err != nil || !readiness.Ready {
		t.Fatalf("consented readiness = %+v, %v", readiness, err)
	}
}

func TestAutomationReviewDisclosesAndEnablesReintakeOnlyOnConfirm(t *testing.T) {
	automation := &fakeAutomation{}
	adapter := NewSetupAdapter(&fakeSources{}, fakeReview{})
	adapter.SetAutomation(automation)
	req := setupwizard.StepRequest{WorkspaceID: "ws", Step: workspace.SetupWizardStep{Kind: workspace.SetupStepKindAutomationReview, RequirementKey: "course-materials-folder"}}
	readiness, err := adapter.Evaluate(context.Background(), req)
	if err != nil || readiness.Ready || automation.approveCalls != 0 {
		t.Fatalf("read-only readiness = %+v, calls=%d, err=%v", readiness, automation.approveCalls, err)
	}
	readiness, err = adapter.Confirm(context.Background(), req, setupwizard.StepAction{Type: setupwizard.ActionConfirm})
	if err != nil || !readiness.Ready || automation.approveCalls != 1 {
		t.Fatalf("confirmed readiness = %+v, calls=%d, err=%v", readiness, automation.approveCalls, err)
	}
}

func TestReviewSetupCompletesOnlyAfterApplyOrSkip(t *testing.T) {
	requirement := workspace.IntakeRequirement{Key: "materials"}
	req := setupwizard.StepRequest{WorkspaceID: "ws", Intake: &requirement, Step: workspace.SetupWizardStep{Kind: workspace.SetupStepKindIntakeReview}}
	for _, status := range []string{"applied", "skipped"} {
		adapter := NewSetupAdapter(&fakeSources{}, fakeReview{proposal: blueprintintake.Proposal{Status: status}})
		readiness, err := adapter.Confirm(context.Background(), req, setupwizard.StepAction{Type: setupwizard.ActionConfirm})
		if err != nil || !readiness.Ready {
			t.Fatalf("%s readiness = %+v, %v", status, readiness, err)
		}
	}
}
