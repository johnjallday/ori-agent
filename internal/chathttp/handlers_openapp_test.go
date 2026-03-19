package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newChatHandlerForOpenAppTests(t *testing.T) *Handler {
	t.Helper()

	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if err := st.CreateAgent(assistantExecutionAgentName, &store.CreateAgentConfig{
		Type: "general",
	}); err != nil {
		t.Fatalf("failed to create assistant agent: %v", err)
	}
	if err := st.CreateAgent("Desktop Launcher", &store.CreateAgentConfig{
		Type: "tool-calling",
	}); err != nil {
		t.Fatalf("failed to create desktop launcher agent: %v", err)
	}
	launcher, ok := st.GetAgent("Desktop Launcher")
	if !ok || launcher == nil {
		t.Fatalf("failed to load desktop launcher agent")
	}
	launcher.Metadata = &types.AgentMetadata{
		Description: "Launch desktop applications on demand.",
		RoutingProfile: &types.AgentRoutingProfile{
			MatchPhrases:    []string{"open safari", "launch safari"},
			ExampleRequests: []string{"open safari"},
			ExternalSystems: []string{"safari", "desktop"},
			SideEffects:     "local_app",
		},
	}
	if err := st.SetAgent("Desktop Launcher", launcher); err != nil {
		t.Fatalf("failed to update desktop launcher agent: %v", err)
	}
	if err := st.CreateAgent("REAPER Assistant", &store.CreateAgentConfig{
		Type: "tool-calling",
	}); err != nil {
		t.Fatalf("failed to create reaper assistant agent: %v", err)
	}
	reaper, ok := st.GetAgent("REAPER Assistant")
	if !ok || reaper == nil {
		t.Fatalf("failed to load reaper assistant agent")
	}
	reaper.Metadata = &types.AgentMetadata{
		Description: "Handle REAPER sessions, projects, and renders.",
		RoutingProfile: &types.AgentRoutingProfile{
			MatchPhrases:    []string{"open my latest reaper project"},
			ExampleRequests: []string{"open my latest reaper project", "render stems from yesterday's session"},
			Domains:         []string{"reaper", "audio", "daw"},
			ExternalSystems: []string{"reaper"},
			SideEffects:     "local_app",
		},
	}
	if err := st.SetAgent("REAPER Assistant", reaper); err != nil {
		t.Fatalf("failed to update reaper assistant agent: %v", err)
	}

	return NewHandler(st, nil)
}

func TestChatHandler_AssistantModeRoutesAppLaunchToSpecialistHandoff(t *testing.T) {
	h := newChatHandlerForOpenAppTests(t)

	original := openApplicationFn
	t.Cleanup(func() { openApplicationFn = original })

	calledWith := ""
	openApplicationFn = func(appName string) error {
		calledWith = appName
		return nil
	}

	body, _ := json.Marshal(map[string]any{
		"question": "open safari",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if calledWith != "" {
		t.Fatalf("expected assistant-mode request to hand off before opening an app, got %q", calledWith)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	responseText, _ := resp["response"].(string)
	if !strings.Contains(strings.ToLower(responseText), "routing it to") {
		t.Fatalf("unexpected response: %q", responseText)
	}
	if mode, _ := resp["route_mode"].(string); mode != routeModeSpecialistFlow {
		t.Fatalf("expected route_mode %q, got %v", routeModeSpecialistFlow, resp["route_mode"])
	}
	if matched, _ := resp["matched_agent"].(string); matched != "Desktop Launcher" {
		t.Fatalf("expected matched_agent Desktop Launcher, got %v", resp["matched_agent"])
	}
}

func TestChatHandler_SpecialistSessionStillAutoRoutesOpenPromptToOpenAppCommand(t *testing.T) {
	h := newChatHandlerForOpenAppTests(t)

	original := openApplicationFn
	t.Cleanup(func() { openApplicationFn = original })

	calledWith := ""
	openApplicationFn = func(appName string) error {
		calledWith = appName
		return nil
	}

	body, _ := json.Marshal(map[string]any{
		"question":   "open safari",
		"agent_name": "Desktop Launcher",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if calledWith != "safari" {
		t.Fatalf("expected openApplication to be called with %q, got %q", "safari", calledWith)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	responseText, _ := resp["response"].(string)
	if !strings.Contains(strings.ToLower(responseText), "opening safari") {
		t.Fatalf("unexpected response: %q", responseText)
	}
}

func TestChatHandler_AssistantModeRoutesReaperPromptToMetadataMatchedSpecialist(t *testing.T) {
	h := newChatHandlerForOpenAppTests(t)

	body, _ := json.Marshal(map[string]any{
		"question": "open my latest reaper project",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if matched, _ := resp["matched_agent"].(string); matched != "REAPER Assistant" {
		t.Fatalf("expected matched_agent REAPER Assistant, got %v", resp["matched_agent"])
	}
	if mode, _ := resp["route_mode"].(string); mode != routeModeSpecialistFlow {
		t.Fatalf("expected route_mode %q, got %v", routeModeSpecialistFlow, resp["route_mode"])
	}
}
