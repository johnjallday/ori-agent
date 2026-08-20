package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
)

// fakeNativeProvider is a tool-less native-MCP provider that captures the
// request it receives so tests can assert the wiring.
type fakeNativeProvider struct {
	caps   llm.ProviderCapabilities
	gotReq llm.ChatRequest
	called bool
}

func (p *fakeNativeProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.gotReq = req
	p.called = true
	return &llm.ChatResponse{Content: "done"}, nil
}
func (p *fakeNativeProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}
func (p *fakeNativeProvider) Name() string                            { return "codex" }
func (p *fakeNativeProvider) Type() llm.ProviderType                  { return llm.ProviderTypeCloud }
func (p *fakeNativeProvider) Capabilities() llm.ProviderCapabilities  { return p.caps }
func (p *fakeNativeProvider) ValidateConfig(llm.ProviderConfig) error { return nil }
func (p *fakeNativeProvider) DefaultModels() []string                 { return nil }

type stubNativeRegistry struct{ servers []mcp.ServerConfig }

type stubExecutionScopeResolver struct {
	scope        *llm.CLIExecutionScope
	err          error
	calls        int
	workspaceID  string
	agentID      string
	capabilities []string
	workspaceDir string
}

func (s *stubExecutionScopeResolver) ResolveTaskExecutionScope(_ context.Context, workspaceID, agentID string, capabilities []string, workspaceDir string) (*llm.CLIExecutionScope, error) {
	s.calls++
	s.workspaceID = workspaceID
	s.agentID = agentID
	s.capabilities = append([]string(nil), capabilities...)
	s.workspaceDir = workspaceDir
	return llm.CloneCLIExecutionScope(s.scope), s.err
}

func (s *stubNativeRegistry) GetToolsForServer(string) ([]toolapi.Tool, error) { return nil, nil }
func (s *stubNativeRegistry) StartServer(string) error                         { return nil }
func (s *stubNativeRegistry) ListServers() []mcp.ServerConfig                  { return s.servers }

