package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assignReq(t *testing.T, h *Handler, agentName, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/agents/"+agentName+"/workspaces", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.AssignWorkspaces(rr, req)
	return rr
}

func assignedCount(t *testing.T, rr *httptest.ResponseRecorder) int {
	t.Helper()
	var resp struct {
		WorkspaceCount int `json:"workspace_count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode assign response: %v (body=%s)", err, rr.Body.String())
	}
	return resp.WorkspaceCount
}

// TestAssignWorkspaces_AddAndRemove reconciles an agent that starts in ws-a into
// {ws-b, ws-c}: ws-a is dropped, ws-b/ws-c are added. The agent is a non-entry
// instance everywhere here (entry is "Lead").
func TestAssignWorkspaces_AddAndRemove(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Lead", "Helper"},
		map[string][]string{
			"ws-a": {"Lead", "Helper"},
			"ws-b": {"Lead"},
			"ws-c": {"Lead"},
		},
	)

	rr := assignReq(t, h, "Helper", `{"workspace_ids":["ws-b","ws-c"]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := assignedCount(t, rr); got != 2 {
		t.Fatalf("expected membership count 2, got %d", got)
	}

	// Verify by re-reading: Helper should now be in ws-b and ws-c, not ws-a.
	for wsID, want := range map[string]bool{"ws-a": false, "ws-b": true, "ws-c": true} {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			t.Fatalf("get %s: %v", wsID, err)
		}
		found := false
		for _, inst := range ws.AgentInstances {
			if inst.Name == "Helper" {
				found = true
			}
		}
		if found != want {
			t.Errorf("ws %s: Helper present=%v, want %v", wsID, found, want)
		}
	}
}

// TestAssignWorkspaces_Idempotent verifies reassigning the same set is a no-op
// that still returns success.
func TestAssignWorkspaces_Idempotent(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Lead", "Helper"},
		map[string][]string{"ws-a": {"Lead", "Helper"}},
	)
	rr := assignReq(t, h, "Helper", `{"workspace_ids":["ws-a"]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := assignedCount(t, rr); got != 1 {
		t.Fatalf("expected count 1, got %d", got)
	}
}

// TestAssignWorkspaces_EmptySetRemovesAll clears membership when the agent is not
// an entry agent anywhere.
func TestAssignWorkspaces_EmptySetRemovesAll(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Lead", "Helper"},
		map[string][]string{"ws-a": {"Lead", "Helper"}, "ws-b": {"Lead", "Helper"}},
	)
	rr := assignReq(t, h, "Helper", `{"workspace_ids":[]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := assignedCount(t, rr); got != 0 {
		t.Fatalf("expected count 0, got %d", got)
	}
}

// TestAssignWorkspaces_EntryAgentRemovalBlocked verifies you cannot unassign an
// agent from a workspace where it is the entry agent.
func TestAssignWorkspaces_EntryAgentRemovalBlocked(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Lead"},
		map[string][]string{"ws-a": {"Lead"}},
	)
	rr := assignReq(t, h, "Lead", `{"workspace_ids":[]}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "entry_agent_removal_blocked") {
		t.Errorf("expected entry_agent_removal_blocked, got %s", rr.Body.String())
	}
}

// TestAssignWorkspaces_UnknownWorkspaceRejected verifies a desired ID that does
// not resolve fails before any mutation.
func TestAssignWorkspaces_UnknownWorkspaceRejected(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Lead", "Helper"},
		map[string][]string{"ws-a": {"Lead", "Helper"}},
	)
	rr := assignReq(t, h, "Helper", `{"workspace_ids":["ws-a","ghost"]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	// ws-a membership must be untouched by the failed request.
	ws, err := h.workspaceStore.Get("ws-a")
	if err != nil {
		t.Fatalf("get ws-a: %v", err)
	}
	found := false
	for _, inst := range ws.AgentInstances {
		if inst.Name == "Helper" {
			found = true
		}
	}
	if !found {
		t.Error("Helper should remain in ws-a after a rejected assignment")
	}
}
