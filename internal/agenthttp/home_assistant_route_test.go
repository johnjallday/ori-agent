package agenthttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
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

func addHomeRouteTestAgent(t *testing.T, st store.Store, name string, cfg *store.CreateAgentConfig, description string, tags []string, plugins []string) {
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

	ag.Status = types.AgentStatusActive
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	ag.Metadata.Description = description
	ag.Metadata.Tags = tags
	ag.Metadata.Favorite = false

	// Plugins have been deprecated; plugin names are no longer stored on agents.
	_ = plugins

	if err := st.SetAgent(name, ag); err != nil {
		t.Fatalf("failed to persist agent %q: %v", name, err)
	}
}

func setHomeRouteTestAgentRoutingProfile(t *testing.T, st store.Store, name string, profile *types.AgentRoutingProfile) {
	t.Helper()

	ag, ok := st.GetAgent(name)
	if !ok || ag == nil {
		t.Fatalf("agent %q not found", name)
	}
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	ag.Metadata.RoutingProfile = profile
	if err := st.SetAgent(name, ag); err != nil {
		t.Fatalf("failed to persist routing profile for %q: %v", name, err)
	}
}

type homeRouteRuntimeResolverStub struct {
	store   store.Store
	servers map[string][]string
}

func (s *homeRouteRuntimeResolverStub) ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error) {
	ag, ok := s.store.GetAgent(agentName)
	if !ok || ag == nil {
		return nil, nil
	}
	return &workspace.ResolvedAgentRuntime{
		Agent:      ag,
		MCPServers: append([]string{}, s.servers[agentName]...),
	}, nil
}

func setHomeRouteRuntimeMCPServers(handler *HomeAssistantRouteHandler, st store.Store, name string, servers []string) {
	handler.SetRuntimeResolver(&homeRouteRuntimeResolverStub{
		store:   st,
		servers: map[string][]string{name: append([]string{}, servers...)},
	})
}

