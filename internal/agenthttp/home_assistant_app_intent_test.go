package agenthttp

import (
	"context"
	"testing"
)

// TestClassifyHomeIntentPrecedence covers the FR #4 precedence examples plus the
// new app_introspection / app_navigation intents, and confirms existing intents
// are preserved.
func TestClassifyHomeIntentPrecedence(t *testing.T) {
	st := newHomeRouteTestStore(t)
	resolver := newHomeWorkspaceResolverForTest(t, st,
		newHomeWorkspaceResolverTestWorkspace("ws-q3", "Q3 Planning", "Quarterly planning"),
	)
	h := NewHomeAssistantRouteHandler(st)
	h.SetWorkspaceResolver(resolver)

	cases := []struct {
		prompt string
		want   string
	}{
		{"summarize this week's task activity", "app_introspection"},
		{"what did I work on this week", "app_introspection"},
		{"how many tasks are pending", "app_introspection"},
		{"where do I manage MCP connectors?", "app_navigation"},
		{"open Action Center", "app_navigation"},
		{"open the Q3 Planning workspace", "app_navigation"},
		{"open Safari", "app_launch"},
		{"create workspace called Q3 Roadmap", "workspace_create"},
		{"what's the weather in Tokyo", "utility_direct"},
	}
	for _, tc := range cases {
		if got := h.classifyHomeIntent(tc.prompt); got.Key != tc.want {
			t.Errorf("classifyHomeIntent(%q) = %q, want %q", tc.prompt, got.Key, tc.want)
		}
	}
}

// TestRoutePromptInlineModeForAppIntents confirms the route response selects the
// inline route mode for the two app intents.
func TestRoutePromptInlineModeForAppIntents(t *testing.T) {
	st := newHomeRouteTestStore(t)
	resolver := newHomeWorkspaceResolverForTest(t, st)
	h := NewHomeAssistantRouteHandler(st)
	h.SetWorkspaceResolver(resolver)

	resp, err := h.RoutePrompt(context.Background(), "summarize this week's task activity", nil)
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	if resp.Intent != "app_introspection" {
		t.Fatalf("intent = %q, want app_introspection", resp.Intent)
	}
	if resp.RouteMode != homeAssistantRouteModeInline {
		t.Errorf("route_mode = %q, want %q", resp.RouteMode, homeAssistantRouteModeInline)
	}
}
