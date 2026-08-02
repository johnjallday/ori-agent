package workspace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Goal Prepare handler coverage (task 4.14) and the end-to-end safety proof
// that a recommendation cannot change anything until explicit review (task
// 4.16; PRD FR-94, FR-99–FR-106).

func newGoalPrepareFixture(t *testing.T) (*HTTPHandler, string) {
	t.Helper()
	ws := newRecommendationFixture(t)
	ws.Mission = "Search the web and summarize what changed"
	ws.MissionEnabled = true
	ws.AutonomyPolicy = AutonomyWatch
	return NewHTTPHandler(newTestWorkspaceStore(t, ws), nil, nil), ws.ID
}

func getGoalJSON(t *testing.T, handler http.HandlerFunc, url, workspaceID string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("workspaceID", workspaceID)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeJSONBody(t, rr)
}

// FR-94: proposing writes nothing. The brief only starts driving anything once
// the user accepts it.
func TestGetGoalBrief_ProposesWithoutPersisting(t *testing.T) {
	handler, workspaceID := newGoalPrepareFixture(t)
	before, _ := handler.store.Get(workspaceID)

	payload := getGoalJSON(t, handler.GetGoalBrief, "/api/workspaces/"+workspaceID+"/goal/brief", workspaceID)

	if payload["accepted"] != nil {
		t.Fatalf("expected no accepted brief yet, got %v", payload["accepted"])
	}
	proposed, _ := payload["proposed"].(map[string]any)
	if proposed == nil || proposed["max_autonomy"] != GoalAutonomyRead {
		t.Fatalf("expected a proposal that respects the watch-only policy, got %v", proposed)
	}

	after, _ := handler.store.Get(workspaceID)
	if after.GoalBrief != nil || after.Version != before.Version {
		t.Fatalf("expected proposing to write nothing, got brief=%v version %d -> %d",
			after.GoalBrief, before.Version, after.Version)
	}
}

func TestUpdateGoalBrief_AcceptsAndVersions(t *testing.T) {
	handler, workspaceID := newGoalPrepareFixture(t)

	first := postToolboxJSON(t, handler.UpdateGoalBrief, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/goal/brief", map[string]string{"workspaceID": workspaceID},
		map[string]any{
			"summary":               "Search and summarize",
			"operations":            []string{"Search", "search", " READ "},
			"required_capabilities": []string{"summarize"},
			"max_autonomy":          "read",
		})
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", first.Code, first.Body.String())
	}
	brief, _ := decodeJSONBody(t, first)["brief"].(map[string]any)
	if brief["version"].(float64) != 1 {
		t.Fatalf("expected the first accepted brief at version 1, got %v", brief["version"])
	}
	// Normalization is what makes ranking deterministic (FR-95).
	operations, _ := brief["operations"].([]any)
	if len(operations) != 2 {
		t.Fatalf("expected duplicate/whitespace operations to normalize, got %v", operations)
	}
	if brief["accepted_at"] == nil {
		t.Fatalf("expected the brief to be marked accepted")
	}

	second := postToolboxJSON(t, handler.UpdateGoalBrief, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/goal/brief", map[string]string{"workspaceID": workspaceID},
		map[string]any{"summary": "Edited", "required_capabilities": []string{"summarize"}})
	edited, _ := decodeJSONBody(t, second)["brief"].(map[string]any)
	if edited["version"].(float64) != 2 {
		t.Fatalf("expected an edit to version the brief, got %v", edited["version"])
	}
}

