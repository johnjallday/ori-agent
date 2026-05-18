package agenthttp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
)

type homeAssistantWorkspaceRouteFixture struct {
	Name                   string `json:"name"`
	Prompt                 string `json:"prompt"`
	ExpectedContextMode    string `json:"expected_context_mode"`
	ExpectedWorkspaceState string `json:"expected_workspace_state"`
	ExpectedWorkspaceID    string `json:"expected_workspace_id"`
}

func loadHomeAssistantWorkspaceRouteFixtures(t *testing.T) []homeAssistantWorkspaceRouteFixture {
	t.Helper()

	path := filepath.Join("testdata", "home_assistant_workspace_route_fixtures.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}

	var fixtures []homeAssistantWorkspaceRouteFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	return fixtures
}

func newHomeAssistantWorkspaceFixtureHandler(t *testing.T) *HomeAssistantRouteHandler {
	t.Helper()

	st := newHomeRouteTestStore(t)
	for _, name := range []string{"Launch Manager", "Robotics Manager"} {
		addHomeRouteTestAgent(t, st, name, &store.CreateAgentConfig{Type: "general"}, "", nil, nil)
	}

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-alpha", "Launch Alpha", "Build launch dashboard", "Launch Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-beta", "Launch Beta", "Build launch dashboard", "Launch Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-robotics", "Robotics Dashboard", "Build robotics dashboard", "Robotics Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-broken", "Broken Ops", "Build broken ops dashboard", "Missing Manager"),
	)

	handler := NewHomeAssistantRouteHandler(st)
	handler.SetWorkspaceResolver(resolver)
	return handler
}

func TestHomeAssistantRouteHandler_WorkspaceRouteFixtures(t *testing.T) {
	handler := newHomeAssistantWorkspaceFixtureHandler(t)

	for _, fixture := range loadHomeAssistantWorkspaceRouteFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			got, err := handler.RoutePrompt(fixture.Prompt, nil)
			if err != nil {
				t.Fatalf("route prompt: %v", err)
			}
			if got.ContextMode != fixture.ExpectedContextMode {
				t.Fatalf("expected context mode %q, got %q", fixture.ExpectedContextMode, got.ContextMode)
			}
			if fixture.ExpectedWorkspaceState == "" {
				if got.WorkspaceResolution != nil {
					t.Fatalf("expected no workspace resolution, got %#v", got.WorkspaceResolution)
				}
				return
			}
			if got.WorkspaceResolution == nil {
				t.Fatalf("expected workspace resolution")
			}
			if got.WorkspaceResolution.State != fixture.ExpectedWorkspaceState {
				t.Fatalf("expected workspace state %q, got %#v", fixture.ExpectedWorkspaceState, got.WorkspaceResolution)
			}
			if fixture.ExpectedWorkspaceID != "" && got.WorkspaceResolution.SelectedWorkspaceID != fixture.ExpectedWorkspaceID {
				t.Fatalf("expected workspace id %q, got %q", fixture.ExpectedWorkspaceID, got.WorkspaceResolution.SelectedWorkspaceID)
			}
		})
	}
}
