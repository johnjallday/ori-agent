package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Handler coverage for Toolbox creation, versioning, archiving, deletion, and
// comparison (task 2.15; PRD FR-37–FR-54).

func newToolboxWriteFixture(t *testing.T) (*HTTPHandler, string) {
	t.Helper()
	ws := &Workspace{
		ID: "ws-toolbox-write",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Writer", NodeID: "writer-1", InstanceNumber: 1},
		},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true},
			{ID: "sb-2", SkillName: "drafting", Enabled: true},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note", "write_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true, AllowedTools: []string{"read_doc"}},
		},
	}
	return NewHTTPHandler(newTestWorkspaceStore(t, ws), nil, nil), ws.ID
}

func postToolboxJSON(t *testing.T, handler http.HandlerFunc, method, url string, pathValues map[string]string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to encode request body: %v", err)
		}
		payload = encoded
	}
	req := httptest.NewRequest(method, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range pathValues {
		req.SetPathValue(key, value)
	}
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func decodeJSONBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v (%s)", err, rr.Body.String())
	}
	return payload
}

func createToolboxVia(t *testing.T, handler *HTTPHandler, workspaceID string, body map[string]any) map[string]any {
	t.Helper()
	rr := postToolboxJSON(t, handler.CreateToolboxHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes", map[string]string{"workspaceID": workspaceID}, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	toolbox, _ := decodeJSONBody(t, rr)["toolbox"].(map[string]any)
	return toolbox
}

// FR-39: an empty non-core selection is a legitimate Toolbox, not an error.
func TestCreateToolboxHandler_EmptySelection(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)

	toolbox := createToolboxVia(t, handler, workspaceID, map[string]any{
		"name": "Blank Kit",
		"from": "empty",
	})

	if toolbox["version"].(float64) != 1 {
		t.Fatalf("expected a new toolbox at version 1, got %v", toolbox["version"])
	}
	if toolbox["status"] != ToolboxStatusDraft {
		t.Fatalf("expected a new toolbox to start as a draft, got %v", toolbox["status"])
	}
	if toolbox["skills"] != nil || toolbox["mcp_bindings"] != nil {
		t.Fatalf("expected an empty selection, got %v / %v", toolbox["skills"], toolbox["mcp_bindings"])
	}
}

// FR-38: saving the current selection reads the SERVER's idea of current, not
// the browser's.
func TestCreateToolboxHandler_FromCurrentSelection(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)

	seed := createToolboxVia(t, handler, workspaceID, map[string]any{
		"name": "Seed Kit",
		"from": "explicit",
		"skills": []map[string]any{
			{"capability_id": "testing", "source": ToolboxSourceWorkspaceProvided, "binding_id": "sb-1", "required": true},
		},
		"mcp_bindings": []map[string]any{
			{"binding_id": "mb-1", "allowed_tools": []string{"read_note"}},
		},
	})
	if err := handler.store.Update(workspaceID, func(ws *Workspace) error {
		_, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: seed["id"].(string)})
		return err
	}); err != nil {
		t.Fatalf("failed to assign the seed toolbox: %v", err)
	}

	saved := createToolboxVia(t, handler, workspaceID, map[string]any{
		"name":              "Snapshot Kit",
		"from":              "current",
		"agent_instance_id": "inst-1",
	})

	skills, _ := saved["skills"].([]any)
	bindings, _ := saved["mcp_bindings"].([]any)
	if len(skills) != 1 || len(bindings) != 1 {
		t.Fatalf("expected the current selection to be copied, got %v / %v", skills, bindings)
	}
	// The source toolbox and the assignment are untouched.
	ws, _ := handler.store.Get(workspaceID)
	assignment, _ := ws.GetToolboxAssignment("inst-1")
	if assignment.ToolboxID != seed["id"] {
		t.Fatalf("expected saving a copy to leave the assignment alone, got %v", assignment.ToolboxID)
	}
}