func TestRuntimeTaskToolsRequireDeclaredCapabilityAndExactAgentInstance(t *testing.T) {
	store := NewInMemoryStore()
	ws := &Workspace{
		ID: "ws-reaper-tools", Name: "REAPER", Status: StatusActive,
		AgentInstances: []AgentInstance{{ID: "agent-1", Name: "reaper", NodeID: "reaper-1"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	handler := &LLMTaskHandler{workspaceStore: store}
	var gotWorkspace, gotAgent string
	handler.SetRuntimeTaskToolFactory(func(task Task, _ string, agentInstanceID string) []toolapi.Tool {
		if !task.RequiresCapability("reaper_live_control") {
			return nil
		}
		gotWorkspace = task.WorkspaceID
		gotAgent = agentInstanceID
		return []toolapi.Tool{taskHandlerToolStub{name: "list_reaper_actions"}}
	})

	unrelated := Task{WorkspaceID: ws.ID, To: "reaper", AssignedNodeID: "reaper-1"}
	if tools := handler.getWorkspaceTools(unrelated); len(tools) != 0 {
		t.Fatalf("unrelated task received runtime tools: %v", tools)
	}
	// Model arguments never provide the workspace or stable agent ID handed to
	// the production runtime-tool factory.
	runtimeTask := Task{
		WorkspaceID: ws.ID, To: "reaper", AssignedNodeID: "reaper-1",
		RequiredCapabilities: []string{"reaper_live_control"},
	}
	tools := handler.getWorkspaceTools(runtimeTask)
	if len(tools) != 1 || gotWorkspace != ws.ID || gotAgent != "agent-1" {
		t.Fatalf("runtime tool scope = tools=%v workspace=%q agent=%q", tools, gotWorkspace, gotAgent)
	}
	wrongInstance := runtimeTask
	wrongInstance.To = "other-agent"
	wrongInstance.AssignedNodeID = "other-node"
	if tools := handler.getWorkspaceTools(wrongInstance); len(tools) != 0 {
		t.Fatalf("unknown instance received runtime tools: %v", tools)
	}
}

func TestExecuteTaskConversation_RuntimeScopeDoesNotRequireBroadNativeMCP(t *testing.T) {
	store := NewInMemoryStore()
	ws := &Workspace{
		ID: "ws-runtime-scope", Name: "Runtime", Status: StatusActive,
		AllowNativeMCPCLI: false,
		AgentInstances:    []AgentInstance{{ID: "agent-1", Name: "reaper", NodeID: "reaper-1"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	reaperRuntime := "ws:ws-runtime-scope:mcp:reaper-plugin/ori-reaper:b1"
	otherRuntime := "ws:ws-runtime-scope:mcp:other-plugin:b2"
	registry := &stubNativeRegistry{servers: []mcp.ServerConfig{
		{Name: reaperRuntime, Command: "/trusted/reaper-helper"},
		{Name: otherRuntime, Command: "/unrelated/helper"},
	}}
	scopeResolver := &stubExecutionScopeResolver{scope: &llm.CLIExecutionScope{
		WorkspaceRoot: "/workspace/files", AdditionalWritableRoots: []string{"/trusted/runner"},
		NetworkPosture: llm.CLINetworkCapabilityLocal, CapabilityKeys: []string{"reaper_live_control"},
		AllowedMCPServers: []string{"reaper-plugin_ori-reaper"},
	}}
	handler := &LLMTaskHandler{workspaceStore: store, mcpRegistry: registry, executionScopeResolver: scopeResolver}
	provider := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: true}}
	agent := nativeMCPAgent(false) // broad per-agent native MCP is OFF
	agent.MCPServers = []string{reaperRuntime, otherRuntime}
	task := Task{
		WorkspaceID: "ws-runtime-scope", AssignedNodeID: "reaper-1",
		RequiredCapabilities: []string{"reaper_live_control"},
	}
	if _, err := handler.executeTaskConversation(context.Background(), provider, "codex", "gpt-5.5", 0, agent, "reaper", task, []llm.Message{llm.NewUserMessage("x")}, nil); err != nil {
		t.Fatal(err)
	}
	if !provider.called || provider.gotReq.ExecutionScope == nil {
		t.Fatalf("provider did not receive scoped authority: %+v", provider.gotReq)
	}
	if provider.gotReq.WorkspaceDir != "" {
		t.Fatalf("scoped run inherited legacy broad WorkspaceDir: %q", provider.gotReq.WorkspaceDir)
	}
	if len(provider.gotReq.MCPServers) != 1 || provider.gotReq.MCPServers[0].Name != "reaper-plugin_ori-reaper" {
		t.Fatalf("scoped run exposed unrelated MCP servers: %+v", provider.gotReq.MCPServers)
	}
	if scopeResolver.calls != 1 || scopeResolver.workspaceID != ws.ID || scopeResolver.agentID != "agent-1" || len(scopeResolver.capabilities) != 1 {
		t.Fatalf("scope resolver inputs = %+v", scopeResolver)
	}
}

func TestExecuteTaskConversation_FileFallbackUsesOnlyConfinedRoot(t *testing.T) {
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "song.rpp"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewInMemoryStore()
	if err := store.Save(&Workspace{ID: "ws-fallback", Status: StatusActive, AllowNativeMCPCLI: true}); err != nil {
		t.Fatal(err)
	}
	resolver := &stubExecutionScopeResolver{scope: &llm.CLIExecutionScope{WorkspaceRoot: "/must-not-be-used"}}
	handler := &LLMTaskHandler{workspaceStore: store, executionScopeResolver: resolver}
	provider := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: true}}
	task := Task{
		WorkspaceID:      "ws-fallback",
		RuntimeExecution: &TaskRuntimeExecution{WorkspaceRoot: stage, DisableTools: true, FileOnly: true, Filename: "song.rpp"},
	}
	if _, err := handler.executeTaskConversation(context.Background(), provider, "codex", "model", 0, nativeMCPAgent(true), "reaper", task, []llm.Message{llm.NewUserMessage("edit the file")}, []llm.Tool{{Name: "unrelated"}}); err != nil {
		t.Fatal(err)
	}
	if !provider.called || provider.gotReq.ExecutionScope == nil || provider.gotReq.ExecutionScope.WorkspaceRoot != stage || provider.gotReq.ExecutionScope.NetworkPosture != llm.CLINetworkDisabled {
		t.Fatalf("fallback scope = %+v", provider.gotReq.ExecutionScope)
	}
	if len(provider.gotReq.Tools) != 0 || len(provider.gotReq.MCPServers) != 0 || provider.gotReq.WorkspaceDir != "" || resolver.calls != 0 {
		t.Fatalf("fallback inherited tools or broader scope: tools=%v mcp=%v dir=%q resolver=%d", provider.gotReq.Tools, provider.gotReq.MCPServers, provider.gotReq.WorkspaceDir, resolver.calls)
	}
}

func TestExecuteTaskConversation_RuntimeScopeNeverReachesNonCLIProviderOrUnrelatedTask(t *testing.T) {
	store := NewInMemoryStore()
	ws := &Workspace{ID: "ws-no-scope", Status: StatusActive, AgentInstances: []AgentInstance{{ID: "agent-1", Name: "worker", NodeID: "worker-1"}}}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	resolver := &stubExecutionScopeResolver{scope: &llm.CLIExecutionScope{WorkspaceRoot: "/workspace", CapabilityKeys: []string{"runtime"}}}
	handler := &LLMTaskHandler{workspaceStore: store, executionScopeResolver: resolver}
	agent := nativeMCPAgent(false)

	nonCLI := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: false}}
	if _, err := handler.executeTaskConversation(context.Background(), nonCLI, "openai", "model", 0, agent, "worker", Task{WorkspaceID: ws.ID, AssignedNodeID: "worker-1", RequiredCapabilities: []string{"runtime"}}, []llm.Message{llm.NewUserMessage("x")}, nil); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 || nonCLI.gotReq.ExecutionScope != nil {
		t.Fatalf("non-CLI provider received runtime scope: calls=%d scope=%+v", resolver.calls, nonCLI.gotReq.ExecutionScope)
	}

	cli := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: true}}
	if _, err := handler.executeTaskConversation(context.Background(), cli, "codex", "model", 0, agent, "worker", Task{WorkspaceID: ws.ID, AssignedNodeID: "worker-1"}, []llm.Message{llm.NewUserMessage("x")}, nil); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 || cli.gotReq.ExecutionScope != nil {
		t.Fatalf("unrelated task received runtime scope: calls=%d scope=%+v", resolver.calls, cli.gotReq.ExecutionScope)
	}
}

