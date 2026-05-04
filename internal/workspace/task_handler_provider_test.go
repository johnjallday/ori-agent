package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
)

type providerStub struct {
	name string
}

func (p *providerStub) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *providerStub) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func (p *providerStub) Name() string {
	return p.name
}

func (p *providerStub) Type() llm.ProviderType {
	return llm.ProviderTypeCloud
}

func (p *providerStub) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}

func (p *providerStub) ValidateConfig(_ llm.ProviderConfig) error {
	return nil
}

func (p *providerStub) DefaultModels() []string {
	return nil
}

type scriptedProviderStub struct {
	name      string
	requests  []llm.ChatRequest
	responses []llm.ChatResponse
}

func (p *scriptedProviderStub) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	index := len(p.requests) - 1
	if index >= len(p.responses) {
		return &llm.ChatResponse{}, nil
	}
	resp := p.responses[index]
	return &resp, nil
}

func (p *scriptedProviderStub) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func (p *scriptedProviderStub) Name() string { return p.name }

func (p *scriptedProviderStub) Type() llm.ProviderType { return llm.ProviderTypeCloud }

func (p *scriptedProviderStub) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}

func (p *scriptedProviderStub) ValidateConfig(_ llm.ProviderConfig) error { return nil }

func (p *scriptedProviderStub) DefaultModels() []string { return nil }

func newTestTaskHandler(providerNames ...string) *LLMTaskHandler {
	factory := llm.NewFactory()
	for _, providerName := range providerNames {
		factory.Register(providerName, &providerStub{name: providerName})
	}
	return &LLMTaskHandler{llmFactory: factory}
}

type taskHandlerToolStub struct {
	name string
}

func (t taskHandlerToolStub) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name:        t.name,
		Description: "test tool",
		Parameters:  map[string]interface{}{"type": "object"},
	}
}

func (t taskHandlerToolStub) Call(_ context.Context, _ string) (string, error) {
	return `{"ok":true}`, nil
}

type taskHandlerToolFunc struct {
	name   string
	result string
	calls  int
}

func (t *taskHandlerToolFunc) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name:        t.name,
		Description: "test tool",
		Parameters:  map[string]interface{}{"type": "object"},
	}
}

func (t *taskHandlerToolFunc) Call(_ context.Context, _ string) (string, error) {
	t.calls++
	return t.result, nil
}

type taskUtilityProviderStub struct {
	tools map[string]toolapi.Tool
}

func (p taskUtilityProviderStub) GetTool(name string) (toolapi.Tool, bool) {
	tool, ok := p.tools[name]
	return tool, ok
}

func TestGetProviderForAgent_UsesConfiguredProvider(t *testing.T) {
	handler := newTestTaskHandler("openai", "claude_code")
	got := handler.getProviderForAgent("claude_code", "haiku")
	if got != "claude_code" {
		t.Fatalf("expected claude_code, got %q", got)
	}
}

func TestGetProviderForAgent_CorrectsOpenAIMismatchForHaiku(t *testing.T) {
	handler := newTestTaskHandler("openai", "claude_code")
	got := handler.getProviderForAgent("openai", "haiku")
	if got != "claude_code" {
		t.Fatalf("expected claude_code for openai+haiku mismatch, got %q", got)
	}
}

func TestGetProviderForAgent_KeepsConfiguredProviderWhenInferredProviderUnavailable(t *testing.T) {
	handler := newTestTaskHandler("openai")
	got := handler.getProviderForAgent("openai", "haiku")
	if got != "openai" {
		t.Fatalf("expected openai for openai+haiku when inferred provider is unavailable, got %q", got)
	}
}

func TestGetProviderForAgent_NormalizesAnthropicAlias(t *testing.T) {
	handler := newTestTaskHandler("claude")
	got := handler.getProviderForAgent("anthropic", "claude-3-5-haiku-latest")
	if got != "claude" {
		t.Fatalf("expected claude for anthropic alias, got %q", got)
	}
}

