package sessionhttp

import (
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCreatedCuratorRecordsActualAttachment(t *testing.T) {
	for _, tc := range []struct{ name, fields, want string }{
		{"default", "", "File Curator"},
		{"renamed", `,"template_agent_overrides":[{"index":0,"name":"My helper"}]`, "My helper"},
		{"role renamed", `,"role_staffing":[{"role_id":"file-curator","mode":"create","name":"My helper"}]`, "My helper"},
		{"role assigned", `,"role_staffing":[{"role_id":"file-curator","mode":"assign","name":"Saved"}]`, "Saved"},
		{"excluded", `,"create_template_agents":false,"existing_agent_names":["Saved"]`, ""},
		{"empty roles", `,"role_staffing":[]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, root, cleanup := capabilityTemplateEnv(t)
			defer cleanup()
			if err := h.agentStore.CreateAgent("Saved", &store.CreateAgentConfig{SystemPrompt: "User-owned instructions"}); err != nil {
				t.Fatal(err)
			}
			writeCapabilityTemplate(t, root, "file-janitor", `{"name":"File Janitor","builtin":true,"capabilities":[{"id":"file-janitor"}],"agents":[{"name":"File Curator","role":"specialist"}]}`)
			res, body := postCreateWorkspace(t, h, `{"name":"Curator test","template_id":"file-janitor"`+tc.fields+`}`)
			if res.Code != http.StatusCreated {
				t.Fatalf("%d %s", res.Code, res.Body.String())
			}
			id := body["folder"].(map[string]any)["id"].(string)
			w, err := h.workspaceTaskStore.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := w.FileCurator()
			if tc.want == "" {
				if ok {
					t.Fatal("excluded role associated an unrelated agent")
				}
				return
			}
			if !ok || got.Name != tc.want {
				t.Fatalf("companion = %+v (%v), instances=%+v installs=%+v", got, ok, w.AgentInstances, w.InstalledCapabilities)
			}
			saved, _ := h.agentStore.GetAgent("Saved")
			if saved.Settings.SystemPrompt != "User-owned instructions" {
				t.Fatal("shared definition changed")
			}
		})
	}
}

func TestTrustedCuratorSeedUsesSlotNotDisplayNameAcrossWorkspaces(t *testing.T) {
	h, _, cleanup := capabilityTemplateEnv(t)
	defer cleanup()
	tpl := projecttemplates.Template{ID: "file-janitor", Builtin: true, Agents: []projecttemplates.AgentSpec{{Name: "Renamed"}}}
	for _, id := range []string{"one", "two"} {
		w := &session.Workspace{ID: id}
		seed := h.seedTemplateAgents(w, tpl)
		if len(seed.CuratorResources) != 1 || seed.CuratorResources[0].ID != w.AgentInstances[0].ID {
			t.Fatalf("seed = %+v", seed)
		}
		if !seed.CuratorResources[0].Shared {
			t.Fatal("entry attachment must survive removal")
		}
	}
	w := &session.Workspace{ID: "unrelated"}
	tpl.ID = "other"
	if seed := h.seedTemplateAgents(w, tpl); len(seed.CuratorResources) != 0 {
		t.Fatal("same-name unrelated roster claimed companion")
	}
}

func TestStrictReviewCuratorRecordsRenamedReuse(t *testing.T) {
	h, _, cleanup := capabilityTemplateEnv(t)
	defer cleanup()
	if err := h.agentStore.CreateAgent("Mine", nil); err != nil {
		t.Fatal(err)
	}
	tpl := projecttemplates.Template{ID: "file-janitor", Builtin: true, Agents: []projecttemplates.AgentSpec{{Name: "Mine"}}}
	w := &session.Workspace{ID: "strict"}
	seed, err := h.seedTemplateAgentsStrict(w, tpl, tpl, templateAgentReview{Expectations: []templateAgentReviewExpectation{{Index: 0, Name: "Mine", Action: "reuse"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.CuratorResources) != 1 || !seed.CuratorResources[0].Shared || seed.CuratorResources[0].Kind != workspace.ResourceCompanionAgent {
		t.Fatalf("seed = %+v", seed)
	}
}
