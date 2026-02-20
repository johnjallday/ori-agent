package agenthttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newHomeRouteTestStore(t *testing.T) store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func addHomeRouteTestAgent(t *testing.T, st store.Store, name string, cfg *store.CreateAgentConfig, status types.AgentStatus, description string, tags []string, plugins []string) {
	t.Helper()

	if cfg == nil {
		cfg = &store.CreateAgentConfig{Type: "general"}
	}
	if err := st.CreateAgent(name, cfg); err != nil {
		t.Fatalf("failed to create agent %q: %v", name, err)
	}

	ag, ok := st.GetAgent(name)
	if !ok || ag == nil {
		t.Fatalf("agent %q not found after creation", name)
	}

	ag.Status = status
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	ag.Metadata.Description = description
	ag.Metadata.Tags = tags
	ag.Metadata.Favorite = false

	if ag.Plugins == nil {
		ag.Plugins = map[string]types.LoadedPlugin{}
	}
	for _, plugin := range plugins {
		ag.Plugins[plugin] = types.LoadedPlugin{}
	}

	if err := st.SetAgent(name, ag); err != nil {
		t.Fatalf("failed to persist agent %q: %v", name, err)
	}
}

func setHomeRouteAgentMCPServers(t *testing.T, st store.Store, name string, servers []string) {
	t.Helper()

	ag, ok := st.GetAgent(name)
	if !ok || ag == nil {
		t.Fatalf("agent %q not found", name)
	}

	ag.MCPServers = append([]string{}, servers...)
	if err := st.SetAgent(name, ag); err != nil {
		t.Fatalf("failed to persist MCP servers for %q: %v", name, err)
	}
}

func postRouteRequest(t *testing.T, handler *HomeAssistantRouteHandler, payload map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/home-assistant/route", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.RouteHandler(rr, req)
	return rr
}

func TestHomeAssistantRouteHandler_MethodNotAllowed(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	req := httptest.NewRequest(http.MethodGet, "/api/home-assistant/route", nil)
	rr := httptest.NewRecorder()
	handler.RouteHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestHomeAssistantRouteHandler_EmptyPrompt(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "   "})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHomeAssistantRouteHandler_TravelMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"}, types.AgentStatusActive,
		"Plans multi-day travel itineraries", []string{"travel", "itinerary"}, []string{"weather-tool", "web-search"})
	addHomeRouteTestAgent(t, st, "Code Helper", &store.CreateAgentConfig{Type: "general"}, types.AgentStatusActive,
		"Helps with code review", []string{"coding"}, []string{"git"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Plan my 3 day trip in LA"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "travel_planning" {
		t.Fatalf("expected intent travel_planning, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false, got true")
	}
	if resp.MatchedAgent != "Travel Planner" {
		t.Fatalf("expected matched agent Travel Planner, got %q", resp.MatchedAgent)
	}
	if resp.Score < 4 {
		t.Fatalf("expected score >= 4, got %d", resp.Score)
	}
	if len(resp.Reasons) == 0 {
		t.Fatalf("expected non-empty reasons")
	}
}

func TestHomeAssistantRouteHandler_NoMatchRequiresCreation(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Code Assistant", &store.CreateAgentConfig{Type: "general"}, types.AgentStatusActive,
		"Helps with code and tests", []string{"coding"}, []string{"git", "filesystem"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Plan my 3 day trip in LA"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "travel_planning" {
		t.Fatalf("expected intent travel_planning, got %q", resp.Intent)
	}
	if !resp.RequiresCreation {
		t.Fatalf("expected requires_creation true")
	}
	if resp.MatchedAgent != "" {
		t.Fatalf("expected no matched agent, got %q", resp.MatchedAgent)
	}
	if resp.SuggestedAgentName != "Travel Planner" {
		t.Fatalf("expected suggested name Travel Planner, got %q", resp.SuggestedAgentName)
	}
	if resp.SuggestedAgentType != "research" {
		t.Fatalf("expected suggested type research, got %q", resp.SuggestedAgentType)
	}
}

