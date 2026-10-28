package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceCreationReview_FolderTemplateMetadataIsInertAndTruthful(t *testing.T) {
	for _, tc := range []struct {
		name, extra       string
		skeleton, invalid bool
	}{
		{name: "metadata only"},
		{name: "scaffold", skeleton: true},
		{name: "unreadable inputs", extra: `,"inputs":{"schema_version":1,"fields":[{"id":"reference","label":"Reference","type":"url","default":""}]}`, invalid: true},
		{name: "unreadable project connection", extra: `,"project_connection":{"schema_version":999}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
			path := t.TempDir()
			manifest := `{"name":"Folder options","agents":[]` + tc.extra + `}`
			if err := os.WriteFile(filepath.Join(path, "template.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.skeleton {
				if err := os.WriteFile(filepath.Join(path, "seed.txt"), []byte("unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			encoded, err := json.Marshal(map[string]any{"template_path": path})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/workspaces/template-agent-plan", bytes.NewReader(encoded))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.HandleWorkspaces(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("folder metadata: %d %s", w.Code, w.Body)
			}
			var plan templateAgentPlan
			if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			source := plan.FolderTemplate
			if source == nil || source.Path != path || len(source.SourceDigest) != 64 || source.Template.Name != "Folder options" || source.Template.HasSkeleton != tc.skeleton {
				t.Fatalf("missing or invented source: %+v", source)
			}
			if source.Readiness.Creatable() == tc.invalid {
				t.Fatalf("wrong source readiness: %+v", source.Readiness)
			}
			if plan.Revision != finalizeTemplateAgentPlan(plan).Revision {
				t.Fatal("readonly source altered the agent review contract")
			}
			workspaces, err := h.store.ListWorkspaces(t.Context())
			if err != nil || len(workspaces) != 0 || len(h.agentStore.ListAgents()) != 0 {
				t.Fatalf("metadata read created resources: %v %v", workspaces, err)
			}
		})
	}
}

// This is declaration/owner contract coverage, not browser creation acceptance.
// The real normalization and inert operation review read the built-in catalog;
// no provider, profile creation, filesystem grant or domain create is stubbed.
func TestWorkspaceCreationReview_BuiltinTeamDeclarationsRemainInertWithoutModel(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(h.templatesRootResolver()); err != nil {
		t.Fatal(err)
	}
	starterRoot := filepath.Join("..", "projecttemplates", "starter")
	entries, err := os.ReadDir(starterRoot)
	if err != nil {
		t.Fatal(err)
	}
	cases := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cases++
		t.Run(entry.Name(), func(t *testing.T) {
			// #nosec G304 -- this path is enumerated from repository-owned starter fixtures, not request data.
			body, err := os.ReadFile(filepath.Join(starterRoot, entry.Name(), "template.json"))
			if err != nil {
				t.Fatal(err)
			}
			var declaration struct {
				Name   string `json:"name"`
				Agents []struct {
					Name string `json:"name"`
				} `json:"agents"`
			}
			if err := json.Unmarshal(body, &declaration); err != nil {
				t.Fatal(err)
			}
			input, err := json.Marshal(map[string]any{"template_id": entry.Name()})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/workspaces/template-agent-plan", bytes.NewReader(input))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.HandleWorkspaces(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("actual blueprint plan: %d %s", w.Code, w.Body)
			}
			var plan templateAgentPlan
			if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.FolderTemplate != nil {
				t.Fatal("catalog selection acquired an unrelated folder source")
			}
			if plan.Revision == "" || plan.TemplateName != declaration.Name || len(plan.Agents) != len(declaration.Agents) {
				t.Fatalf("declaration was flattened or lost: %+v", plan)
			}
			for index, agent := range declaration.Agents {
				if plan.Agents[index].Name != agent.Name {
					t.Fatalf("declared team member %q became %q", agent.Name, plan.Agents[index].Name)
				}
			}
			reviewCreationOperation(t, h, map[string]any{"name": "Reviewed " + declaration.Name, "template_id": entry.Name()})
			workspaces, err := h.store.ListWorkspaces(t.Context())
			if err != nil || len(workspaces) != 0 || len(h.agentStore.ListAgents()) != 0 {
				t.Fatalf("plan/review created domain resources: %v %v", workspaces, err)
			}
		})
	}
	if cases == 0 {
		t.Fatal("no actual built-in declarations were covered")
	}
}

// Trusted-resolver fixture only: this proves the host review fence, not a
// published plugin's installation/runtime or cross-repository compatibility.
func TestWorkspaceCreationReview_NeutralPluginDeclarationCannotChangeUnderAnOperation(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, h, "neutral", `{"name":"Neutral blueprint","agents":[]}`)
	declaration, err := h.resolveProjectTemplate("neutral", "")
	if err != nil {
		t.Fatal(err)
	}
	declaration.ID = "plugin:owner-plugin:neutral"
	declaration.PluginOwner = &workspace.PluginTemplateOwner{PluginID: "owner-plugin", PluginVersion: "1.0.0", BlueprintID: "neutral", BlueprintVersion: 1}
	installed := installedPluginRecord(true)
	installed.ResolvedBlueprints = []plugin.ResolvedBlueprint{{ID: "neutral", QualifiedID: declaration.ID, Version: 1}}
	h.SetInstalledPluginLister(matrixPluginLister{installed: []plugin.InstalledPlugin{installed}})
	h.SetProjectTemplateResolver(func(string, string) (projecttemplates.Template, error) { return declaration, nil })
	if readiness := h.revalidateBlueprintReadiness(declaration); !readiness.Creatable() {
		t.Fatalf("incoherent initial fixture: %+v", readiness)
	}
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "Reviewed neutral project", "template_id": declaration.ID, "create_template_agents": false})
	declaration.PluginOwner = &workspace.PluginTemplateOwner{PluginID: "owner-plugin", PluginVersion: "1.0.0", BlueprintID: "neutral", BlueprintVersion: 2}
	installed.ResolvedBlueprints[0].Version = 2
	installed.Generation++
	h.SetInstalledPluginLister(matrixPluginLister{installed: []plugin.InstalledPlugin{installed}})
	if readiness := h.revalidateBlueprintReadiness(declaration); !readiness.Creatable() {
		t.Fatalf("source fence must be tested independently of missing dependencies: %+v", readiness)
	}
	w, body := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusConflict || body["code"] != "creation_blueprint_changed" {
		t.Fatalf("changed plugin declaration was not refused by its source fence: %d %s", w.Code, w.Body)
	}
	assertCreationOperationState(t, h, id, "refused")
	read, observed := creationOperationRequest(t, h, http.MethodGet, "/"+id, nil)
	refusal, ok := observed["refusal"].(map[string]any)
	if read.Code != http.StatusOK || !ok || refusal["code"] != "creation_blueprint_changed" {
		t.Fatalf("read-only recovery lost the original refusal: %d %v", read.Code, observed)
	}
	workspaces, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(workspaces) != 0 || len(h.agentStore.ListAgents()) != 0 {
		t.Fatalf("plugin source refusal created resources: %v %v", workspaces, err)
	}
}
