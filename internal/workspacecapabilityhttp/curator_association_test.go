package workspacecapabilityhttp

import (
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCuratorAssociationRequiresOwnerAndCurrentInstance(t *testing.T) {
	for _, tc := range []struct {
		name, owner, body string
		want              int
	}{
		{"explicit recovery", "local", `{"agent_instance_id":"helper"}`, http.StatusOK},
		{"wrong owner", "other", `{"agent_instance_id":"helper"}`, http.StatusNotFound},
		{"foreign instance", "local", `{"agent_instance_id":"foreign"}`, http.StatusInternalServerError},
		{"malformed", "local", `{"agent_instance_id":true}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := ownedWorkspace("one", tc.owner)
			w.AgentInstances = []workspace.AgentInstance{{ID: "helper", Name: "My chosen helper", NodeID: "helper-node"}}
			if _, err := w.AddInstalledCapability(workspace.InstalledCapability{ID: workspace.CapabilityFileJanitor, Version: 1, Source: workspace.InstallSourceBlueprint}); err != nil {
				t.Fatal(err)
			}
			s := newMemStore(w)
			h, _ := newTestHandler(t, s, stubUserProvider{id: "local"})
			res := do(t, newTestMux(t, h), http.MethodPost, "/api/workspaces/one/capabilities/file-janitor/companion", tc.body)
			if res.Code != tc.want {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			got, _ := s.Get(w.ID)
			_, associated := got.FileCurator()
			if associated != (tc.want == http.StatusOK) {
				t.Fatal("unauthorized association persisted or recovery missing")
			}
		})
	}
}