func TestHomeAssistantRouteHandler_EmailMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Inbox Triage", &store.CreateAgentConfig{Type: "tool-calling"}, types.AgentStatusActive,
		"Summarizes unread emails and drafts replies", []string{"email", "inbox"}, []string{"gmail-reader"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Check my email and summarize unread messages"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "email_check" {
		t.Fatalf("expected intent email_check, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false")
	}
	if resp.MatchedAgent != "Inbox Triage" {
		t.Fatalf("expected matched agent Inbox Triage, got %q", resp.MatchedAgent)
	}
}

func TestHomeAssistantRouteHandler_EmailIntentPreferredOverAppLaunch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Inbox Triage", &store.CreateAgentConfig{Type: "tool-calling"}, types.AgentStatusActive,
		"Summarizes unread emails and drafts replies", []string{"email", "inbox"}, []string{"gmail-reader"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "open my email inbox"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "email_check" {
		t.Fatalf("expected intent email_check, got %q", resp.Intent)
	}
}

func TestHomeAssistantRouteHandler_EmailMatch_UsesMCPServers(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Task Runner", &store.CreateAgentConfig{Type: "tool-calling"}, types.AgentStatusActive,
		"General assistant for home tasks", []string{"automation"}, []string{})
	setHomeRouteAgentMCPServers(t, st, "Task Runner", []string{"gmail"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "check my email and summarize unread messages"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "email_check" {
		t.Fatalf("expected intent email_check, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false")
	}
	if resp.MatchedAgent != "Task Runner" {
		t.Fatalf("expected matched agent Task Runner, got %q", resp.MatchedAgent)
	}

	reasonContainsMCP := false
	for _, reason := range resp.Reasons {
		if reason == "has MCP support for gmail" {
			reasonContainsMCP = true
			break
		}
	}
	if !reasonContainsMCP {
		t.Fatalf("expected MCP-based reason in response, got %v", resp.Reasons)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_NoLowSignalReuse(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"}, types.AgentStatusActive,
		"Plans multi-day travel itineraries", []string{"travel", "itinerary"}, []string{"weather-tool", "web-search"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "open reaper"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "app_launch" {
		t.Fatalf("expected intent app_launch, got %q", resp.Intent)
	}
	if !resp.RequiresCreation {
		t.Fatalf("expected requires_creation true for app launch without suitable agent")
	}
	if resp.MatchedAgent != "" {
		t.Fatalf("expected no matched agent, got %q", resp.MatchedAgent)
	}
	if resp.SuggestedAgentName != "Desktop Launcher" {
		t.Fatalf("expected suggested name Desktop Launcher, got %q", resp.SuggestedAgentName)
	}
	if resp.SuggestedAgentType != "tool-calling" {
		t.Fatalf("expected suggested type tool-calling, got %q", resp.SuggestedAgentType)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_ContextualMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Desktop Launcher", &store.CreateAgentConfig{Type: "tool-calling"}, types.AgentStatusActive,
		"Opens desktop applications like reaper and finder", []string{"desktop", "automation"}, []string{"os-shell"})
	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"}, types.AgentStatusActive,
		"Plans travel itineraries", []string{"travel"}, []string{"weather-tool"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "open reaper"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "app_launch" {
		t.Fatalf("expected intent app_launch, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected contextual app launch match, got requires_creation=true")
	}
	if resp.MatchedAgent != "Desktop Launcher" {
		t.Fatalf("expected matched agent Desktop Launcher, got %q", resp.MatchedAgent)
	}
	if resp.Score < 4 {
		t.Fatalf("expected score >= 4, got %d", resp.Score)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_FallsBackToSystemAssistant(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Help me organize my thoughts for tomorrow"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "general_task" {
		t.Fatalf("expected intent general_task, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false for system assistant fallback")
	}
	if resp.MatchedAgent != systemAssistantAgentName {
		t.Fatalf("expected matched agent %q, got %q", systemAssistantAgentName, resp.MatchedAgent)
	}
	if resp.Score < 3 {
		t.Fatalf("expected score >= 3, got %d", resp.Score)
	}
	if len(resp.Reasons) == 0 || resp.Reasons[0] != "fallback to system assistant" {
		t.Fatalf("expected fallback reason, got %v", resp.Reasons)
	}

	ag, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || ag == nil {
		t.Fatalf("expected system assistant to be created in store")
	}
	if ag.Metadata == nil {
		t.Fatalf("expected metadata for system assistant")
	}
	if !containsTag(ag.Metadata.Tags, "system") || !containsTag(ag.Metadata.Tags, "orchestrator") {
		t.Fatalf("expected system/orchestrator tags, got %v", ag.Metadata.Tags)
	}
}

func TestHomeAssistantRouteHandler_UtilityPrompt_FallsBackToSystemAssistant(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "What time is it in Tokyo?"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "utility_direct" {
		t.Fatalf("expected intent utility_direct, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false for utility fallback")
	}
	if resp.MatchedAgent != systemAssistantAgentName {
		t.Fatalf("expected matched agent %q, got %q", systemAssistantAgentName, resp.MatchedAgent)
	}
	if resp.Score < 4 {
		t.Fatalf("expected score >= 4, got %d", resp.Score)
	}
	if len(resp.Reasons) == 0 {
		t.Fatalf("expected non-empty reasons for utility match")
	}
}

func TestHomeAssistantRouteHandler_AirQualityPrompt_UsesUtilityIntent(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "air quality in seoul today"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "utility_direct" {
		t.Fatalf("expected intent utility_direct, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false for utility fallback")
	}
	if resp.MatchedAgent != systemAssistantAgentName {
		t.Fatalf("expected matched agent %q, got %q", systemAssistantAgentName, resp.MatchedAgent)
	}
}

func TestHomeAssistantRouteHandler_CreatesSystemAssistantFiles(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "agents_index.json")
	st, err := store.NewFileStore(storePath, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}

	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "quick fact: capital of japan"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	agentSettingsPath := filepath.Join(tmpDir, "agents", systemAssistantAgentName, "agent_settings.json")
	if _, err := os.Stat(agentSettingsPath); err != nil {
		t.Fatalf("expected persisted assistant file at %s: %v", agentSettingsPath, err)
	}
}

func TestHomeAssistantRouteHandler_SystemAssistantUsesConfiguredSystemModel(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)
	handler.SetSystemModelReader(systemModelReaderStub{
		provider: "openai",
		model:    "gpt-4o-mini",
	})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "What time is it in Tokyo?"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	ag, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || ag == nil {
		t.Fatalf("expected system assistant to exist")
	}
	if ag.Settings.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", ag.Settings.Provider)
	}
	if ag.Settings.Model != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %q", ag.Settings.Model)
	}
}

func TestHomeAssistantRouteHandler_MigratesLegacyAssistantName(t *testing.T) {
	st := newHomeRouteTestStore(t)
	if err := st.CreateAgent(systemAssistantAgentLegacyName, &store.CreateAgentConfig{
		Type:        "general",
		Model:       "gpt-5-nano",
		LLMProvider: "openai",
	}); err != nil {
		t.Fatalf("failed to create legacy assistant: %v", err)
	}

	handler := NewHomeAssistantRouteHandler(st)
	rr := postRouteRequest(t, handler, map[string]string{"prompt": "quick fact: capital of japan"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	if _, ok := st.GetAgent(systemAssistantAgentName); !ok {
		t.Fatalf("expected migrated assistant %q to exist", systemAssistantAgentName)
	}
	if _, ok := st.GetAgent(systemAssistantAgentLegacyName); ok {
		t.Fatalf("expected legacy assistant %q to be removed", systemAssistantAgentLegacyName)
	}
}

type systemModelReaderStub struct {
	provider string
	model    string
}

func (s systemModelReaderStub) GetSystemModel() (string, string) {
	return s.provider, s.model
}