// TestExecuteTaskConversation_NativeMCPWiring proves the end-to-end path:
// gate (workspace+agent opted in) -> resolve runtime servers -> populate the
// request handed to the native-MCP provider, with the CLI-safe alias.
func TestExecuteTaskConversation_NativeMCPWiring(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(&Workspace{ID: "ws-x", Name: "X", Status: StatusActive, AllowNativeMCPCLI: true}); err != nil {
		t.Fatalf("save: %v", err)
	}
	runtime := "ws:ws-x:mcp:reaper-plugin/ori-reaper:b1"
	reg := &stubNativeRegistry{servers: []mcp.ServerConfig{{
		Name:    runtime,
		Command: "/abs/reaper-plugin",
		Args:    []string{"--stdio"},
		Env:     map[string]string{"REAPER_WEB_REMOTE_PORT": "2307"},
	}}}
	h := &LLMTaskHandler{workspaceStore: store, mcpRegistry: reg}

	prov := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: true}}
	ag := nativeMCPAgent(true)
	ag.MCPServers = []string{runtime}

	out, err := h.executeTaskConversation(context.Background(), prov, "codex", "gpt-5.5", 0, ag, "reaper",
		Task{WorkspaceID: "ws-x"}, []llm.Message{llm.NewUserMessage("create project")}, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != "done" || !prov.called {
		t.Fatalf("provider not exercised: out=%q called=%v", out, prov.called)
	}
	if len(prov.gotReq.MCPServers) != 1 {
		t.Fatalf("MCPServers=%+v, want 1 (gate+resolve should populate it)", prov.gotReq.MCPServers)
	}
	spec := prov.gotReq.MCPServers[0]
	if spec.Name != "reaper-plugin_ori-reaper" || spec.Command != "/abs/reaper-plugin" {
		t.Errorf("resolved spec wrong: %+v", spec)
	}
	if spec.Env["REAPER_WEB_REMOTE_PORT"] != "2307" {
		t.Errorf("env not forwarded: %+v", spec.Env)
	}
	if prov.gotReq.WorkspaceID != "ws-x" {
		t.Errorf("WorkspaceID=%q, want ws-x", prov.gotReq.WorkspaceID)
	}
}

// TestExecuteTaskConversation_NativeMCPGatedOff confirms that with the agent
// opted out (even though the workspace is opted in), the provider receives no
// MCP servers and runs text-only.
func TestExecuteTaskConversation_NativeMCPGatedOff(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(&Workspace{ID: "ws-y", Status: StatusActive, AllowNativeMCPCLI: true}); err != nil {
		t.Fatalf("save: %v", err)
	}
	runtime := "ws:ws-y:mcp:reaper-plugin/ori-reaper:b1"
	reg := &stubNativeRegistry{servers: []mcp.ServerConfig{{Name: runtime, Command: "/abs/x"}}}
	h := &LLMTaskHandler{workspaceStore: store, mcpRegistry: reg}

	prov := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: true}}
	ag := nativeMCPAgent(false) // agent NOT opted in
	ag.MCPServers = []string{runtime}

	if _, err := h.executeTaskConversation(context.Background(), prov, "codex", "gpt-5.5", 0, ag, "reaper",
		Task{WorkspaceID: "ws-y"}, []llm.Message{llm.NewUserMessage("x")}, nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(prov.gotReq.MCPServers) != 0 {
		t.Errorf("gate off must hand no MCP servers, got %+v", prov.gotReq.MCPServers)
	}
}

