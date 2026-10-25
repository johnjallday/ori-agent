package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/modelview"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Any attempt to use the runtime boundary for inspection fails immediately.
type inspectionOnlyRuntime struct {
	*workspace.AgentRuntimeResolver
}

func (*inspectionOnlyRuntime) ResolveAgentForWorkspace(string, string, string) (*workspace.ResolvedAgentRuntime, error) {
	panic("metadata must not resolve runtime tools")
}

type modelSessionStore struct {
	session.HybridStore
	items map[string]*session.Session
}

func (s *modelSessionStore) GetSession(_ context.Context, id string) (*session.Session, error) {
	if item := s.items[id]; item != nil {
		return item, nil
	}
	return nil, session.ErrSessionNotFound
}

func chatModel(t *testing.T, h *Handler, query string) (int, modelview.Selection) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ModelHandler(w, httptest.NewRequest(http.MethodGet, "/api/chat/model"+query, nil))
	var got modelview.Selection
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable projection")
	}
	var fields map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		switch key {
		case "recipient", "studio_id", "session_id", "provider", "model", "source", "state", "settings_href", "error", "code", "success", "message":
		default:
			t.Fatalf("unexpected/private field %q", key)
		}
	}
	return w.Code, got
}

func TestChatModel_WorkspaceCopyAgreesWithChatWithoutRuntimeSideEffects(t *testing.T) {
	for _, providerName := range []string{"claude_code", "claude"} {
		t.Run(providerName, func(t *testing.T) {
			provider := &scriptedChatProvider{name: providerName, responses: []llm.ChatResponse{{Content: "Hello"}}}
			h, tools := newCLIChatTestHandler(t, providerName, "global-model", provider)
			ws := workspace.NewInMemoryStore()
			if err := ws.Save(&workspace.Workspace{ID: "project", Name: "Project", AgentInstances: []workspace.AgentInstance{{Name: "cli-agent", EntryPoint: true}}}); err != nil {
				t.Fatal(err)
			}
			configuredProvider := providerName
			if providerName == "claude" {
				configuredProvider = "anthropic"
			}
			local := &agent.Agent{Settings: types.Settings{Provider: configuredProvider, Model: "sonnet", SystemPrompt: "private prompt", APIKey: "private-key"}}
			if err := ws.SaveWorkspaceAgent("project", "cli-agent", local); err != nil {
				t.Fatal(err)
			}
			h.SetWorkspaceStore(ws)
			runtime := workspace.NewAgentRuntimeResolver(h.store, ws, nil, nil)
			h.SetRuntimeResolver(&inspectionOnlyRuntime{runtime})
			before, _ := json.Marshal(local)
			code, view := chatModel(t, h, "?studio_id=project")
			if code != 200 || view.State != "configured" || view.Provider != providerName || view.Model != "sonnet" || view.SettingsHref != "/workspaces/project?editAgentModel=cli-agent" {
				t.Fatalf("projection %d %+v", code, view)
			}
			after, _ := json.Marshal(local)
			if !bytes.Equal(before, after) || len(provider.requests) != 0 || len(*tools) != 0 {
				t.Fatal("inspection mutated an agent or invoked work")
			}
			h.SetRuntimeResolver(runtime)
			body := []byte(`{"question":"Say hello.","route_context":{"workspace_id":"project","surface":"workspace_detail"}}`)
			w := httptest.NewRecorder()
			h.ChatHandler(w, httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body)))
			if len(provider.requests) != 1 || provider.requests[0].Model != view.Model {
				t.Fatalf("chat disagrees: requests=%+v response=%s", provider.requests, w.Body.String())
			}
			global, _ := h.store.GetAgent("cli-agent")
			if global.Settings.Model != "global-model" {
				t.Fatal("read or chat overwrote the global definition")
			}
		})
	}
}

func TestChatModel_SessionBindingScopeAndUnavailableStates(t *testing.T) {
	for _, tc := range []struct {
		name, query, provider, model string
		disabled                     bool
		code                         int
		state, recipient             string
	}{
		{"entry", "?studio_id=project", "claude_code", "sonnet", false, 200, "configured", "entry"},
		{"session wins", "?studio_id=project&session_id=mine&agent_name=entry", "claude_code", "sonnet", false, 200, "configured", "pinned"},
		{"foreign session", "?studio_id=project&session_id=foreign", "claude_code", "sonnet", false, 404, "", ""},
		{"missing session", "?studio_id=project&session_id=missing", "claude_code", "sonnet", false, 404, "", ""},
		{"missing workspace", "?studio_id=missing", "claude_code", "sonnet", false, 404, "", ""},
		{"missing context", "", "claude_code", "sonnet", false, 400, "", ""},
		{"disabled", "?studio_id=project", "claude_code", "sonnet", true, 200, "unavailable", "entry"},
		{"empty model", "?studio_id=project", "claude_code", "", false, 200, "unconfigured", "entry"},
		{"unregistered provider", "?studio_id=project", "gemini", "model", false, 200, "unavailable", "entry"},
		{"unknown provider", "?studio_id=project", "invented", "model", false, 200, "unconfigured", "entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag := &agent.Agent{Settings: types.Settings{Provider: tc.provider, Model: tc.model}}
			if tc.disabled {
				ag.Status = types.AgentStatusDisabled
			}
			st := &preflightStore{agents: map[string]*agent.Agent{"entry": ag, "pinned": {Settings: types.Settings{Provider: "claude_code", Model: "pinned-model"}}}}
			h := NewHandler(st, nil)
			ws := workspace.NewInMemoryStore()
			if err := ws.Save(&workspace.Workspace{ID: "project", Name: "Project", AgentInstances: []workspace.AgentInstance{{Name: "entry", EntryPoint: true}}}); err != nil {
				t.Fatal(err)
			}
			h.SetWorkspaceStore(ws)
			h.SetRuntimeResolver(&inspectionOnlyRuntime{workspace.NewAgentRuntimeResolver(st, ws, nil, nil)})
			provider := &scriptedChatProvider{name: "claude_code"}
			factory := llm.NewFactory()
			factory.Register("claude_code", provider)
			h.SetLLMFactory(factory)
			sessions := &modelSessionStore{items: map[string]*session.Session{
				"mine":    {ID: "mine", FolderID: "project", AgentName: "pinned"},
				"foreign": {ID: "foreign", FolderID: "other", AgentName: "pinned"},
			}}
			h.SetSessionStore(sessions)
			before, _ := json.Marshal(sessions.items)
			code, view := chatModel(t, h, tc.query)
			if code != tc.code || view.State != tc.state || view.Recipient != tc.recipient {
				t.Fatalf("projection %d %+v", code, view)
			}
			if view.State != "configured" && (view.Model != "" || view.Provider != "") {
				t.Fatal("stale selection advertised")
			}
			if view.Recipient == "pinned" && view.Model != "pinned-model" {
				t.Fatal("session binding ignored")
			}
			after, _ := json.Marshal(sessions.items)
			if !reflect.DeepEqual(before, after) || len(provider.requests) != 0 {
				t.Fatal("inspection mutated sessions or ran a model")
			}
		})
	}
}