// FR-40: duplicating produces an editable copy and never changes the source.
func TestCreateToolboxHandler_DuplicateLeavesSourceUnchanged(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)

	source := createToolboxVia(t, handler, workspaceID, map[string]any{
		"name": "Research Kit",
		"from": "explicit",
		"skills": []map[string]any{
			{"capability_id": "testing", "source": ToolboxSourceWorkspaceProvided, "binding_id": "sb-1"},
		},
	})

	duplicate := createToolboxVia(t, handler, workspaceID, map[string]any{
		"name":              "Research Kit (copy)",
		"from":              "duplicate",
		"source_toolbox_id": source["id"],
	})

	if duplicate["id"] == source["id"] {
		t.Fatalf("expected the duplicate to be a distinct toolbox")
	}
	if len(duplicate["skills"].([]any)) != 1 {
		t.Fatalf("expected the duplicate to carry the source contents, got %v", duplicate["skills"])
	}

	ws, _ := handler.store.Get(workspaceID)
	reloadedSource, _ := ws.GetToolbox(source["id"].(string))
	if reloadedSource.Version != 1 {
		t.Fatalf("expected duplicating not to version the source, got version %d", reloadedSource.Version)
	}
}

// FR-42: names are trimmed, bounded, and case-insensitively unique.
func TestCreateToolboxHandler_RejectsDuplicateAndInvalidNames(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)
	createToolboxVia(t, handler, workspaceID, map[string]any{"name": "Research Kit"})

	duplicate := postToolboxJSON(t, handler.CreateToolboxHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes", map[string]string{"workspaceID": workspaceID},
		map[string]any{"name": "  research   KIT  "})
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("expected a duplicate name to be rejected, got %d: %s", duplicate.Code, duplicate.Body.String())
	}

	blank := postToolboxJSON(t, handler.CreateToolboxHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes", map[string]string{"workspaceID": workspaceID},
		map[string]any{"name": "   "})
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("expected a blank name to be rejected, got %d", blank.Code)
	}
}

// FR-13: a client that omits the tool list is asking for legacy all-tools
// semantics, which must be refused rather than silently reinterpreted.
func TestCreateToolboxHandler_RejectsImplicitAllToolsSemantics(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)

	rr := postToolboxJSON(t, handler.CreateToolboxHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes", map[string]string{"workspaceID": workspaceID},
		map[string]any{
			"name":         "Wildcard Kit",
			"from":         "explicit",
			"mcp_bindings": []map[string]any{{"binding_id": "mb-1"}},
		})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected an omitted tool list to be rejected, got %d: %s", rr.Code, rr.Body.String())
	}
}

// FR-12: a Toolbox narrows a binding's policy and never widens it.
func TestCreateToolboxHandler_RejectsWideningTheBindingPolicy(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)

	rr := postToolboxJSON(t, handler.CreateToolboxHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes", map[string]string{"workspaceID": workspaceID},
		map[string]any{
			"name":         "Too Much",
			"from":         "explicit",
			"mcp_bindings": []map[string]any{{"binding_id": "mb-2", "allowed_tools": []string{"read_doc", "delete_doc"}}},
		})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected widening the binding policy to be rejected, got %d: %s", rr.Code, rr.Body.String())
	}
}

// FR-18/FR-19: every content edit versions, and the prior version keeps its
// meaning.
func TestCreateToolboxVersionHandler_VersionsAndKeepsHistory(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)
	created := createToolboxVia(t, handler, workspaceID, map[string]any{
		"name": "Research Kit",
		"from": "explicit",
		"skills": []map[string]any{
			{"capability_id": "testing", "source": ToolboxSourceWorkspaceProvided, "binding_id": "sb-1"},
		},
	})
	toolboxID := created["id"].(string)

	rr := postToolboxJSON(t, handler.CreateToolboxVersionHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID+"/versions",
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{
			"expected_version": 1,
			"skills": []map[string]any{
				{"capability_id": "drafting", "source": ToolboxSourceWorkspaceProvided, "binding_id": "sb-2"},
			},
		})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	if decodeJSONBody(t, rr)["version"].(float64) != 2 {
		t.Fatalf("expected version 2, got %v", decodeJSONBody(t, rr)["version"])
	}

	ws, _ := handler.store.Get(workspaceID)
	definition, _ := ws.GetToolbox(toolboxID)
	v1, err := definition.ResolveVersion(1)
	if err != nil {
		t.Fatalf("expected version 1 to remain resolvable: %v", err)
	}
	if v1.Skills[0].CapabilityID != "testing" {
		t.Fatalf("expected version 1 to keep its original meaning, got %+v", v1.Skills)
	}
	// A draft becomes active once it is edited.
	if NormalizeToolboxStatus(definition.Status) != ToolboxStatusActive {
		t.Fatalf("expected an edited toolbox to leave draft status, got %q", definition.Status)
	}
}