func TestGetProviderForModel_InfersClaudeCodeForShortAlias(t *testing.T) {
	handler := newTestTaskHandler("claude_code")
	got := handler.getProviderForModel("haiku")
	if got != "claude_code" {
		t.Fatalf("expected claude_code, got %q", got)
	}
}

func TestGetProviderForModel_UsesClaudeFamilyForShortAliasWithoutClaudeProviders(t *testing.T) {
	handler := newTestTaskHandler("openai")
	got := handler.getProviderForModel("haiku")
	if got != "claude" {
		t.Fatalf("expected claude for short alias without claude providers, got %q", got)
	}
}

func TestGetProviderForModel_InfersClaudeForClaudeModel(t *testing.T) {
	handler := newTestTaskHandler("claude")
	got := handler.getProviderForModel("claude-3-5-haiku-latest")
	if got != "claude" {
		t.Fatalf("expected claude, got %q", got)
	}
}

func TestNormalizeModelForProvider_ClaudeAliases(t *testing.T) {
	handler := newTestTaskHandler("claude")

	if got := handler.normalizeModelForProvider("claude", "haiku"); got != "claude-3-5-haiku-latest" {
		t.Fatalf("expected claude haiku alias mapping, got %q", got)
	}

	if got := handler.normalizeModelForProvider("claude", "sonnet"); got != "claude-3-5-sonnet-latest" {
		t.Fatalf("expected claude sonnet alias mapping, got %q", got)
	}

	if got := handler.normalizeModelForProvider("claude", "opus"); got != "claude-3-opus-latest" {
		t.Fatalf("expected claude opus alias mapping, got %q", got)
	}
}

func TestConvertAgentToolsToLLMTools_IncludesWorkspaceTools(t *testing.T) {
	handler := newTestTaskHandler("openai")
	handler.SetWorkspaceToolFactory(func(workspaceID string) []toolapi.Tool {
		if workspaceID != "workspace-1" {
			t.Fatalf("expected workspace-1, got %q", workspaceID)
		}
		return []toolapi.Tool{taskHandlerToolStub{name: "workspace_notes"}}
	})

	task := Task{WorkspaceID: "workspace-1"}
	tools := handler.convertAgentToolsToLLMTools(&resolvedTaskAgent{}, task)
	if len(tools) != 1 || tools[0].Name != "workspace_notes" {
		t.Fatalf("expected workspace_notes tool, got %#v", tools)
	}

	tool, ok := handler.findTool(&resolvedTaskAgent{}, task, "workspace_notes")
	if !ok || tool == nil || tool.Definition().Name != "workspace_notes" {
		t.Fatalf("expected findTool to resolve workspace_notes, got ok=%t tool=%#v", ok, tool)
	}
}

func TestConvertAgentToolsToLLMTools_IncludesUtilityWebToolsWhenAllowed(t *testing.T) {
	handler := newTestTaskHandler("openai")
	handler.SetUtilityToolProvider(taskUtilityProviderStub{tools: map[string]toolapi.Tool{
		"time":       taskHandlerToolStub{name: "time"},
		"web_search": taskHandlerToolStub{name: "web_search"},
		"web_fetch":  taskHandlerToolStub{name: "web_fetch"},
		"browser":    taskHandlerToolStub{name: "browser"},
	}})

	ag := &resolvedTaskAgent{Agent: &agent.Agent{}}
	tools := handler.convertAgentToolsToLLMTools(ag, Task{WorkspaceID: "workspace-1"})
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = true
	}

	for _, name := range []string{"time", "web_search", "web_fetch", "browser"} {
		if !got[name] {
			t.Fatalf("expected utility tool %q in %#v", name, tools)
		}
	}

	tool, ok := handler.findTool(ag, Task{WorkspaceID: "workspace-1"}, "web_search")
	if !ok || tool == nil || tool.Definition().Name != "web_search" {
		t.Fatalf("expected findTool to resolve web_search, got ok=%t tool=%#v", ok, tool)
	}
}

