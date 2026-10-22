package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
)

func coreTool(name string) llm.Tool { return llm.Tool{Name: name} }

func TestPruneToolsForLocal_UnderCapUnchanged(t *testing.T) {
	tools := []llm.Tool{coreTool("workspace_notes"), {Name: "web_search"}}
	kept, dropped := pruneToolsForLocal(tools, "anything", localToolCap)
	if len(dropped) != 0 || len(kept) != 2 {
		t.Fatalf("under cap should be unchanged, got kept=%d dropped=%v", len(kept), dropped)
	}
}

func TestPruneToolsForLocal_KeepsCoreAndRanksByRelevance(t *testing.T) {
	tools := []llm.Tool{
		coreTool("workspace_notes"),
		coreTool("workspace_tasks"),
		coreTool("workspace_files"),
		coreTool("workspace_directories"),
		coreTool("workspace_sessions"),
		{Name: "reaper_transport", Description: "control reaper transport and tempo"},
		{Name: "reaper_tracks", Description: "list reaper tracks"},
		{Name: "web_search", Description: "search the web"},
		{Name: "calendar_create", Description: "create a calendar event"},
		{Name: "email_send", Description: "send an email"},
		{Name: "weather", Description: "get the weather"},
		{Name: "air_quality", Description: "get air quality"},
		{Name: "stock_price", Description: "get a stock price"},
		{Name: "translate", Description: "translate text"},
	}
	// 14 tools, cap 12 -> drop 2. Task keywords favor the reaper tools.
	kept, dropped := pruneToolsForLocal(tools, "control the reaper transport and adjust tempo on tracks", localToolCap)

	if len(kept) != localToolCap {
		t.Fatalf("expected %d kept, got %d", localToolCap, len(kept))
	}
	keptNames := map[string]bool{}
	for _, k := range kept {
		keptNames[k.Name] = true
	}
	// Every core tool that was offered survives (a workspace without a linked
	// directory never offers the directory tools, so only offered ones count).
	offered := map[string]bool{}
	for _, tool := range tools {
		offered[tool.Name] = true
	}
	for name := range workspaceCoreToolNames {
		if offered[name] && !keptNames[name] {
			t.Fatalf("core tool %q was dropped", name)
		}
	}
	// The relevant reaper tools survive.
	if !keptNames["reaper_transport"] || !keptNames["reaper_tracks"] {
		t.Fatalf("relevant reaper tools were dropped: kept=%v", keptNames)
	}
	if len(dropped) != 2 {
		t.Fatalf("expected 2 dropped, got %v", dropped)
	}
	// Dropped list is sorted (deterministic).
	for i := 1; i < len(dropped); i++ {
		if dropped[i-1] > dropped[i] {
			t.Fatalf("dropped not sorted: %v", dropped)
		}
	}
}

func TestPruneToolsForLocal_CoreNeverDroppedEvenOverCap(t *testing.T) {
	// 5 core + 10 irrelevant, cap 3: core (5) exceeds cap but is never dropped;
	// all non-core are dropped.
	tools := []llm.Tool{
		coreTool("workspace_notes"), coreTool("workspace_tasks"), coreTool("workspace_files"),
		coreTool("workspace_directories"), coreTool("workspace_sessions"),
	}
	for _, n := range []string{"a_tool", "b_tool", "c_tool"} {
		tools = append(tools, llm.Tool{Name: n})
	}
	kept, dropped := pruneToolsForLocal(tools, "unrelated task", 3)
	if len(kept) != 5 {
		t.Fatalf("all 5 core tools should be kept, got %d", len(kept))
	}
	if len(dropped) != 3 {
		t.Fatalf("all 3 non-core should be dropped, got %v", dropped)
	}
}