// TestExecuteTaskConversation_SkillOnlyElevated confirms a skill-only agent
// (opted in, NO MCP servers bound) still gets the elevated posture: WorkspaceID/
// WorkspaceDir are set so the provider runs workspace-write + network, even
// though no MCP servers are handed over.
func TestExecuteTaskConversation_SkillOnlyElevated(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(&Workspace{ID: "ws-s", Status: StatusActive, AllowNativeMCPCLI: true}); err != nil {
		t.Fatalf("save: %v", err)
	}
	reg := &stubNativeRegistry{} // no MCP servers registered
	h := &LLMTaskHandler{workspaceStore: store, mcpRegistry: reg}

	prov := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: true}}
	ag := nativeMCPAgent(true) // opted in
	ag.MCPServers = nil        // skill-only: no MCP servers bound

	if _, err := h.executeTaskConversation(context.Background(), prov, "codex", "gpt-5.5", 0, ag, "reaper",
		Task{WorkspaceID: "ws-s"}, []llm.Message{llm.NewUserMessage("x")}, nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if prov.gotReq.WorkspaceID != "ws-s" {
		t.Errorf("elevated posture must set WorkspaceID even with no MCP servers, got %q", prov.gotReq.WorkspaceID)
	}
	if len(prov.gotReq.MCPServers) != 0 {
		t.Errorf("skill-only run must carry no MCP servers, got %+v", prov.gotReq.MCPServers)
	}
}

func TestEffectiveNativeMCPExecTimeout(t *testing.T) {
	h := &LLMTaskHandler{}
	if got := h.effectiveNativeMCPExecTimeout(); got != defaultNativeMCPExecTimeout {
		t.Errorf("unset default = %v, want %v", got, defaultNativeMCPExecTimeout)
	}
	h.SetNativeMCPExecTimeout(45 * time.Second)
	if got := h.effectiveNativeMCPExecTimeout(); got != 45*time.Second {
		t.Errorf("override = %v, want 45s", got)
	}
	h.SetNativeMCPExecTimeout(0) // non-positive restores the default
	if got := h.effectiveNativeMCPExecTimeout(); got != defaultNativeMCPExecTimeout {
		t.Errorf("reset = %v, want default", got)
	}
}

func nativeMCPAgent(allow bool) *resolvedTaskAgent {
	return &resolvedTaskAgent{
		Agent: &agent.Agent{
			Settings: types.Settings{AllowNativeMCPTools: &allow},
		},
	}
}

func TestNativeMCPGateAllowed(t *testing.T) {
	cases := []struct {
		name      string
		wsAllow   bool
		nilWS     bool
		agentNil  bool
		agentAllo bool
		want      bool
	}{
		{name: "both opted in", wsAllow: true, agentAllo: true, want: true},
		{name: "workspace off", wsAllow: false, agentAllo: true, want: false},
		{name: "agent off", wsAllow: true, agentAllo: false, want: false},
		{name: "both off", wsAllow: false, agentAllo: false, want: false},
		{name: "nil workspace", nilWS: true, agentAllo: true, want: false},
		{name: "nil agent", wsAllow: true, agentNil: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ws *Workspace
			if !tc.nilWS {
				ws = &Workspace{AllowNativeMCPCLI: tc.wsAllow}
			}
			var ag *resolvedTaskAgent
			if !tc.agentNil {
				ag = nativeMCPAgent(tc.agentAllo)
			}
			if got := nativeMCPGateAllowed(ws, ag); got != tc.want {
				t.Errorf("nativeMCPGateAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsNativeMCPToolsAllowed(t *testing.T) {
	if (types.Settings{}).IsNativeMCPToolsAllowed() {
		t.Error("default (nil) must be false")
	}
	f := false
	if (types.Settings{AllowNativeMCPTools: &f}).IsNativeMCPToolsAllowed() {
		t.Error("explicit false must be false")
	}
	tr := true
	if !(types.Settings{AllowNativeMCPTools: &tr}).IsNativeMCPToolsAllowed() {
		t.Error("explicit true must be true")
	}
}
