package workspace

import (
	"context"
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

func (s *stubNativeRegistry) GetToolsForServer(string) ([]toolapi.Tool, error) { return nil, nil }
func (s *stubNativeRegistry) StartServer(string) error                         { return nil }
func (s *stubNativeRegistry) ListServers() []mcp.ServerConfig                  { return s.servers }

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
