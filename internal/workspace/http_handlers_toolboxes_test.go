package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Read-surface coverage for the Toolbox APIs (task 1.14) plus the shared-agent
// isolation invariants they must never break (FR-26, FR-27, FR-156).

func newToolboxHandlerFixture(t *testing.T) (*HTTPHandler, *Workspace, *ToolboxDefinition) {
	t.Helper()
	ws := &Workspace{
		ID: "ws-toolbox-http",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Coder", NodeID: "coder-2", InstanceNumber: 2},
		},
		SkillBindings: []SkillBinding{{ID: "sb-1", SkillName: "testing", Enabled: true}},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note", "write_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
		},
	}

	created, err := ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-1",
		Name:        "Research Kit",
		Description: "Reading and note-taking.",
		Skills:      []ToolboxSkillRef{{CapabilityID: "testing", DisplayName: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"}},
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note"}}},
	})
	if err != nil {
		t.Fatalf("CreateToolbox() error = %v", err)
	}
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: created.ID, Provenance: ToolboxProvenanceUser}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	store := newTestWorkspaceStore(t, ws)
	reloaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	return NewHTTPHandler(store, nil, nil), reloaded, created
}

func decodeToolboxResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v (%s)", err, rr.Body.String())
	}
	return payload
}

func TestListToolboxes_SummarizesCountsAndAssignments(t *testing.T) {
	handler, ws, created := newToolboxHandlerFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/toolboxes", nil)
	req.SetPathValue("workspaceID", ws.ID)
	rr := httptest.NewRecorder()
	handler.ListToolboxes(rr, req)

	payload := decodeToolboxResponse(t, rr)
	toolboxes, _ := payload["toolboxes"].([]any)
	if len(toolboxes) != 1 {
		t.Fatalf("expected one toolbox, got %v", payload["toolboxes"])
	}
	summary, _ := toolboxes[0].(map[string]any)
	if summary["id"] != created.ID {
		t.Fatalf("expected the created toolbox, got %v", summary["id"])
	}
	if summary["skill_count"].(float64) != 1 || summary["operation_count"].(float64) != 1 {
		t.Fatalf("expected 1 skill and 1 operation, got %v / %v", summary["skill_count"], summary["operation_count"])
	}
	assigned, _ := summary["assigned_instance_ids"].([]any)
	if len(assigned) != 1 || assigned[0] != "inst-1" {
		t.Fatalf("expected the assignment to be reported by INSTANCE id, got %v", assigned)
	}
}

func TestGetToolboxByID_ResolvesAHistoricalVersion(t *testing.T) {
	handler, ws, created := newToolboxHandlerFixture(t)
	if _, err := ws.SaveToolboxVersion(created.ID, nil, nil, ToolboxProvenanceUser, "tester"); err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}
	if err := handler.store.Save(ws); err != nil {
		t.Fatalf("store.Save() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/toolboxes/"+created.ID+"?version=1", nil)
	req.SetPathValue("workspaceID", ws.ID)
	req.SetPathValue("toolboxID", created.ID)
	rr := httptest.NewRecorder()
	handler.GetToolboxByID(rr, req)

	payload := decodeToolboxResponse(t, rr)
	recipe, _ := payload["recipe"].(map[string]any)
	if recipe["version"].(float64) != 1 {
		t.Fatalf("expected version 1 to be returned, got %v", recipe["version"])
	}
	skills, _ := recipe["skills"].([]any)
	if len(skills) != 1 {
		t.Fatalf("expected the historical version to keep its original contents, got %v", skills)
	}
	versions, _ := payload["available_versions"].([]any)
	if len(versions) != 2 {
		t.Fatalf("expected both versions to be listed, got %v", versions)
	}
}

