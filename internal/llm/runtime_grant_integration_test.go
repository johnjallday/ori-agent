package llm_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type grantIntegrationStore struct{ *workspace.InMemoryStore }

func (s *grantIntegrationStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	return s.Get(id)
}

type grantIntegrationAdapter struct{ runner string }

func (a grantIntegrationAdapter) ID() string { return "reaper_live_control" }
func (a grantIntegrationAdapter) EvaluateDurable(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	return runtimecapability.DurableResult{State: runtimecapability.DurableConfigured}, nil
}
func (a grantIntegrationAdapter) ValidateGrant(context.Context, runtimecapability.GrantValidationRequest) error {
	return nil
}
func (a grantIntegrationAdapter) ResolveExecutionScope(context.Context, runtimecapability.ExecutionScopeRequest) (runtimecapability.CapabilityExecutionScope, error) {
	return runtimecapability.CapabilityExecutionScope{
		AdditionalWritableRoots: []string{a.runner},
		NetworkPosture:          runtimecapability.CapabilityNetworkLocal,
	}, nil
}

// TestRuntimeGrantProviderDemo is the opt-in Delivery PR 1 demo: one canonical
// workspace grant is applied to Codex and Claude Code one at a time, real writes
// prove workspace/runner access and outside denial, then revocation proves no
// subsequent provider scope can be constructed.
func TestRuntimeGrantProviderDemo(t *testing.T) {
	if os.Getenv("ORI_RUN_CLI_SCOPE_TESTS") != "1" {
		t.Skip("set ORI_RUN_CLI_SCOPE_TESTS=1 for authenticated provider demo")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("provider scope demo is macOS-specific")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(home, ".ori-runtime-grant-demo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	workspaceRoot := filepath.Join(root, "workspace")
	runnerRoot := filepath.Join(root, "runner")
	outsideRoot := filepath.Join(root, "outside")
	for _, dir := range []string{workspaceRoot, runnerRoot, outsideRoot} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	store := &grantIntegrationStore{InMemoryStore: workspace.NewInMemoryStore()}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Provider Grant Demo"})
	ws.ID = "ws-provider-grant-demo"
	ws.AgentInstances = []workspace.AgentInstance{
		{ID: "codex-agent", Name: "Codex Producer"},
		{ID: "claude-agent", Name: "Claude Producer"},
	}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "scope-demo",
		RuntimeRequirements: &workspace.RuntimeRequirementsContract{
			SchemaVersion:  workspace.RuntimeRequirementsSchemaVersion,
			OperatingModes: []workspace.RuntimeOperatingMode{{ID: "assisted", Label: "Assisted", Description: "Use runtime.", Requires: []string{"reaper_live_control"}}},
			Requirements:   []workspace.RuntimeRequirement{{Key: "reaper_live_control", Label: "REAPER live control", Description: "Scoped provider demo.", Adapter: "reaper_live_control"}},
		},
	})
	ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	registry := runtimecapability.NewRegistry()
	if err := registry.Register(grantIntegrationAdapter{runner: runnerRoot}); err != nil {
		t.Fatal(err)
	}
	service := runtimecapability.NewService(store, registry)

	providers := []struct {
		name    string
		agentID string
		build   func() (llm.Provider, error)
		model   string
	}{
		{name: "codex", agentID: "codex-agent", build: func() (llm.Provider, error) { return llm.NewCodexProvider() }},
		{name: "claude", agentID: "claude-agent", build: func() (llm.Provider, error) { return llm.NewClaudeCodeProvider() }, model: "haiku"},
	}
	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := tc.build()
			if err != nil {
				t.Skip(err)
			}
			if _, err := service.Grant(context.Background(), ws.ID, "reaper_live_control", tc.agentID); err != nil {
				t.Fatal(err)
			}
			scope, err := service.ResolveTaskExecutionScope(context.Background(), ws.ID, tc.agentID, []string{"reaper_live_control"}, workspaceRoot)
			if err != nil || scope == nil {
				t.Fatalf("resolve granted scope: %+v, %v", scope, err)
			}
			workspaceFile := filepath.Join(workspaceRoot, tc.name+"-workspace.txt")
			runnerFile := filepath.Join(runnerRoot, tc.name+"-runner.txt")
			outsideFile := filepath.Join(outsideRoot, tc.name+"-outside.txt")
			prompt := fmt.Sprintf("Attempt all three writes with available tools, continuing after denial: write WORKSPACE_OK to %s; write RUNNER_OK to %s; attempt OUTSIDE_BAD to %s. Report briefly.", workspaceFile, runnerFile, outsideFile)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if _, err := provider.Chat(ctx, llm.ChatRequest{
				Model: tc.model, Messages: []llm.Message{llm.NewUserMessage(prompt)},
				WorkspaceID: ws.ID, ExecutionScope: scope,
			}); err != nil {
				t.Fatalf("provider run: %v", err)
			}
			assertGrantDemoFile(t, workspaceFile, "WORKSPACE_OK")
			assertGrantDemoFile(t, runnerFile, "RUNNER_OK")
			if _, err := os.Lstat(outsideFile); !os.IsNotExist(err) {
				t.Fatalf("outside write succeeded (err=%v)", err)
			}

			if _, err := service.Revoke(context.Background(), ws.ID, "reaper_live_control", tc.agentID); err != nil {
				t.Fatal(err)
			}
			if scope, err := service.ResolveTaskExecutionScope(context.Background(), ws.ID, tc.agentID, []string{"reaper_live_control"}, workspaceRoot); !errors.Is(err, runtimecapability.ErrExecutionScopeUnavailable) || scope != nil {
				t.Fatalf("revoked scope = %+v, %v", scope, err)
			}
			t.Log("grant allowed workspace+runner, denied outside, and revoke removed subsequent scope")
		})
	}
}

func assertGrantDemoFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-owned fixture path
	if err != nil || strings.TrimSpace(string(data)) != want {
		t.Fatalf("allowed sentinel %s = %q, %v", filepath.Base(path), data, err)
	}
}