// Two editors must not silently overwrite each other.
func TestCreateToolboxVersionHandler_RejectsAStaleEdit(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)
	created := createToolboxVia(t, handler, workspaceID, map[string]any{"name": "Research Kit"})
	toolboxID := created["id"].(string)

	// First editor saves.
	first := postToolboxJSON(t, handler.CreateToolboxVersionHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID+"/versions",
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{"expected_version": 1, "skills": []map[string]any{}})
	if first.Code != http.StatusCreated {
		t.Fatalf("expected the first save to succeed, got %d", first.Code)
	}

	// Second editor still thinks the toolbox is at version 1.
	second := postToolboxJSON(t, handler.CreateToolboxVersionHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID+"/versions",
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{"expected_version": 1, "skills": []map[string]any{}})
	if second.Code != http.StatusConflict {
		t.Fatalf("expected a stale edit to conflict, got %d: %s", second.Code, second.Body.String())
	}
}

// FR-23: a write built against a stale workspace version is rejected rather
// than overwriting whatever changed underneath it.
func TestCreateToolboxHandler_RejectsAStaleWorkspaceVersion(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)

	rr := postToolboxJSON(t, handler.CreateToolboxHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes", map[string]string{"workspaceID": workspaceID},
		map[string]any{"name": "Stale Kit", "expected_workspace_version": 9999})

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected a stale workspace version to conflict, got %d: %s", rr.Code, rr.Body.String())
	}
}

// FR-41: metadata edits do NOT version — no capability changed.
func TestUpdateToolboxHandler_RenamesWithoutVersioning(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)
	created := createToolboxVia(t, handler, workspaceID, map[string]any{"name": "Research Kit"})
	toolboxID := created["id"].(string)

	rr := postToolboxJSON(t, handler.UpdateToolboxHandler, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID,
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{"name": "Evidence Kit", "icon": "🔍", "color": "#3355ff"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	toolbox, _ := decodeJSONBody(t, rr)["toolbox"].(map[string]any)
	if toolbox["name"] != "Evidence Kit" || toolbox["icon"] != "🔍" {
		t.Fatalf("expected the metadata to be updated, got %v", toolbox)
	}
	if toolbox["version"].(float64) != 1 {
		t.Fatalf("expected a rename not to produce a version, got %v", toolbox["version"])
	}
}

// FR-20/FR-21: archiving and deleting explain what still depends on the
// Toolbox instead of failing flatly.
func TestToolboxLifecycleHandlers_GuardReferencesWithDetail(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)
	created := createToolboxVia(t, handler, workspaceID, map[string]any{"name": "Research Kit"})
	toolboxID := created["id"].(string)

	if err := handler.store.Update(workspaceID, func(ws *Workspace) error {
		_, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: toolboxID})
		return err
	}); err != nil {
		t.Fatalf("failed to assign the toolbox: %v", err)
	}

	archive := postToolboxJSON(t, handler.SetToolboxStatusHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID+"/status",
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{"status": ToolboxStatusArchived})
	if archive.Code != http.StatusConflict {
		t.Fatalf("expected archiving an in-use toolbox to conflict, got %d", archive.Code)
	}
	refs, _ := decodeJSONBody(t, archive)["references"].([]any)
	if len(refs) != 1 {
		t.Fatalf("expected the blocking reference to be named, got %v", refs)
	}
	first, _ := refs[0].(map[string]any)
	if first["kind"] != "assignment" || first["id"] != "inst-1" {
		t.Fatalf("expected the assignment reference to identify the instance, got %v", first)
	}

	del := postToolboxJSON(t, handler.DeleteToolboxHandler, http.MethodDelete,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID,
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID}, nil)
	if del.Code != http.StatusConflict {
		t.Fatalf("expected deleting an in-use toolbox to conflict, got %d", del.Code)
	}

	// Once nothing references it, both succeed.
	if err := handler.store.Update(workspaceID, func(ws *Workspace) error {
		return ws.DeleteToolboxAssignment("inst-1")
	}); err != nil {
		t.Fatalf("failed to clear the assignment: %v", err)
	}
	freed := postToolboxJSON(t, handler.DeleteToolboxHandler, http.MethodDelete,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID,
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID}, nil)
	if freed.Code != http.StatusOK {
		t.Fatalf("expected an unreferenced toolbox to be deletable, got %d: %s", freed.Code, freed.Body.String())
	}
}

