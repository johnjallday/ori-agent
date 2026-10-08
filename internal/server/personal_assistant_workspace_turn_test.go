package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type gatedWorkspaceProvider struct {
	capturingChatProvider
	entered     chan struct{}
	release     chan struct{}
	calls       int
	workspaceID string
	tools       bool
	once        sync.Once
}

func (p *gatedWorkspaceProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: p.tools, SupportsSystemPrompt: true}
}
func (p *gatedWorkspaceProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		p.once.Do(func() { close(p.entered) })
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if p.tools {
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "read-owned-task", Name: "home_tasks", Arguments: `{"workspace_id":"` + p.workspaceID + `"}`}}}, nil
		}
	}
	return &llm.ChatResponse{Content: "Fixture scoped reply"}, nil
}

func TestAssistantWorkspaceTurn_ProviderReadsPinnedScopeAndSavesOriginalAttribution(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	provider := &gatedWorkspaceProvider{entered: make(chan struct{}), release: make(chan struct{}), workspaceID: f.project.ID, tools: true}
	f.builder.llmFactory.Register("claude_code", provider)
	for _, prompt := range []string{"Hello", "Translate hello into Korean", "What should I work on next?", "Summarize my workspace activity"} {
		request := f.turn(prompt, f.project)
		delete(request, "folder_context")
		status, route := f.call(t, http.MethodPost, "/api/home-assistant/route", request)
		if status != http.StatusOK || route["route_mode"] != "home_inline" || route["workspace_context"].(map[string]any)["subject"].(map[string]any)["id"] != f.project.ID {
			t.Fatalf("panel route %q: %d %v", prompt, status, route)
		}
	}
	payload := f.turn("What are my current tasks?", f.project)
	delete(payload, "folder_context")
	type result struct {
		status int
		body   map[string]any
	}
	finished := make(chan result, 1)
	go func() {
		status, body := f.call(t, http.MethodPost, "/api/home-assistant/ask", payload)
		finished <- result{status, body}
	}()
	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	// A second, real-host display refresh is navigation, not a retargeting lease.
	status, display := f.call(t, http.MethodPost, "/api/home-assistant/context", map[string]any{"context": map[string]any{"origin": "personal_assistant_panel", "context_version": 1, "workspace_id": f.other.ID, "workspace_slug": f.other.FolderSlug, "page_path": "/workspaces/" + f.other.FolderSlug}})
	if status != http.StatusOK || display["subject"].(map[string]any)["id"] != f.other.ID {
		t.Fatal(status, display)
	}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error {
		ws.Tasks = append(ws.Tasks, workspace.Task{ID: "fresh-task", WorkspaceID: f.project.ID, Description: "FRESH_CURRENT_TASK", Status: workspace.TaskStatusPending})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	var reply result
	select {
	case reply = <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not finish")
	}
	if reply.status != http.StatusOK || reply.body["conversation"].(map[string]any)["stored"] != true {
		t.Fatal("save", reply)
	}
	if reply.body["workspace_context"].(map[string]any)["subject"].(map[string]any)["id"] != f.project.ID {
		t.Fatal("navigation relabeled reply", reply.body)
	}
	if len(provider.requests) != 2 {
		t.Fatal("brokered tool did not run", len(provider.requests))
	}
	for _, req := range provider.requests {
		assertNoPanelExecutionAuthority(t, req)
	}
	toolInput := mustJSON(t, provider.requests[1].Messages)
	if !strings.Contains(toolInput, "FRESH_CURRENT_TASK") || !strings.Contains(toolInput, f.project.ID) {
		t.Fatal("did not re-read current pinned task")
	}
	loaded, err := f.builder.sessionStore.GetSession(context.Background(), f.conversationID)
	if err != nil || loaded.FolderID != f.hqID {
		t.Fatal("ownership moved", err)
	}
	for _, msg := range loaded.Messages[len(loaded.Messages)-2:] {
		if msg.WorkspaceContext == nil || msg.WorkspaceContext.Subject.ID != f.project.ID || !msg.WorkspaceContext.Historical {
			t.Fatal("saved attribution missing", msg)
		}
	}
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if status != http.StatusOK {
		t.Fatal(status, history)
	}
	messages := history["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	if last["workspace_context"].(map[string]any)["subject"].(map[string]any)["id"] != f.project.ID {
		t.Fatal("hydration rewrote old scope", last)
	}
}

func TestAssistantWorkspaceTurn_RevocationDuringModelWritesNothing(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	provider := &gatedWorkspaceProvider{entered: make(chan struct{}), release: make(chan struct{})}
	f.builder.llmFactory.Register("claude_code", provider)
	payload := f.turn("Hello", f.project)
	delete(payload, "folder_context")
	before, err := f.builder.sessionStore.GetMessages(context.Background(), f.conversationID)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan map[string]any, 1)
	go func() { _, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", payload); finished <- reply }()
	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { ws.Status = workspace.StatusTrashed; return nil }); err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	var reply map[string]any
	select {
	case reply = <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not finish")
	}
	if reply["conversation"].(map[string]any)["error"] != "context_save_failed" {
		t.Fatal("revocation saved turn", reply)
	}
	after, err := f.builder.sessionStore.GetMessages(context.Background(), f.conversationID)
	if err != nil || len(after) != len(before) {
		t.Fatal("revocation wrote half-turn", err)
	}
	// A forged page/ID pair is rejected before the provider, not relocated to HQ.
	calls := len(provider.requests)
	payload["context"] = map[string]any{"origin": "personal_assistant_panel", "context_version": 1, "page_path": "/workspaces/" + f.other.FolderSlug, "workspace_id": f.home.ID}
	_, reply = f.call(t, http.MethodPost, "/api/home-assistant/ask", payload)
	if reply["model_unavailable"] != true || len(provider.requests) != calls {
		t.Fatal("forged scope reached provider", reply)
	}
}
