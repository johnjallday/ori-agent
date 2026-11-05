package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/store"
)

// The inspection is reachable through the real mux, ahead of the /detail
// substring route (an agent name containing "detail" must not be misrouted),
// and the server wires the read-only tracking reader.
func TestSettingsOwnershipRouteIsWiredAndReadOnly(t *testing.T) {
	srv := newRoutesTestServer(t)
	handler := srv.Handler()
	if err := srv.Storage.AgentStore.CreateAgent("Detail Writer", &store.CreateAgentConfig{Model: "route-model"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents/Detail%20Writer/settings-ownership", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings-ownership = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Agent      string `json:"agent"`
		Definition struct {
			Source string `json:"source"`
			Fields []struct {
				Key   string `json:"key"`
				Value any    `json:"value"`
			} `json:"fields"`
		} `json:"definition"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Agent != "Detail Writer" || body.Definition.Source != "library" || len(body.Definition.Fields) == 0 {
		t.Fatalf("unexpected projection: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/agents/Detail%20Writer/settings-ownership", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST settings-ownership = %d", rec.Code)
	}

	if _, ok := srv.Storage.AgentEditCarrier.(agenthttp.CopyTrackingReader); !ok {
		t.Fatalf("AgentEditCarrier %T does not provide copy tracking", srv.Storage.AgentEditCarrier)
	}
}