func TestIsToolsRejectedError(t *testing.T) {
	cases := map[string]bool{
		"registry.ollama.ai/library/llama2 does not support tools":          true,
		"model does not support tool use":                                   true,
		"tools are not supported by this model":                             true,
		"the model does not support this and does not support tools things": true,
		"connection refused":                                                false,
		"context deadline exceeded":                                         false,
		"":                                                                  false,
	}
	for msg, want := range cases {
		var err error
		if msg != "" {
			err = errors.New(msg)
		}
		if got := isToolsRejectedError(err); got != want {
			t.Fatalf("isToolsRejectedError(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestParseTextToolCall(t *testing.T) {
	// Fenced.
	name, args, ok := parseTextToolCall("```json\n{\"tool_call\": {\"name\": \"workspace_notes\", \"arguments\": {\"id\": \"n1\"}}}\n```")
	if !ok || name != "workspace_notes" {
		t.Fatalf("fenced parse failed: name=%q ok=%v", name, ok)
	}
	if args != `{"id": "n1"}` {
		t.Fatalf("args = %q", args)
	}

	// Surrounded by prose.
	name, _, ok = parseTextToolCall("Sure, let me look: {\"tool_call\": {\"name\": \"web_search\", \"arguments\": {}}} done")
	if !ok || name != "web_search" {
		t.Fatalf("prose parse failed: name=%q ok=%v", name, ok)
	}

	// Missing arguments -> defaults to {}.
	name, args, ok = parseTextToolCall(`{"tool_call": {"name": "weather"}}`)
	if !ok || name != "weather" || args != "{}" {
		t.Fatalf("missing-args parse: name=%q args=%q ok=%v", name, args, ok)
	}

	// Not a tool call.
	if _, _, ok := parseTextToolCall("Here is the final answer, no tools needed."); ok {
		t.Fatal("plain answer should not parse as a tool call")
	}
	// Empty name.
	if _, _, ok := parseTextToolCall(`{"tool_call": {"name": "", "arguments": {}}}`); ok {
		t.Fatal("empty name should not parse")
	}
}

// recordingLocalProvider is a local provider that rejects any request carrying
// tools and answers one without, recording every request.
type recordingLocalProvider struct {
	fakeLocalProvider
	requests []llm.ChatRequest
}

func (p *recordingLocalProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(req.Tools) > 0 {
		return nil, errors.New("registry.ollama.ai/library/llama2 does not support tools")
	}
	return &llm.ChatResponse{Content: "done"}, nil
}

// With the text tool protocol on, a model that rejects tools is retried with
// the protocol instruction added to the system prompt it is sent, not to a
// system-role message.
func TestExecuteTaskConversation_TextToolProtocolExtendsTheSystemPrompt(t *testing.T) {
	t.Setenv("ORI_LOCAL_TEXT_TOOL_PROTOCOL", "true")
	prov := &recordingLocalProvider{fakeLocalProvider: fakeLocalProvider{name: "ollama"}}
	if _, err := (&LLMTaskHandler{}).executeTaskConversation(context.Background(), prov, "ollama", "llama2", 8192,
		agentWithSettings(types.Settings{Model: "llama2"}), "agent-a", Task{WorkspaceID: "ws-x"},
		"sys", []llm.Message{llm.NewUserMessage("do it")}, []llm.Tool{{Name: "workspace_notes"}}); err != nil {
		t.Fatal(err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("expected the rejected request and its tool-less retry, got %d", len(prov.requests))
	}
	if prov.requests[0].SystemPrompt != "sys" {
		t.Fatalf("first request system prompt = %q", prov.requests[0].SystemPrompt)
	}
	if retry := prov.requests[1]; retry.SystemPrompt != "sys"+textToolProtocolInstruction || len(retry.Tools) != 0 {
		t.Fatalf("retry system prompt = %q tools = %d", retry.SystemPrompt, len(retry.Tools))
	}
	for _, request := range prov.requests {
		for _, message := range request.Messages {
			if message.Role == llm.RoleSystem || message.Content != "do it" {
				t.Fatalf("unexpected message %+v", message)
			}
		}
	}
}

func TestLocalTextToolProtocolEnabled(t *testing.T) {
	t.Setenv("ORI_LOCAL_TEXT_TOOL_PROTOCOL", "")
	if localTextToolProtocolEnabled() {
		t.Fatal("unset should be disabled")
	}
	t.Setenv("ORI_LOCAL_TEXT_TOOL_PROTOCOL", "true")
	if !localTextToolProtocolEnabled() {
		t.Fatal("true should be enabled")
	}
	t.Setenv("ORI_LOCAL_TEXT_TOOL_PROTOCOL", "off")
	if localTextToolProtocolEnabled() {
		t.Fatal("off should be disabled")
	}
}