func TestGetAgentToolbox_ReportsProvenanceAndUnassignedInstances(t *testing.T) {
	handler, ws, created := newToolboxHandlerFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/agent-toolboxes/inst-1", nil)
	req.SetPathValue("workspaceID", ws.ID)
	req.SetPathValue("agentInstanceID", "inst-1")
	rr := httptest.NewRecorder()
	handler.GetAgentToolbox(rr, req)

	view, _ := decodeToolboxResponse(t, rr)["agent_toolbox"].(map[string]any)
	if view["assigned"] != true || view["toolbox_id"] != created.ID {
		t.Fatalf("expected the pinned toolbox to be reported, got %v", view)
	}
	skills, _ := view["skills"].([]any)
	first, _ := skills[0].(map[string]any)
	if first["source"] != ToolboxSourceWorkspaceProvided || first["binding_id"] != "sb-1" {
		t.Fatalf("expected the exact skill source to be reported, got %v", first)
	}
	if first["available"] != true {
		t.Fatalf("expected an enabled binding to report as available, got %v", first["available"])
	}

	// The second instance is deliberately unmigrated.
	unassignedReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/agent-toolboxes/inst-2", nil)
	unassignedReq.SetPathValue("workspaceID", ws.ID)
	unassignedReq.SetPathValue("agentInstanceID", "inst-2")
	unassignedRR := httptest.NewRecorder()
	handler.GetAgentToolbox(unassignedRR, unassignedReq)

	unassigned, _ := decodeToolboxResponse(t, unassignedRR)["agent_toolbox"].(map[string]any)
	if unassigned["assigned"] != false {
		t.Fatalf("expected an unmigrated instance to be reported as unassigned, got %v", unassigned)
	}
}

func TestGetAgentToolbox_RejectsAnInstanceOutsideTheWorkspace(t *testing.T) {
	handler, ws, _ := newToolboxHandlerFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/agent-toolboxes/not-here", nil)
	req.SetPathValue("workspaceID", ws.ID)
	req.SetPathValue("agentInstanceID", "not-here")
	rr := httptest.NewRecorder()
	handler.GetAgentToolbox(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign instance, got %d", rr.Code)
	}
}

// FR-26/FR-27: the two selections live in different records and share no write
// path, so neither operation can reach the other.
func TestToolboxIsolation_WorkspaceEditsNeverTouchTheGlobalDefaultToolbox(t *testing.T) {
	_, ws, created := newToolboxHandlerFixture(t)

	globalAgent := &agent.Agent{DefaultToolbox: &types.AgentDefaultToolbox{
		Name:    types.DefaultToolboxName,
		Version: 3,
		Skills:  []types.DefaultToolboxSkillRef{{CapabilityID: "code-review", DisplayName: "code-review"}},
	}}
	before := globalAgent.DefaultToolbox.Clone()

	if _, err := ws.SaveToolboxVersion(created.ID,
		[]ToolboxSkillRef{{CapabilityID: "testing", DisplayName: "testing", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-1"}},
		[]ToolboxMCPRef{{BindingID: "mb-1", AllowedTools: []string{"read_note", "write_note"}}},
		ToolboxProvenanceUser, "tester"); err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-2", ToolboxID: created.ID}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}

	if globalAgent.DefaultToolbox.Version != before.Version || len(globalAgent.DefaultToolbox.Skills) != len(before.Skills) {
		t.Fatalf("expected workspace toolbox edits to leave the global default toolbox untouched, got %+v", globalAgent.DefaultToolbox)
	}
	if globalAgent.DefaultToolbox.Skills[0].CapabilityID != "code-review" {
		t.Fatalf("expected the global selection to be unchanged, got %q", globalAgent.DefaultToolbox.Skills[0].CapabilityID)
	}
}

// The mirror invariant: editing the Default Toolbox changes no workspace
// assignment (FR-27).
func TestToolboxIsolation_DefaultToolboxEditsNeverTouchWorkspaceAssignments(t *testing.T) {
	_, ws, created := newToolboxHandlerFixture(t)
	before, _ := ws.GetToolboxAssignment("inst-1")

	globalAgent := &agent.Agent{}
	globalAgent.InitializeDefaultToolbox()
	if err := globalAgent.DefaultToolbox.SetSkills([]types.DefaultToolboxSkillRef{{CapabilityID: "mac-automation"}}); err != nil {
		t.Fatalf("SetSkills() error = %v", err)
	}

	after, ok := ws.GetToolboxAssignment("inst-1")
	if !ok || after.ToolboxID != created.ID || after.ToolboxVersion != before.ToolboxVersion {
		t.Fatalf("expected the workspace assignment to be untouched, got %+v", after)
	}
}