func TestConvertAgentToolsToLLMTools_ExcludesUtilityWebToolsWhenDisabled(t *testing.T) {
	handler := newTestTaskHandler("openai")
	handler.SetUtilityToolProvider(taskUtilityProviderStub{tools: map[string]toolapi.Tool{
		"time":       taskHandlerToolStub{name: "time"},
		"weather":    taskHandlerToolStub{name: "weather"},
		"web_search": taskHandlerToolStub{name: "web_search"},
		"web_fetch":  taskHandlerToolStub{name: "web_fetch"},
		"browser":    taskHandlerToolStub{name: "browser"},
	}})

	allowWebSearch := false
	ag := &resolvedTaskAgent{Agent: &agent.Agent{
		Settings: types.Settings{AllowWebSearch: &allowWebSearch},
	}}
	tools := handler.convertAgentToolsToLLMTools(ag, Task{WorkspaceID: "workspace-1"})
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = true
	}

	for _, name := range []string{"time", "weather"} {
		if !got[name] {
			t.Fatalf("expected non-web utility tool %q in %#v", name, tools)
		}
	}
	for _, name := range []string{"web_search", "web_fetch", "browser"} {
		if got[name] {
			t.Fatalf("did not expect web utility tool %q in %#v", name, tools)
		}
	}

	tool, ok := handler.findTool(ag, Task{WorkspaceID: "workspace-1"}, "web_search")
	if ok || tool != nil {
		t.Fatalf("expected findTool not to resolve disabled web_search, got ok=%t tool=%#v", ok, tool)
	}
}

func TestExecuteTask_PublicInfoUsesWebSearchUtilityFallback(t *testing.T) {
	provider := &scriptedProviderStub{
		name: "openai",
		responses: []llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{
						ID:        "search-1",
						Name:      "web_search",
						Arguments: `{"query":"NYC pollen count today"}`,
					},
				},
				FinishReason: llm.FinishReasonToolCalls,
			},
			{
				Content: "NYC pollen is high today. Tree pollen is the main issue.\n\nSource: Pollen.com https://www.pollen.com/forecast/current/pollen/10001",
			},
		},
	}
	factory := llm.NewFactory()
	factory.Register("openai", provider)

	searchTool := &taskHandlerToolFunc{
		name:   "web_search",
		result: `{"results":[{"title":"Pollen.com NYC","url":"https://www.pollen.com/forecast/current/pollen/10001","snippet":"Tree pollen is high in New York City today."}]}`,
	}
	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Ori": {
			Settings: types.Settings{
				Provider:    "openai",
				Model:       "gpt-test",
				Temperature: 0,
			},
		},
	}}
	handler := &LLMTaskHandler{
		agentStore: agentStore,
		llmFactory: factory,
	}
	handler.SetUtilityToolProvider(taskUtilityProviderStub{tools: map[string]toolapi.Tool{
		"web_search": searchTool,
	}})

	result, err := handler.ExecuteTask(context.Background(), "Ori", Task{
		ID:          "task-pollen",
		Description: "check pollen count in NYC",
		To:          "Ori",
	})
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}
	if searchTool.calls != 1 {
		t.Fatalf("expected web_search to be called once, got %d", searchTool.calls)
	}
	if !strings.Contains(result, "NYC pollen is high") || !strings.Contains(result, "https://www.pollen.com/") {
		t.Fatalf("expected sourced pollen result, got %q", result)
	}
	if len(provider.requests) == 0 {
		t.Fatal("expected provider request")
	}
	toolNames := map[string]bool{}
	for _, tool := range provider.requests[0].Tools {
		toolNames[tool.Name] = true
	}
	if !toolNames["web_search"] {
		t.Fatalf("expected first request to include web_search tool, got %#v", provider.requests[0].Tools)
	}
}

