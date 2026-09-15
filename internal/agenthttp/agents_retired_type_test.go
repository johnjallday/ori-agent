package agenthttp

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestCreateAgent_IgnoresRetiredTypeKey proves an API client that still posts
// the retired "type" key keeps working, and that no agent response echoes it.
func TestCreateAgent_IgnoresRetiredTypeKey(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	rr := ts.doRequest(t, http.MethodPost, "/api/agents", map[string]any{
		"name":  "legacy-client-agent",
		"type":  "tool-calling",
		"model": "gpt-4o-mini",
	})
	assertStatus(t, rr, http.StatusOK)
	assertNoTypeKey(t, "create response", rr.Body.Bytes())

	ag, ok := ts.store.GetAgent("legacy-client-agent")
	if !ok || ag == nil || ag.Settings.Model != "gpt-4o-mini" {
		t.Fatalf("expected the agent to be created with its model, got %+v", ag)
	}

	for _, path := range []string{
		"/api/agents?name=legacy-client-agent",
		"/api/agents/legacy-client-agent/detail",
	} {
		rr = ts.doRequest(t, http.MethodGet, path, nil)
		assertStatus(t, rr, http.StatusOK)
		assertNoTypeKey(t, path, rr.Body.Bytes())
	}

	for _, path := range []string{"/api/agents", "/api/agents/dashboard/list"} {
		rr = ts.doRequest(t, http.MethodGet, path, nil)
		assertStatus(t, rr, http.StatusOK)
		var list struct {
			Agents []json.RawMessage `json:"agents"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		if len(list.Agents) == 0 {
			t.Fatalf("%s: expected at least one agent", path)
		}
		for _, row := range list.Agents {
			assertNoTypeKey(t, path, row)
		}
	}
}

func assertNoTypeKey(t *testing.T, label string, body []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("%s: decode object: %v (%s)", label, err, body)
	}
	if value, has := fields["type"]; has {
		t.Errorf("%s: response still carries type=%s", label, value)
	}
}
