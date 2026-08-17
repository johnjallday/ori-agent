package runtimecapability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type scopedGrantAdapter struct {
	*grantAdapter
	scope    CapabilityExecutionScope
	scopeErr error
}

func (a *scopedGrantAdapter) ResolveExecutionScope(context.Context, ExecutionScopeRequest) (CapabilityExecutionScope, error) {
	return a.scope, a.scopeErr
}

func executionScopeService(t *testing.T) (*Service, *runtimeStore, *scopedGrantAdapter, string, string) {
	t.Helper()
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	runnerRoot := filepath.Join(base, "runner")
	for _, dir := range []string{workspaceRoot, runnerRoot} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	adapter := &scopedGrantAdapter{
		grantAdapter: &grantAdapter{recordingAdapter: &recordingAdapter{id: "runtime_adapter", durable: DurableResult{State: DurableConfigured}}},
		scope: CapabilityExecutionScope{
			AdditionalWritableRoots: []string{runnerRoot},
			NetworkPosture:          CapabilityNetworkLocal,
			AllowedMCPServers:       []string{"trusted_runtime"},
		},
	}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	contract := contractWithRequirements("runtime")
	contract.Requirements[0].Adapter = adapter.ID()
	contract.OperatingModes = append(contract.OperatingModes, workspace.RuntimeOperatingMode{ID: "limited", Label: "Limited", Description: "Use files."})
	ws := runtimeWorkspace(contract)
	ws.AgentInstances = []workspace.AgentInstance{{ID: "agent-1", Name: "Producer"}, {ID: "agent-2", Name: "Other"}}
	ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})
	store := &runtimeStore{ws: ws}
	return NewService(store, registry), store, adapter, workspaceRoot, runnerRoot
}

func TestResolveTaskExecutionScopeRequiresExactGrantAndTaskCapability(t *testing.T) {
	service, store, _, workspaceRoot, runnerRoot := executionScopeService(t)
	ctx := context.Background()

	if scope, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-1", []string{"planning"}, workspaceRoot); err != nil || scope != nil {
		t.Fatalf("unrelated task scope = %+v, %v", scope, err)
	}
	if _, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-1", []string{"runtime"}, workspaceRoot); !errors.Is(err, ErrExecutionScopeUnavailable) {
		t.Fatalf("no-grant scope error = %v", err)
	}
	if scope, err := service.ResolveTaskExecutionScope(ctx, "other-workspace", "agent-1", []string{"runtime"}, workspaceRoot); err == nil || scope != nil {
		t.Fatalf("wrong-workspace scope = %+v, %v", scope, err)
	}
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-2", []string{"runtime"}, workspaceRoot); !errors.Is(err, ErrExecutionScopeUnavailable) {
		t.Fatalf("wrong-agent scope error = %v", err)
	}

	events := captureRuntimeEvents(t)
	scope, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-1", []string{"runtime", "runtime"}, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, _ := filepath.EvalSymlinks(workspaceRoot)
	canonicalRunner, _ := filepath.EvalSymlinks(runnerRoot)
	if scope == nil || scope.WorkspaceRoot != canonicalWorkspace || len(scope.AdditionalWritableRoots) != 1 || scope.AdditionalWritableRoots[0] != canonicalRunner {
		t.Fatalf("resolved roots = %+v", scope)
	}
	if scope.NetworkPosture != llm.CLINetworkCapabilityLocal || len(scope.CapabilityKeys) != 1 || scope.CapabilityKeys[0] != "runtime" || len(scope.AllowedMCPServers) != 1 {
		t.Fatalf("resolved posture = %+v", scope)
	}
	if len(*events) != 1 || (*events)[0][eventFieldName] != EventScopeUseDecision || (*events)[0][eventFieldOutcome] != "allowed" {
		t.Fatalf("scope-use audit event = %+v", *events)
	}

	// Unknown/non-runtime keys never inherit the grant.
	if unknown, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-1", []string{"unknown_runtime"}, workspaceRoot); err != nil || unknown != nil {
		t.Fatalf("unknown capability scope = %+v, %v", unknown, err)
	}

	// A later switch to a limited mode removes authority immediately without
	// deleting the grant record.
	state := store.ws.GetRuntimeState()
	state.SelectedModeID = "limited"
	store.ws.SetRuntimeState(state)
	if _, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-1", []string{"runtime"}, workspaceRoot); !errors.Is(err, ErrExecutionScopeUnavailable) {
		t.Fatalf("file-only scope error = %v", err)
	}
	state.SelectedModeID = "assisted"
	store.ws.SetRuntimeState(state)

	if _, err := service.Revoke(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-1", []string{"runtime"}, workspaceRoot); !errors.Is(err, ErrExecutionScopeUnavailable) {
		t.Fatalf("revoked scope error = %v", err)
	}
}

func TestResolveTaskExecutionScopeRechecksCanonicalRoots(t *testing.T) {
	service, store, adapter, workspaceRoot, _ := executionScopeService(t)
	ctx := context.Background()
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(filepath.Dir(workspaceRoot), "linked-runner")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	adapter.scope.AdditionalWritableRoots = []string{linked}
	if _, err := service.ResolveTaskExecutionScope(ctx, store.ws.ID, "agent-1", []string{"runtime"}, workspaceRoot); !errors.Is(err, ErrExecutionScopeUnavailable) {
		t.Fatalf("symlink root scope error = %v", err)
	}
}
