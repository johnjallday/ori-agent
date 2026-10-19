package backuprecovery

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type storedSetupFixture struct {
	state SetupConnectionState
	calls int
	err   error
}

func (s *storedSetupFixture) ReadBackupSetup(context.Context, string) (SetupConnectionState, error) {
	s.calls++
	return s.state, s.err
}
func backupSetupFixture() setupwizard.StepRequest {
	return setupwizard.StepRequest{WorkspaceID: "invented-workspace", Step: workspace.SetupWizardStep{ID: "backup-connection", Kind: workspace.SetupStepKindReadiness, Required: true, Adapter: "backup_recovery"}}
}

func TestBackupSetupAdapterIsUnavailableWithoutAStoredReader(t *testing.T) {
	a := NewSetupAdapter(nil)
	for range 2 {
		result, err := a.Confirm(t.Context(), backupSetupFixture(), setupwizard.StepAction{Type: setupwizard.ActionConfirm})
		if err != nil || result.Ready || !result.Blocked || result.ErrorCategory != setupwizard.ErrorCategoryUnavailable {
			t.Fatal("unwired setup claimed readiness", err)
		}
	}
}

func TestBackupSetupAdapterConfirmOnlyReadsStoredFacts(t *testing.T) {
	reader := &storedSetupFixture{}
	a := NewSetupAdapter(reader)
	for _, state := range []SetupConnectionState{SetupUnavailable, SetupNotConfigured, SetupReviewRequired, SetupConnected, 99} {
		reader.state = state
		for _, summary := range []bool{false, true} {
			req := backupSetupFixture()
			if summary {
				req.Step.ID = "backup-summary"
				req.Step.Kind = workspace.SetupStepKindSummary
			}
			result, err := a.Confirm(t.Context(), req, setupwizard.StepAction{Type: setupwizard.ActionConfirm})
			if err != nil || result.Ready != (state == SetupConnected) {
				t.Fatal("setup trusted something other than its stored reader", state, err)
			}
		}
	}
	if reader.calls != 10 {
		t.Fatal("unexpected repeated/missing reads", reader.calls)
	}
	reader.err = errors.New("invented private provider diagnostic")
	result, err := a.Evaluate(t.Context(), backupSetupFixture())
	if err != nil || result.Ready || result.Summary == reader.err.Error() {
		t.Fatal("private source error escaped")
	}
}

func TestBackupSetupAdapterRejectsActionsAndForeignStepShapes(t *testing.T) {
	reader := &storedSetupFixture{state: SetupConnected}
	a := NewSetupAdapter(reader)
	for _, action := range []setupwizard.StepAction{{Type: "refresh"}, {Type: "connect"}, {Type: "confirm", Option: "approved"}, {Type: "confirm", Option: "/Volumes/Invented"}} {
		if _, err := a.Confirm(t.Context(), backupSetupFixture(), action); err != setupwizard.ErrInvalidAction {
			t.Fatal("wizard accepted a domain mutation")
		}
	}
	for _, mutate := range []func(*setupwizard.StepRequest){
		func(r *setupwizard.StepRequest) { r.Step.ID = "other" },
		func(r *setupwizard.StepRequest) { r.Step.Adapter = "file_janitor" },
		func(r *setupwizard.StepRequest) { r.Step.Kind = workspace.SetupStepKindDirectory },
		func(r *setupwizard.StepRequest) { r.Step.RequirementKey = "file-janitor-root" },
		func(r *setupwizard.StepRequest) { r.WorkspaceID = "../other" },
	} {
		req := backupSetupFixture()
		mutate(&req)
		result, err := a.Evaluate(t.Context(), req)
		if err != nil || result.Ready || result.ErrorCategory != setupwizard.ErrorCategoryUnsupported {
			t.Fatal("foreign step accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Evaluate(ctx, backupSetupFixture()); err == nil {
		t.Fatal("cancelled read continued")
	}
	if reader.calls != 0 {
		t.Fatal("invalid input reached owning state")
	}
}
