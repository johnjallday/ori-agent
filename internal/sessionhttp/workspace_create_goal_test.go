package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func writeGoalTemplate(t *testing.T, libDir, id, goal string) {
	t.Helper()
	tplDir := filepath.Join(libDir, id)
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"Goal Template","goal":"` + goal + `","starter_tasks":[{"description":"Draft a style guide"}]}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

// createdGoal posts a create request and returns the new workspace's Mission
// and legacy bootstrap goal as stored.
func createdGoal(t *testing.T, handler *Handler, body string) (mission, bootstrapGoal string) {
	t.Helper()
	w, resp := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	ws, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get: %v", err)
	}
	stored, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("store.GetWorkspace: %v", err)
	}
	return ws.Mission, workspaceBootstrapStringValue(stored.SharedData, "goal")
}

func TestCreateWorkspaceGoal(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeGoalTemplate(t, handler.templatesRootResolver(), "goal-template", "Publish on-brand content weekly.")

	cases := []struct {
		name          string
		body          string
		wantMission   string
		wantBootstrap string
	}{
		{
			name:          "modal goal wins over the description",
			body:          `{"name":"Brand Desk","description":"Brand assets and posts","mission":"Keep the brand voice consistent","workspace_bootstrap":{"goal":"Brand assets and posts"}}`,
			wantMission:   "Keep the brand voice consistent",
			wantBootstrap: "Keep the brand voice consistent",
		},
		{
			name:          "template goal applies when the caller sends none",
			body:          `{"name":"Content Desk","template_id":"goal-template"}`,
			wantMission:   "Publish on-brand content weekly.",
			wantBootstrap: "Publish on-brand content weekly.",
		},
		{
			name:        "an explicit empty goal is allowed even with a template goal",
			body:        `{"name":"Empty Goal Desk","template_id":"goal-template","mission":""}`,
			wantMission: "",
		},
		{
			name:          "a legacy bootstrap-only caller still gets a goal",
			body:          `{"name":"Legacy Desk","workspace_bootstrap":{"goal":"Ship the launch"}}`,
			wantMission:   "Ship the launch",
			wantBootstrap: "Ship the launch",
		},
		{
			name:        "explicit goal beats template and bootstrap",
			body:        `{"name":"Explicit Desk","template_id":"goal-template","mission":"  My outcome 日本語  ","workspace_bootstrap":{"goal":"Old fallback"}}`,
			wantMission: "My outcome 日本語", wantBootstrap: "My outcome 日本語",
		},
		{
			name:        "template beats legacy bootstrap",
			body:        `{"name":"Default Desk","template_id":"goal-template","workspace_bootstrap":{"goal":"Old fallback"}}`,
			wantMission: "Publish on-brand content weekly.", wantBootstrap: "Publish on-brand content weekly.",
		},
		{
			name:        "null mission leaves template precedence intact",
			body:        `{"name":"Null Desk","template_id":"goal-template","mission":null}`,
			wantMission: "Publish on-brand content weekly.", wantBootstrap: "Publish on-brand content weekly.",
		},
		{
			name: "explicit whitespace clears even the bootstrap mirror",
			body: `{"name":"Cleared Desk","template_id":"goal-template","mission":" \n ","workspace_bootstrap":{"goal":"Old fallback","context":"Keep context"}}`,
		},
		{
			name: "no goal anywhere leaves Mission empty",
			body: `{"name":"Plain Desk","description":"Just a description"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mission, bootstrap := createdGoal(t, handler, tc.body)
			if mission != tc.wantMission {
				t.Fatalf("Mission = %q, want %q", mission, tc.wantMission)
			}
			if bootstrap != tc.wantBootstrap {
				t.Fatalf("bootstrap goal = %q, want %q", bootstrap, tc.wantBootstrap)
			}
		})
	}
}

func TestGroupCreationDoesNotAssignWorkspaceGoal(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	var body map[string]any
	if err := json.Unmarshal([]byte(groupRosterCreateBody(t, handler, "Group Desk", "")), &body); err != nil {
		t.Fatal(err)
	}
	body["mission"] = "Ignore for group"
	body["workspace_bootstrap"] = map[string]any{"goal": "Ignore legacy too"}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	mission, bootstrap := createdGoal(t, handler, string(data))
	if mission != "" || bootstrap != "" {
		t.Fatalf("group acquired a workspace goal: %q / %q", mission, bootstrap)
	}
}

// Assistant-built workspaces go through the same create path, so a blueprint's
// goal reaches them: template setups (Personal HQ, Email Ops) post only a name
// and a template id, and folder offers post a name, a blueprint and an offer.
func TestAssistantBuiltWorkspacesInheritTheBlueprintGoal(t *testing.T) {
	handler, libDir, cleanup := capabilityTemplateEnv(t)
	defer cleanup()
	writeCapabilityTemplate(t, libDir, "goal-blueprint", `{
		"name": "Goal Blueprint",
		"goal": "Keep the newsletter on schedule.",
		"agents": []
	}`)

	fromTemplate, err := handler.CreateFromTemplate(context.Background(), "Newsletter", "goal-blueprint")
	if err != nil {
		t.Fatal(err)
	}
	if ws, err := handler.workspaceStore.Get(fromTemplate); err != nil || ws.Mission != "Keep the newsletter on schedule." {
		t.Fatalf("template setup Mission = %q, err = %v", ws.Mission, err)
	}

	fromOffer, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{
		Name: "Newsletter Folder", TemplateID: "goal-blueprint", OfferID: "offer-goal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws, err := handler.workspaceStore.Get(fromOffer); err != nil || ws.Mission != "Keep the newsletter on schedule." {
		t.Fatalf("folder offer Mission = %q, err = %v", ws.Mission, err)
	}

	// A folder offer whose shape has no blueprint has no goal to inherit.
	blank, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{Name: "Loose Notes", OfferID: "offer-blank"})
	if err != nil {
		t.Fatal(err)
	}
	if ws, err := handler.workspaceStore.Get(blank); err != nil || ws.Mission != "" {
		t.Fatalf("blank folder offer Mission = %q, err = %v", ws.Mission, err)
	}
}

func TestCreateWorkspaceInheritsEveryActiveStockGoalWithoutRunning(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	library := handler.templatesRootResolver()
	if err := projecttemplates.EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	templates, err := projecttemplates.ListLibrary(library)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, tpl := range templates {
		if !tpl.Builtin {
			continue
		}
		count++
		t.Run(tpl.ID, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"name": "Goal " + tpl.Name, "template_id": tpl.ID})
			if err != nil {
				t.Fatal(err)
			}
			w, resp := postCreateWorkspace(t, handler, string(body))
			if w.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			id := resp["folder"].(map[string]any)["id"].(string)
			ws, err := handler.workspaceStore.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := handler.store.GetWorkspace(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if tpl.Goal == "" || ws.Mission != tpl.Goal || workspaceBootstrapStringValue(stored.SharedData, "goal") != tpl.Goal {
				t.Fatalf("stock goal did not reach canonical and legacy storage: %q / %q", tpl.Goal, ws.Mission)
			}
			if ws.MissionEnabled || ws.Cadence != nil || ws.MissionExecutionCount != 0 || ws.LastMissionRunAt != nil || len(ws.MissionRuns) != 0 || ws.GoalFirstRunState != "" {
				t.Fatal("creation must store intent, not accept or execute a goal run")
			}
			if !ws.GoalFirstRunPending {
				t.Fatal("creation must retain first-open intent for admission to evaluate")
			}
			if tpl.ID == "code-project" && !workspace.GoalRequiresReadConsent(ws) {
				t.Fatal("the Code default must retain its separate read-consent boundary")
			}
		})
	}
	if count != 12 {
		t.Fatalf("created %d active stock templates, want 12", count)
	}
}

func TestCreateWorkspaceGoalNeverEnablesOrRunsTheMission(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, resp := postCreateWorkspace(t, handler, `{"name":"Quiet Desk","mission":"Keep the brand voice consistent"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	ws, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.MissionEnabled || ws.Cadence != nil || ws.MissionExecutionCount != 0 || ws.LastMissionRunAt != nil {
		t.Fatalf("create must only store the goal: enabled=%v cadence=%v runs=%d last=%v",
			ws.MissionEnabled, ws.Cadence, ws.MissionExecutionCount, ws.LastMissionRunAt)
	}
	// FR25: the first goal run waits for the first open.
	if !ws.GoalFirstRunPending {
		t.Fatal("a workspace created with a goal must mark its first run pending")
	}

	// No goal, no pending run.
	_, resp = postCreateWorkspace(t, handler, `{"name":"Goal-less Desk","mission":""}`)
	folder, _ = resp["folder"].(map[string]any)
	if plain, err := handler.workspaceStore.Get(folder["id"].(string)); err != nil || plain.GoalFirstRunPending {
		t.Fatalf("a workspace created without a goal has nothing pending: %v %v", plain.GoalFirstRunPending, err)
	}
}
