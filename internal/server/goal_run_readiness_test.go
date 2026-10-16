package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type goalSetupReaderStub struct {
	status setupwizard.Status
	err    error
	calls  int
}

func (s *goalSetupReaderStub) Status(context.Context, string) (setupwizard.Status, error) {
	s.calls++
	return s.status, s.err
}

func TestGoalSetupAdmissionUsesReadinessNotVisibility(t *testing.T) {
	ws := &workspace.Workspace{ID: "goal", TemplateProvenance: &workspace.TemplateProvenance{SetupWizard: &workspace.SetupWizard{Version: 1}}}
	for _, tc := range []struct {
		name   string
		status setupwizard.Status
		err    error
		ready  bool
	}{
		{"ready", setupwizard.Status{Applicable: true, State: workspace.SetupWizardStateReady}, nil, true},
		{"missing", setupwizard.Status{}, nil, false},
		{"dismissed", setupwizard.Status{Applicable: true, Dismissed: true, State: workspace.SetupWizardStateNotStarted}, nil, false},
		{"opened", setupwizard.Status{Applicable: true, AutoOpen: false, State: workspace.SetupWizardStateInProgress}, nil, false},
		{"regressed", setupwizard.Status{Applicable: true, State: workspace.SetupWizardStateNeedsAttention}, nil, false},
		{"unsupported", setupwizard.Status{Applicable: true, State: workspace.SetupWizardStateReady, Diagnostic: "unknown version"}, nil, false},
		{"unreadable", setupwizard.Status{}, errors.New("unreadable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &goalSetupReaderStub{status: tc.status, err: tc.err}
			blocked := checkGoalSetup(t.Context(), ws, reader)
			if (blocked == nil) != tc.ready || reader.calls != 1 {
				t.Fatalf("blocked=%v calls=%d", blocked, reader.calls)
			}
		})
	}
	if checkGoalSetup(t.Context(), ws, nil) == nil {
		t.Fatal("required check must fail closed")
	}
	reader := &goalSetupReaderStub{err: errors.New("must not be called")}
	if checkGoalSetup(t.Context(), &workspace.Workspace{ID: "plain"}, reader) != nil || reader.calls != 0 {
		t.Fatal("no declared wizard acquired a setup requirement")
	}
}

func TestGoalReadinessUndeclaredRequirementsRemainOptional(t *testing.T) {
	b := &ServerBuilder{}
	if blocked := b.goalRunReadiness(t.Context(), &workspace.Workspace{ID: "plain"}); blocked != nil {
		t.Fatal(blocked)
	}
	for _, p := range []*workspace.TemplateProvenance{
		{CapabilityRequirements: []workspace.CapabilityRequirement{{Key: "unknown"}}},
		{DirectoryRequirements: []workspace.DirectoryRequirement{{Key: "folder"}}},
		{RuntimeRequirements: &workspace.RuntimeRequirementsContract{}},
	} {
		if b.goalRunReadiness(t.Context(), &workspace.Workspace{ID: "required", TemplateProvenance: p}) == nil {
			t.Fatal("uncheckable requirement was treated as ready")
		}
	}
}

func TestGoalModelCheckCannotBorrowProviderForMissingAgent(t *testing.T) {
	b := &ServerBuilder{}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Missing", Agents: []string{"Ghost"}})
	if ok, reason := b.goalRunModelCheck(ws); ok || reason == "" {
		t.Fatal("missing entry agent passed model admission")
	}
}