func postRouteRequest(t *testing.T, handler *HomeAssistantRouteHandler, payload any) *httptest.ResponseRecorder {
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

func postTraceRequest(t *testing.T, handler *HomeAssistantRouteHandler, payload any) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/home-assistant/trace", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.TraceHandler(rr, req)
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

func TestHomeAssistantRouteHandler_TraceHandlerRequiresPromptAndTarget(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postTraceRequest(t, handler, HomeAssistantIntakeTrace{
		Prompt: "ship launch tasks",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d for missing target, got %d", http.StatusBadRequest, rr.Code)
	}

	rr = postTraceRequest(t, handler, HomeAssistantIntakeTrace{
		FinalHandoffTarget: "workspace_assistant",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d for missing prompt, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHomeAssistantRouteHandler_TraceHandlerAcceptsValidPayload(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postTraceRequest(t, handler, HomeAssistantIntakeTrace{
		Prompt:             "ship launch tasks",
		Intent:             "general_task",
		ContextMode:        homeAssistantContextWorkspace,
		FinalHandoffTarget: "workspace_assistant",
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, rr.Code, rr.Body.String())
	}
}

func TestHomeAssistantRouteHandler_TraceHandlerPersistsWhenStoreAvailable(t *testing.T) {
	st := newHomeRouteTestStore(t)
	traceStore := &homeAssistantIntakeTraceStoreStub{}
	handler := NewHomeAssistantRouteHandler(st)
	handler.SetIntakeTraceStore(traceStore)

	rr := postTraceRequest(t, handler, HomeAssistantIntakeTrace{
		Prompt:             "ship launch tasks",
		Intent:             "general_task",
		ContextMode:        homeAssistantContextWorkspace,
		FinalHandoffTarget: "workspace_assistant",
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, rr.Code, rr.Body.String())
	}
	if len(traceStore.traces) != 1 {
		t.Fatalf("expected one stored trace, got %d", len(traceStore.traces))
	}
	if traceStore.traces[0].Prompt != "ship launch tasks" {
		t.Fatalf("expected prompt to persist, got %#v", traceStore.traces[0])
	}
}

func TestHomeAssistantRouteHandler_TraceHandlerReturnsErrorWhenPersistenceFails(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)
	handler.SetIntakeTraceStore(&homeAssistantIntakeTraceStoreStub{err: errors.New("boom")})

	rr := postTraceRequest(t, handler, HomeAssistantIntakeTrace{
		Prompt:             "ship launch tasks",
		Intent:             "general_task",
		ContextMode:        homeAssistantContextWorkspace,
		FinalHandoffTarget: "workspace_assistant",
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d: %s", http.StatusInternalServerError, rr.Code, rr.Body.String())
	}
}

func TestHomeAssistantRouteHandler_TraceSummaryHandlerRequiresStore(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	req := httptest.NewRequest(http.MethodGet, "/api/home-assistant/trace/summary", nil)
	rr := httptest.NewRecorder()
	handler.TraceSummaryHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}
}

func TestHomeAssistantRouteHandler_TraceSummaryHandlerReturnsSummary(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)
	handler.SetIntakeTraceStore(&homeAssistantIntakeTraceStoreStub{
		summary: HomeAssistantIntakeTraceSummary{
			TotalCount:      3,
			WorkspaceCount:  2,
			CorrectionCount: 1,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/home-assistant/trace/summary", nil)
	rr := httptest.NewRecorder()
	handler.TraceSummaryHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	var got HomeAssistantIntakeTraceSummary
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if got.TotalCount != 3 || got.WorkspaceCount != 2 || got.CorrectionCount != 1 {
		t.Fatalf("unexpected summary %#v", got)
	}
}

func TestHomeAssistantRouteHandler_TravelMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"},
		"Plans multi-day travel itineraries", []string{"travel", "itinerary"}, []string{"weather-tool", "web-search"})
	addHomeRouteTestAgent(t, st, "Code Helper", &store.CreateAgentConfig{Type: "general"},
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
	if resp.RoutingPolicy != homeAssistantPolicyAssistantPreferred {
		t.Fatalf("expected routing policy %q, got %q", homeAssistantPolicyAssistantPreferred, resp.RoutingPolicy)
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

	addHomeRouteTestAgent(t, st, "Code Assistant", &store.CreateAgentConfig{Type: "general"},
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

	addHomeRouteTestAgent(t, st, "Inbox Triage", &store.CreateAgentConfig{Type: "tool-calling"},
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
	if resp.RoutingPolicy != homeAssistantPolicySpecialistRequired {
		t.Fatalf("expected routing policy %q, got %q", homeAssistantPolicySpecialistRequired, resp.RoutingPolicy)
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

	addHomeRouteTestAgent(t, st, "Inbox Triage", &store.CreateAgentConfig{Type: "tool-calling"},
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

	addHomeRouteTestAgent(t, st, "Task Runner", &store.CreateAgentConfig{Type: "tool-calling"},
		"General assistant for home tasks", []string{"automation"}, []string{})
	setHomeRouteRuntimeMCPServers(handler, st, "Task Runner", []string{"gmail"})

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "check my email and summarize unread messages",
		"context": map[string]any{
			"workspace_id": "ws-email",
		},
	})
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

func TestHomeAssistantRouteHandler_CalendarMatch_UsesIntentVariant(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Calendar Assistant", &store.CreateAgentConfig{Type: "tool-calling"},
		"Checks calendar events and schedule availability", []string{"calendar", "schedule"}, nil)
	setHomeRouteRuntimeMCPServers(handler, st, "Calendar Assistant", []string{"google-calendar"})

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "check my schedule",
		"context": map[string]any{
			"workspace_id": "ws-calendar",
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "calendar_check" {
		t.Fatalf("expected intent calendar_check, got %q", resp.Intent)
	}
	if resp.IntentVariant != "personal_calendar" {
		t.Fatalf("expected personal_calendar variant, got %q", resp.IntentVariant)
	}
	if resp.MatchedAgent != "Calendar Assistant" {
		t.Fatalf("expected matched agent Calendar Assistant, got %q", resp.MatchedAgent)
	}
}

func TestHomeAssistantRouteHandler_CalendarIntent_AmbiguousInWorkspaceContext(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "check my schedule",
		"context": map[string]any{
			"workspace_id": "ws-123",
			"page_path":    "/workspaces/ws-123",
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "calendar_check" {
		t.Fatalf("expected intent calendar_check, got %q", resp.Intent)
	}
	if resp.IntentVariant != "ambiguous" {
		t.Fatalf("expected ambiguous variant, got %q", resp.IntentVariant)
	}
	if resp.RouteMode != "specialist_handoff" || resp.TargetSurface != "chat" {
		t.Fatalf("expected ambiguous request to stay in chat flow, got mode=%q surface=%q", resp.RouteMode, resp.TargetSurface)
	}
}

func TestHomeAssistantRouteHandler_CalendarIntent_WorkspaceScheduleRoutesToWorkspace(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "what scheduled tasks run today in this workspace",
		"context": map[string]any{
			"workspace_id": "ws-123",
			"page_path":    "/workspaces/ws-123",
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "calendar_check" {
		t.Fatalf("expected intent calendar_check, got %q", resp.Intent)
	}
	if resp.IntentVariant != "workspace_schedule" {
		t.Fatalf("expected workspace_schedule variant, got %q", resp.IntentVariant)
	}
	if resp.RoutingPolicy != homeAssistantPolicyAssistantOnly {
		t.Fatalf("expected routing policy %q, got %q", homeAssistantPolicyAssistantOnly, resp.RoutingPolicy)
	}
	if resp.RouteMode != "workspace_task" || resp.TargetSurface != "workspace" {
		t.Fatalf("expected workspace route, got mode=%q surface=%q", resp.RouteMode, resp.TargetSurface)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_NoLowSignalReuse(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"},
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

	addHomeRouteTestAgent(t, st, "Desktop Launcher", &store.CreateAgentConfig{Type: "tool-calling"},
		"Opens desktop applications like reaper and finder", []string{"desktop", "automation"}, []string{"os-shell"})
	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"},
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

func TestHomeAssistantRouteHandler_AppLaunchMatch_UsesRoutingProfile(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "REAPER Assistant", &store.CreateAgentConfig{Type: "tool-calling"},
		"Handles audio production workflows", []string{"audio"}, []string{})
	setHomeRouteTestAgentRoutingProfile(t, st, "REAPER Assistant", &types.AgentRoutingProfile{
		MatchPhrases:    []string{"open my latest reaper project"},
		ExampleRequests: []string{"open my latest reaper project", "render stems from yesterday's session"},
		Domains:         []string{"reaper", "audio production"},
		ExternalSystems: []string{"reaper"},
		SideEffects:     "local_app",
	})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "open my latest reaper project"})
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
	if resp.RoutingPolicy != homeAssistantPolicySpecialistRequired {
		t.Fatalf("expected routing policy %q, got %q", homeAssistantPolicySpecialistRequired, resp.RoutingPolicy)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected routing-profile match, got requires_creation=true")
	}
	if resp.MatchedAgent != "REAPER Assistant" {
		t.Fatalf("expected matched agent REAPER Assistant, got %q", resp.MatchedAgent)
	}
	if resp.Score < 4 {
		t.Fatalf("expected score >= 4, got %d", resp.Score)
	}
}

func TestHomeAssistantRouteHandler_WorkspaceNotePrompt_NotClassifiedAsAppLaunch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Desktop Launcher", &store.CreateAgentConfig{Type: "tool-calling"},
		"Opens desktop applications like reaper and finder", []string{"desktop", "automation"}, []string{"os-shell"})

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "start another note",
		"context": map[string]any{
			"workspace_id": "ws-notes",
			"page_path":    "/workspaces/ws-notes",
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent == "app_launch" {
		t.Fatalf("expected workspace note follow-up to avoid app launch, got %q", resp.Intent)
	}
	if resp.RoutingPolicy != homeAssistantPolicyAssistantOnly {
		t.Fatalf("expected routing policy %q, got %q", homeAssistantPolicyAssistantOnly, resp.RoutingPolicy)
	}
	if resp.RouteMode != "workspace_task" || resp.TargetSurface != "workspace" {
		t.Fatalf("expected workspace note follow-up to stay in workspace flow, got mode=%q surface=%q", resp.RouteMode, resp.TargetSurface)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_UsesRoutingProfileExamples(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "REAPER Assistant", &store.CreateAgentConfig{Type: "tool-calling"},
		"Handles DAW automation", []string{"audio"}, []string{})
	setHomeRouteTestAgentRoutingProfile(t, st, "REAPER Assistant", &types.AgentRoutingProfile{
		ExampleRequests: []string{
			"render stems from yesterday's session",
			"show muted tracks in the current reaper session",
		},
		Domains:         []string{"reaper", "mixing"},
		ExternalSystems: []string{"reaper"},
		SideEffects:     "local_app",
	})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "show muted tracks in reaper"})
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
	if resp.RoutingPolicy != homeAssistantPolicyAssistantPreferred {
		t.Fatalf("expected routing policy %q, got %q", homeAssistantPolicyAssistantPreferred, resp.RoutingPolicy)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected routing-profile match for general prompt, got requires_creation=true")
	}
	if resp.MatchedAgent != "REAPER Assistant" {
		t.Fatalf("expected matched agent REAPER Assistant, got %q", resp.MatchedAgent)
	}
	if resp.Score < 3 {
		t.Fatalf("expected score >= 3, got %d", resp.Score)
	}
}

func TestHomeAssistantRouteHandler_OpenDomain_NotClassifiedAsAppLaunch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Desktop Launcher", &store.CreateAgentConfig{Type: "tool-calling"},
		"Opens desktop applications like reaper and finder", []string{"desktop", "automation"}, []string{"os-shell"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "open instagram.com"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent == "app_launch" {
		t.Fatalf("expected non-app-launch intent for domain target, got %q", resp.Intent)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_FallsBackToSystemAssistant(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	// Explicitly create the system assistant for fallback tests
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("failed to ensure system assistant: %v", err)
	}

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

func TestHomeAssistantRouteHandler_GeneralPrompt_ComplexProjectSetsWorkspaceRecommendation(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Let's create a website from scratch with authentication and a database"})
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
	if !resp.WorkspaceRecommended {
		t.Fatalf("expected workspace recommendation for complex project prompt")
	}
	if resp.RouteMode != "workspace_task" {
		t.Fatalf("expected route_mode workspace_task, got %q", resp.RouteMode)
	}
	if resp.ContextMode != homeAssistantContextWorkspace {
		t.Fatalf("expected context_mode %q, got %q", homeAssistantContextWorkspace, resp.ContextMode)
	}
	if resp.HandoffPolicy != homeAssistantHandoffAssistant {
		t.Fatalf("expected handoff_policy %q, got %q", homeAssistantHandoffAssistant, resp.HandoffPolicy)
	}
	if resp.TargetSurface != "workspace" {
		t.Fatalf("expected target_surface workspace, got %q", resp.TargetSurface)
	}
}

func TestHomeAssistantRouteHandler_WorkspaceContext_ForcesWorkspaceMode(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Task Assistant", &store.CreateAgentConfig{Type: "general"},
		"General purpose task helper", []string{"tasks"}, []string{})

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "Draft a migration checklist for our service",
		"context": map[string]any{
			"surface":      "workspace_canvas",
			"page_path":    "/workspaces/ws-abc/canvas",
			"workspace_id": "ws-abc",
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.RouteMode != "workspace_task" {
		t.Fatalf("expected route_mode workspace_task, got %q", resp.RouteMode)
	}
	if resp.TargetSurface != "workspace" {
		t.Fatalf("expected target_surface workspace, got %q", resp.TargetSurface)
	}
}

func TestHomeAssistantRouteHandler_UtilityPrompt_WorkspaceContextStaysUtilityDirect(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "What time is it in Tokyo?",
		"context": map[string]any{
			"surface":      "workspace_detail",
			"page_path":    "/workspaces/ws-abc",
			"workspace_id": "ws-abc",
		},
	})
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
	if resp.RouteMode != "utility_direct" {
		t.Fatalf("expected route_mode utility_direct, got %q", resp.RouteMode)
	}
	if resp.ContextMode != homeAssistantContextDirect {
		t.Fatalf("expected context_mode %q, got %q", homeAssistantContextDirect, resp.ContextMode)
	}
	if resp.HandoffPolicy != homeAssistantHandoffTool {
		t.Fatalf("expected handoff_policy %q, got %q", homeAssistantHandoffTool, resp.HandoffPolicy)
	}
	if resp.TargetSurface != "current" {
		t.Fatalf("expected target_surface current, got %q", resp.TargetSurface)
	}
}

func TestHomeAssistantRouteHandler_WorkspaceCreatePrompt_UsesWorkspaceIntentAndMode(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]any{
		"prompt": "create workspace called test2",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "workspace_create" {
		t.Fatalf("expected intent workspace_create, got %q", resp.Intent)
	}
	if resp.RouteMode != "workspace_task" {
		t.Fatalf("expected route_mode workspace_task, got %q", resp.RouteMode)
	}
	if resp.TargetSurface != "workspace" {
		t.Fatalf("expected target_surface workspace, got %q", resp.TargetSurface)
	}
}

func TestHomeAssistantRouteHandler_UtilityPrompt_FallsBackToSystemAssistant(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	// Explicitly create the system assistant for utility tests
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("failed to ensure system assistant: %v", err)
	}

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
	if resp.WorkspaceRecommended {
		t.Fatalf("expected workspace recommendation to be false for utility prompt")
	}
}

func TestHomeAssistantRouteHandler_AirQualityPrompt_UsesUtilityIntent(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	// Explicitly create the system assistant for utility tests
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("failed to ensure system assistant: %v", err)
	}

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

func TestHomeAssistantRouteHandler_DoesNotCreateSystemAssistantFilesByDefault(t *testing.T) {
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
	if _, err := os.Stat(agentSettingsPath); !os.IsNotExist(err) {
		t.Fatalf("expected system assistant file at %s NOT to exist, but it does", agentSettingsPath)
	}
}

func TestHomeAssistantRouteHandler_DoesNotMigrateLegacyAssistantNameByRoute(t *testing.T) {
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

	if _, ok := st.GetAgent(systemAssistantAgentName); ok {
		t.Fatalf("expected assistant %q NOT to exist yet", systemAssistantAgentName)
	}
	if _, ok := st.GetAgent(systemAssistantAgentLegacyName); !ok {
		t.Fatalf("expected legacy assistant %q to still exist", systemAssistantAgentLegacyName)
	}
}
