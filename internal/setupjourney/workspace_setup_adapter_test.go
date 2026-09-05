package setupjourney

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type workspaceSetupStore struct{ workspace *workspace.Workspace }

func (s *workspaceSetupStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	if s.workspace == nil || s.workspace.ID != id {
		return nil, errors.New("not found")
	}
	return s.workspace, nil
}

func (s *workspaceSetupStore) Update(id string, mutate func(*workspace.Workspace) error) error {
	if s.workspace == nil || s.workspace.ID != id {
		return errors.New("not found")
	}
	return mutate(s.workspace)
}

type projectFilesConnected bool

func (connected projectFilesConnected) FilesConnected(string) bool { return bool(connected) }

type liveAdapterSpy struct {
	evaluations int
	durable     string
}

func (a *liveAdapterSpy) ID() string { return "live_adapter" }
func (a *liveAdapterSpy) EvaluateDurable(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	a.evaluations++
	state := a.durable
	if state == "" {
		state = runtimecapability.DurableInProgress
	}
	return runtimecapability.DurableResult{State: state, Summary: "Live control configuration status."}, nil
}

func workspaceSetupFixture(t *testing.T) (*WorkspaceSetupAdapter, *workspaceSetupStore, *liveAdapterSpy) {
	t.Helper()
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Project"})
	project.ID = "project-1"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "plugin:neutral:project",
		RuntimeRequirements: &workspace.RuntimeRequirementsContract{
			SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
			OperatingModes: []workspace.RuntimeOperatingMode{
				{ID: fileOnlyModeID, Label: "File-only", Description: "Files are connected. Live control is not configured or tested."},
				{ID: "assisted", Label: "Assisted", Description: "Configure live control.", Requires: []string{"live"}},
			},
			Requirements: []workspace.RuntimeRequirement{{Key: "live", Label: "Live", Description: "Live control", Adapter: "live_adapter"}},
		},
		SetupWizard: &workspace.SetupWizard{
			Version: workspace.SetupWizardSchemaVersion, Title: "Set up project",
			Steps: []workspace.SetupWizardStep{
				{ID: "mode", Kind: workspace.SetupStepKindRuntimeMode, Required: true},
				{ID: "live", Kind: workspace.SetupStepKindRuntimeReadiness, RequirementKey: "live", Required: true},
				{ID: "summary", Kind: workspace.SetupStepKindSummary, Required: true},
			},
		},
	})
	store := &workspaceSetupStore{workspace: project}
	spy := &liveAdapterSpy{}
	registry := runtimecapability.NewRegistry()
	if err := registry.Register(spy); err != nil {
		t.Fatal(err)
	}
	runtime := runtimecapability.NewService(store, registry)
	wizard := setupwizard.NewService(store, setupwizard.NewRegistry())
	wizard.SetRuntimeService(runtime)
	return NewWorkspaceSetupAdapter(wizard, projectFilesConnected(true)), store, spy
}

