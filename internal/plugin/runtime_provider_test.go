package plugin

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

type providerFakeProcess struct {
	mu        sync.Mutex
	healthy   bool
	lastArgs  map[string]any
	operation string
}

func (p *providerFakeProcess) Start(context.Context) error { p.healthy = true; return nil }
func (p *providerFakeProcess) Stop(context.Context) error  { p.healthy = false; return nil }
func (p *providerFakeProcess) Healthy() bool               { return p.healthy }
func (p *providerFakeProcess) Call(_ context.Context, operation string, arguments map[string]any) (json.RawMessage, error) {
	p.mu.Lock()
	p.operation, p.lastArgs = operation, arguments
	p.mu.Unlock()
	switch operation {
	case "runtime.prerequisites", "runtime.readiness":
		return json.RawMessage(`{"ready":true,"summary":"Provider is configured."}`), nil
	case "runtime.live_status":
		return json.RawMessage(`{"available":true,"summary":"Provider is online."}`), nil
	case "runtime.verify":
		return json.RawMessage(`{"verified":true,"summary":"Provider verified."}`), nil
	case "runtime.repair":
		return json.RawMessage(`{"repaired":true,"summary":"Provider repaired."}`), nil
	default:
		return nil, workspacesurface.ErrServiceUnavailable
	}
}

func TestPluginRuntimeProviderRoutesDeclaredOperationsThroughTrustedService(t *testing.T) {
	process := &providerFakeProcess{}
	services := workspacesurface.NewServiceManager(func(workspacesurface.ServiceSpec) workspacesurface.ServiceProcess {
		return process
	})
	defer func() { _ = services.Shutdown() }()
	runtimes := runtimecapability.NewRegistry()
	lifecycle := NewSurfaceLifecycle(workspacesurface.NewRegistry(), services)
	lifecycle.SetRuntimeRegistry(runtimes)
	workspaceRoot := t.TempDir()
	pluginDataRoot := t.TempDir()
	lifecycle.SetRuntimeContextResolver(func(_ context.Context, workspaceID string, _ workspacesurface.Owner) (workspacesurface.WorkspaceContext, error) {
		return workspacesurface.WorkspaceContext{
			WorkspaceID: workspaceID, WorkspaceRoot: workspaceRoot, PluginDataRoot: pluginDataRoot,
		}, nil
	})
	installed := installedSurfacePlugin(t, true, true)
	if err := lifecycle.RegisterInstalled(installed); err != nil {
		t.Fatal(err)
	}
	adapter, ok := runtimes.Lookup("plugin:workspace-surface-demo:demo-runtime")
	if !ok {
		t.Fatal("plugin runtime adapter was not registered")
	}
	request := runtimecapability.EvaluationRequest{
		WorkspaceID: "workspace-a",
		Mode:        workspace.RuntimeOperatingMode{ID: "standard", Requires: []string{"demo_runtime"}},
		Requirement: workspace.RuntimeRequirement{Key: "demo_runtime", Adapter: adapter.ID()},
	}
	durable, err := adapter.EvaluateDurable(context.Background(), request)
	if err != nil || durable.State != runtimecapability.DurableConfigured {
		t.Fatalf("durable = %+v, %v", durable, err)
	}
	live, err := adapter.(runtimecapability.LiveChecker).CheckLive(context.Background(), request)
	if err != nil || live.State != runtimecapability.LiveAvailable {
		t.Fatalf("live = %+v, %v", live, err)
	}
	verified, err := adapter.(runtimecapability.Verifier).Verify(context.Background(), runtimecapability.VerificationRequest{EvaluationRequest: request})
	if err != nil || !verified.Succeeded {
		t.Fatalf("verification = %+v, %v", verified, err)
	}
	if err := adapter.(runtimecapability.ActionConfirmer).ConfirmAction(context.Background(), runtimecapability.ConfirmedActionRequest{
		EvaluationRequest: request, ActionToken: "repair",
	}); err != nil {
		t.Fatal(err)
	}
	executionScope, err := adapter.(runtimecapability.ExecutionScopeProvider).ResolveExecutionScope(context.Background(), runtimecapability.ExecutionScopeRequest{
		WorkspaceID: request.WorkspaceID, Mode: request.Mode, Requirement: request.Requirement,
	})
	if err != nil || len(executionScope.AdditionalWritableRoots) != 1 {
		t.Fatalf("execution scope = %+v, %v", executionScope, err)
	}
	process.mu.Lock()
	arguments := process.lastArgs
	process.mu.Unlock()
	contextValue, _ := arguments["context"].(map[string]any)
	if contextValue["workspace_root"] != workspaceRoot || contextValue["plugin_data_root"] != pluginDataRoot {
		t.Fatalf("service context = %+v", contextValue)
	}
	ready, err := adapter.(*pluginRuntimeProvider).CheckPrerequisites(context.Background())
	if err != nil || !ready {
		t.Fatalf("prerequisites: %v %v", ready, err)
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	contextValue, _ = process.lastArgs["context"].(map[string]any)
	scopes, ok := contextValue["scopes"].([]string)
	if process.operation != "runtime.prerequisites" || !ok || len(scopes) != 0 ||
		contextValue["workspace_id"] != "" || contextValue["workspace_root"] != "" || contextValue["project_entry"] != "" || contextValue["plugin_data_root"] != "" {
		t.Fatalf("pre-workspace check selected project, scope or mutation: %s %+v", process.operation, contextValue)
	}
}

func TestPluginRuntimeProviderRejectsUnknownRepairTokenWithoutServiceCall(t *testing.T) {
	provider := &pluginRuntimeProvider{id: "plugin:demo:provider"}
	err := provider.ConfirmAction(context.Background(), runtimecapability.ConfirmedActionRequest{ActionToken: "client-selected"})
	if err != runtimecapability.ErrUnknownAction {
		t.Fatalf("error = %v", err)
	}
}