func TestExecuteTask_FollowsUpWhenModelReturnsEmptyAfterToolResult(t *testing.T) {
	provider := &scriptedProviderStub{
		name: "openai",
		responses: []llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{
						ID:        "search-1",
						Name:      "web_search",
						Arguments: `{"query":"site:pollen.com New York pollen count"}`,
					},
				},
				FinishReason: llm.FinishReasonToolCalls,
			},
			{},
			{
				Content: "I could not find a current NYC pollen count from that search. The search should be broadened across public allergy sources.",
			},
		},
	}
	factory := llm.NewFactory()
	factory.Register("openai", provider)

	searchTool := &taskHandlerToolFunc{
		name:   "web_search",
		result: `{"query":"site:pollen.com New York pollen count","results":[],"source":"duckduckgo.com"}`,
	}
	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Ori": {
			Settings: types.Settings{
				Provider:    "openai",
				Model:       "gpt-test",
				Temperature: 0,
			},
		},
	}}
	handler := &LLMTaskHandler{
		agentStore: agentStore,
		llmFactory: factory,
	}
	handler.SetUtilityToolProvider(taskUtilityProviderStub{tools: map[string]toolapi.Tool{
		"web_search": searchTool,
	}})

	result, err := handler.ExecuteTask(context.Background(), "Ori", Task{
		ID:          "task-pollen",
		Description: "check pollen count in NYC",
		To:          "Ori",
	})
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(strings.ToLower(result)), "tool results:") {
		t.Fatalf("expected synthesized result, got raw tool output %q", result)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected follow-up request after empty tool result, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[len(provider.requests)-1]
	if len(lastRequest.Messages) == 0 {
		t.Fatal("expected follow-up request messages")
	}
	followup := lastRequest.Messages[len(lastRequest.Messages)-1].Content
	for _, want := range []string{"broaden the search", "Do not return raw Tool Results", "check pollen count in NYC"} {
		if !strings.Contains(followup, want) {
			t.Fatalf("expected follow-up prompt to contain %q, got %q", want, followup)
		}
	}
}

func TestExecuteTask_BlocksRawEmptyWebSearchAfterFollowup(t *testing.T) {
	provider := &scriptedProviderStub{
		name: "openai",
		responses: []llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{
						ID:        "search-1",
						Name:      "web_search",
						Arguments: `{"query":"site:pollen.com New York pollen count"}`,
					},
				},
				FinishReason: llm.FinishReasonToolCalls,
			},
			{},
			{},
		},
	}
	factory := llm.NewFactory()
	factory.Register("openai", provider)

	searchTool := &taskHandlerToolFunc{
		name:   "web_search",
		result: `{"query":"site:pollen.com New York pollen count","results":[],"source":"duckduckgo.com"}`,
	}
	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Ori": {
			Settings: types.Settings{
				Provider:    "openai",
				Model:       "gpt-test",
				Temperature: 0,
			},
		},
	}}
	handler := &LLMTaskHandler{
		agentStore: agentStore,
		llmFactory: factory,
	}
	handler.SetUtilityToolProvider(taskUtilityProviderStub{tools: map[string]toolapi.Tool{
		"web_search": searchTool,
	}})

	result, err := handler.ExecuteTask(context.Background(), "Ori", Task{
		ID:          "task-pollen",
		Description: "check pollen count in NYC",
		To:          "Ori",
	})
	if err == nil {
		t.Fatalf("expected blocked error, got result %q", result)
	}
	if result != "" {
		t.Fatalf("expected empty result when blocked, got %q", result)
	}
	blockedErr, ok := AsTaskBlockedError(err)
	if !ok {
		t.Fatalf("expected TaskBlockedError, got %T %v", err, err)
	}
	if blockedErr.ReasonCode != "empty_web_search_results" {
		t.Fatalf("expected empty_web_search_results, got %q", blockedErr.ReasonCode)
	}
	if !strings.Contains(blockedErr.RawResponse, "Tool Results:") {
		t.Fatalf("expected raw response to preserve tool results, got %q", blockedErr.RawResponse)
	}
}