// FR-99: ranking never selects, applies, or changes anything.
func TestGetGoalRecommendations_IsInert(t *testing.T) {
	handler, workspaceID := newGoalPrepareFixture(t)
	postToolboxJSON(t, handler.UpdateGoalBrief, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/goal/brief", map[string]string{"workspaceID": workspaceID},
		map[string]any{"required_capabilities": []string{"summarize"}, "max_autonomy": "read"})

	before, _ := handler.store.Get(workspaceID)
	beforeVersion := before.Version

	payload := getGoalJSON(t, handler.GetGoalRecommendations,
		"/api/workspaces/"+workspaceID+"/goal/recommendations", workspaceID)

	result, _ := payload["recommendations"].(map[string]any)
	candidates, _ := result["recommendations"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("expected candidates, got %v", result)
	}

	after, _ := handler.store.Get(workspaceID)
	if after.Version != beforeVersion {
		t.Fatalf("expected ranking to write nothing, version went %d -> %d", beforeVersion, after.Version)
	}
	if _, assigned := after.GetToolboxAssignment("inst-1"); assigned {
		t.Fatalf("expected ranking not to assign a toolbox")
	}
	// Nothing was connected, installed, or trusted either.
	for i, binding := range after.GetMCPBindings() {
		if binding.Enabled != before.GetMCPBindings()[i].Enabled {
			t.Fatalf("expected ranking to leave connection state alone")
		}
	}
}

// FR-103/FR-104: pinning fixes an exact version and does not follow later
// edits; current-at-start is the explicitly labeled alternative.
func TestUpdateGoalToolboxPolicy_PinsAndLabels(t *testing.T) {
	handler, workspaceID := newGoalPrepareFixture(t)

	pinned := postToolboxJSON(t, handler.UpdateGoalToolboxPolicy, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/goal/toolbox-policy", map[string]string{"workspaceID": workspaceID},
		map[string]any{"entry_agent_instance_id": "inst-1", "toolbox_id": "tbx-lean"})
	if pinned.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", pinned.Code, pinned.Body.String())
	}
	policy, _ := decodeJSONBody(t, pinned)["policy"].(map[string]any)
	if policy["toolbox_version"].(float64) != 1 {
		t.Fatalf("expected the current version to be pinned, got %v", policy["toolbox_version"])
	}

	// Editing the toolbox creates v2; the pin stays on v1.
	if err := handler.store.Update(workspaceID, func(ws *Workspace) error {
		_, err := ws.SaveToolboxVersion("tbx-lean", nil, nil, ToolboxProvenanceUser, "tester")
		return err
	}); err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}
	reloaded, _ := handler.store.Get(workspaceID)
	if reloaded.GoalToolboxPolicy.ToolboxVersion != 1 {
		t.Fatalf("expected the pin to stay on version 1 after an edit, got %d", reloaded.GoalToolboxPolicy.ToolboxVersion)
	}

	current := postToolboxJSON(t, handler.UpdateGoalToolboxPolicy, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/goal/toolbox-policy", map[string]string{"workspaceID": workspaceID},
		map[string]any{"entry_agent_instance_id": "inst-1", "use_current_at_start": true})
	message, _ := decodeJSONBody(t, current)["message"].(string)
	if message != "This goal will use the current toolbox when it starts." {
		t.Fatalf("expected the alternative policy to be labelled explicitly, got %q", message)
	}
}