func TestWorkspaceSetupAdapterSelectsOnlyFileModeWithoutLiveSideEffects(t *testing.T) {
	adapter, store, live := workspaceSetupFixture(t)
	scope := ReadScope{OwnerUserID: "owner-1", RunKind: RunKindRoot, RunID: "run", ProjectWorkspaceID: store.workspace.ID}
	before, err := adapter.Read(context.Background(), scope)
	if err != nil || before.Complete || before.WorkspaceSetup == nil || !before.WorkspaceSetup.FilesConnected {
		t.Fatalf("before = %+v err=%v", before, err)
	}
	material, err := adapter.Review(context.Background(), scope, ActionReviewFileOnlyMode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if material.WorkspaceSetup == nil || material.WorkspaceSetup.ModeID != "" || material.WorkspaceSetup.LiveControlConfigured || material.WorkspaceSetup.LiveControlTested {
		t.Fatalf("review projection = %+v", material.WorkspaceSetup)
	}
	result, err := adapter.Commit(context.Background(), scope, ActionSelectFileOnlyMode, nil, material)
	if err != nil || result.SelectedModeID != fileOnlyModeID {
		t.Fatalf("commit = %+v err=%v", result, err)
	}
	if live.evaluations != 0 {
		t.Fatalf("File-only evaluated live adapter %d times", live.evaluations)
	}
	state := store.workspace.GetRuntimeState()
	if state == nil || state.SelectedModeID != fileOnlyModeID || len(state.Grants) != 0 || len(state.RequirementStates) != 0 {
		t.Fatalf("File-only runtime state = %+v", state)
	}
	after, err := adapter.Read(context.Background(), scope)
	if err != nil || !after.Complete || after.WorkspaceSetup == nil || after.WorkspaceSetup.LiveControlConfigured || after.WorkspaceSetup.LiveControlTested {
		t.Fatalf("after = %+v err=%v", after, err)
	}
}

func TestWorkspaceSetupAdapterReadsCanonicallyReadyPermissionBearingMode(t *testing.T) {
	adapter, store, live := workspaceSetupFixture(t)
	live.durable = runtimecapability.DurableConfigured
	ctx := context.Background()
	completeAssistedWorkspaceSetup(t, adapter, store.workspace.ID)

	read, err := adapter.Read(ctx, ReadScope{RunKind: RunKindRoot, RunID: "run", ProjectWorkspaceID: store.workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !read.Complete || read.Result.SelectedModeID != "assisted" || read.WorkspaceSetup == nil {
		t.Fatalf("assisted mode did not become canonically complete: %+v", read)
	}
	if !read.WorkspaceSetup.LiveControlConfigured || !read.WorkspaceSetup.LiveControlTested {
		t.Fatalf("ready assisted mode lost canonical live readiness: %+v", read.WorkspaceSetup)
	}
	if live.evaluations == 0 {
		t.Fatal("permission-bearing mode was accepted without a canonical runtime read")
	}
}

func TestWorkspaceSetupAdapterReportsSelectedLiveRegressionAsNeedsAttention(t *testing.T) {
	adapter, store, live := workspaceSetupFixture(t)
	live.durable = runtimecapability.DurableConfigured
	ctx := context.Background()
	completeAssistedWorkspaceSetup(t, adapter, store.workspace.ID)
	live.durable = runtimecapability.DurableNeedsAttention

	read, err := adapter.Read(ctx, ReadScope{RunKind: RunKindRoot, RunID: "run", ProjectWorkspaceID: store.workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if read.Complete || read.BlockedReason != ReasonRuntimeNeedsAttention || read.WorkspaceSetup == nil || read.WorkspaceSetup.ModeID != "assisted" {
		t.Fatalf("live regression was not narrow and resumable: %+v", read)
	}
	if read.WorkspaceSetup.LiveControlConfigured || read.WorkspaceSetup.LiveControlTested {
		t.Fatalf("historical mode selection was claimed as current live readiness: %+v", read.WorkspaceSetup)
	}
}

func completeAssistedWorkspaceSetup(t *testing.T, adapter *WorkspaceSetupAdapter, workspaceID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := adapter.wizard.Confirm(ctx, workspaceID, "mode", setupwizard.StepAction{Type: setupwizard.ActionConfirm, Option: "assisted"}); err != nil {
		t.Fatalf("select assisted mode: %v", err)
	}
	if _, err := adapter.wizard.Confirm(ctx, workspaceID, "summary", setupwizard.StepAction{Type: setupwizard.ActionConfirm}); err != nil {
		t.Fatalf("confirm setup summary: %v", err)
	}
}

func TestWorkspaceSetupAdapterRejectsFieldsAndMissingProjectEntry(t *testing.T) {
	adapter, store, _ := workspaceSetupFixture(t)
	scope := ReadScope{RunKind: RunKindRoot, RunID: "run", ProjectWorkspaceID: store.workspace.ID}
	if _, err := adapter.Review(context.Background(), scope, ActionReviewFileOnlyMode, []byte(`{"mode_id":"assisted"}`)); err == nil {
		t.Fatal("client-selected mode field was accepted")
	}
	adapter.readiness = projectFilesConnected(false)
	read, err := adapter.Read(context.Background(), scope)
	if err != nil || read.BlockedReason != ReasonOwnerUnavailable {
		t.Fatalf("missing project entry read = %+v err=%v", read, err)
	}
}