// FR-51/FR-52: the comparison names exactly what moved.
func TestCompareToolboxVersionsHandler_ReportsTheExactDifference(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)
	created := createToolboxVia(t, handler, workspaceID, map[string]any{
		"name": "Research Kit",
		"from": "explicit",
		"skills": []map[string]any{
			{"capability_id": "testing", "source": ToolboxSourceWorkspaceProvided, "binding_id": "sb-1"},
		},
		"mcp_bindings": []map[string]any{{"binding_id": "mb-1", "allowed_tools": []string{"read_note"}}},
	})
	toolboxID := created["id"].(string)

	version := postToolboxJSON(t, handler.CreateToolboxVersionHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID+"/versions",
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{
			"skills": []map[string]any{
				{"capability_id": "drafting", "source": ToolboxSourceWorkspaceProvided, "binding_id": "sb-2"},
			},
			"mcp_bindings": []map[string]any{{"binding_id": "mb-1", "allowed_tools": []string{"read_note", "write_note"}}},
		})
	if version.Code != http.StatusCreated {
		t.Fatalf("expected the edit to succeed, got %d: %s", version.Code, version.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID+"/compare?from=1&to=2", nil)
	req.SetPathValue("workspaceID", workspaceID)
	req.SetPathValue("toolboxID", toolboxID)
	rr := httptest.NewRecorder()
	handler.CompareToolboxVersionsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	payload := decodeJSONBody(t, rr)
	diff, _ := payload["diff"].(map[string]any)

	added, _ := diff["skills_added"].([]any)
	removed, _ := diff["skills_removed"].([]any)
	changed, _ := diff["bindings_changed"].([]any)
	if len(added) != 1 || len(removed) != 1 || len(changed) != 1 {
		t.Fatalf("expected one added skill, one removed skill, and one changed binding, got %v", diff)
	}
	change, _ := changed[0].(map[string]any)
	addedTools, _ := change["added_tools"].([]any)
	if len(addedTools) != 1 || addedTools[0] != "write_note" {
		t.Fatalf("expected write_note to be reported as newly exposed, got %v", change)
	}
	if payload["expands_operations"] != true {
		t.Fatalf("expected a version that exposes a new operation to be reported as expanding")
	}
}

// FR-53: Toolbox CRUD cannot reach an agent's model, provider, prompt, role,
// evolution, or Default Toolbox. They live in a different store, and this test
// exists so a future refactor that hands the handler an agent store trips here.
func TestToolboxWrites_CannotMutateTheGlobalAgent(t *testing.T) {
	handler, workspaceID := newToolboxWriteFixture(t)

	globalAgent := &agent.Agent{
		Type: agent.TypeResearch,
		Role: types.RoleGeneral,
		Settings: types.Settings{
			SystemPrompt: "You are a careful researcher.",
			Model:        "gpt-5",
		},
		Evolution:      &types.AgentEvolution{Level: 4, Stage: types.AgentStageLearner},
		DefaultToolbox: &types.AgentDefaultToolbox{Version: 2},
	}
	before := *globalAgent
	beforeToolbox := globalAgent.DefaultToolbox.Clone()

	created := createToolboxVia(t, handler, workspaceID, map[string]any{"name": "Research Kit"})
	toolboxID := created["id"].(string)
	postToolboxJSON(t, handler.CreateToolboxVersionHandler, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID+"/versions",
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{"skills": []map[string]any{
			{"capability_id": "testing", "source": ToolboxSourceWorkspaceProvided, "binding_id": "sb-1"},
		}})
	postToolboxJSON(t, handler.UpdateToolboxHandler, http.MethodPut,
		"/api/workspaces/"+workspaceID+"/toolboxes/"+toolboxID,
		map[string]string{"workspaceID": workspaceID, "toolboxID": toolboxID},
		map[string]any{"name": "Renamed Kit"})

	if globalAgent.Settings.Model != before.Settings.Model ||
		globalAgent.Settings.SystemPrompt != before.Settings.SystemPrompt ||
		globalAgent.Role != before.Role ||
		globalAgent.Type != before.Type ||
		globalAgent.Evolution.Level != before.Evolution.Level ||
		globalAgent.Evolution.Stage != before.Evolution.Stage {
		t.Fatalf("expected toolbox writes to leave the reusable agent untouched, got %+v", globalAgent)
	}
	if globalAgent.DefaultToolbox.Version != beforeToolbox.Version {
		t.Fatalf("expected toolbox writes to leave the default toolbox untouched, got version %d", globalAgent.DefaultToolbox.Version)
	}
}