// FR-105: the preflight the scheduler runs is also readable, so a manual start
// refuses for the same reason.
func TestGetGoalPreflight_ReportsAnUnusablePin(t *testing.T) {
	handler, workspaceID := newGoalPrepareFixture(t)
	postToolboxJSON(t, handler.UpdateGoalToolboxPolicy, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/goal/toolbox-policy", map[string]string{"workspaceID": workspaceID},
		map[string]any{"entry_agent_instance_id": "inst-1", "toolbox_id": "tbx-lean"})

	ok := getGoalJSON(t, handler.GetGoalPreflight, "/api/workspaces/"+workspaceID+"/goal/preflight", workspaceID)
	if preflight, _ := ok["preflight"].(map[string]any); preflight["ok"] != true {
		t.Fatalf("expected a healthy pin to pass, got %v", preflight)
	}

	if err := handler.store.Update(workspaceID, func(ws *Workspace) error {
		for i := range ws.MCPBindings {
			if ws.MCPBindings[i].ID == "mb-web" {
				ws.MCPBindings[i].Enabled = false
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("failed to disconnect the binding: %v", err)
	}

	blocked := getGoalJSON(t, handler.GetGoalPreflight, "/api/workspaces/"+workspaceID+"/goal/preflight", workspaceID)
	preflight, _ := blocked["preflight"].(map[string]any)
	if preflight["ok"] != false {
		t.Fatalf("expected a lost connection to stop the goal, got %v", preflight)
	}
	if preflight["reason"] == "" {
		t.Fatalf("expected the refusal to explain itself")
	}
}

// FR-94/FR-99–FR-106 end to end: from proposal through ranking, nothing about
// the agent, its toolbox, its connections, its scopes, expert mode, or the
// goal's autonomy changes until an explicit review completes.
func TestGoalPrepare_ChangesNothingUntilExplicitReview(t *testing.T) {
	handler, workspaceID := newGoalPrepareFixture(t)

	globalAgent := &agent.Agent{
		Settings:       types.Settings{Model: "gpt-5", SystemPrompt: "Be careful."},
		Metadata:       &types.AgentMetadata{},
		Evolution:      &types.AgentEvolution{Stage: types.AgentStageLearner},
		DefaultToolbox: &types.AgentDefaultToolbox{Version: 1},
	}
	beforeAgent := *globalAgent
	beforeExpert := globalAgent.Metadata.IsExpertMode(globalAgent.Role)

	before, _ := handler.store.Get(workspaceID)
	beforeAutonomy := before.AutonomyPolicy
	beforeBindings := before.GetMCPBindings()

	// Propose → accept → rank. The whole Prepare flow.
	getGoalJSON(t, handler.GetGoalBrief, "/api/workspaces/"+workspaceID+"/goal/brief", workspaceID)
	postToolboxJSON(t, handler.UpdateGoalBrief, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/goal/brief", map[string]string{"workspaceID": workspaceID},
		map[string]any{"required_capabilities": []string{"summarize", "citations"}, "max_autonomy": "read"})
	getGoalJSON(t, handler.GetGoalRecommendations,
		"/api/workspaces/"+workspaceID+"/goal/recommendations", workspaceID)

	after, _ := handler.store.Get(workspaceID)

	if _, assigned := after.GetToolboxAssignment("inst-1"); assigned {
		t.Fatalf("expected no toolbox to be assigned by preparing a goal")
	}
	if after.AutonomyPolicy != beforeAutonomy {
		t.Fatalf("expected goal autonomy to be untouched, got %q", after.AutonomyPolicy)
	}
	for i, binding := range after.GetMCPBindings() {
		if binding.Enabled != beforeBindings[i].Enabled ||
			len(binding.AllowedTools) != len(beforeBindings[i].AllowedTools) ||
			binding.DefaultSideEffect != beforeBindings[i].DefaultSideEffect {
			t.Fatalf("expected connection, scope, and classification to be untouched")
		}
	}
	if globalAgent.Settings.Model != beforeAgent.Settings.Model ||
		globalAgent.Settings.SystemPrompt != beforeAgent.Settings.SystemPrompt ||
		globalAgent.Evolution.Stage != beforeAgent.Evolution.Stage ||
		globalAgent.DefaultToolbox.Version != beforeAgent.DefaultToolbox.Version {
		t.Fatalf("expected the reusable agent to be untouched, got %+v", globalAgent)
	}
	if globalAgent.Metadata.IsExpertMode(globalAgent.Role) != beforeExpert {
		t.Fatalf("expected expert mode to be untouched")
	}
	// Toolboxes were neither created nor edited.
	if len(after.GetToolboxes()) != len(before.GetToolboxes()) {
		t.Fatalf("expected no toolbox to be created by preparing a goal")
	}
	for i, definition := range after.GetToolboxes() {
		if definition.Version != before.GetToolboxes()[i].Version {
			t.Fatalf("expected no toolbox to be versioned by preparing a goal")
		}
	}
}
