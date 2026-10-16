package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func assertGoalCreationIntent(t *testing.T, handler *Handler, id, goal string) {
	t.Helper()
	ws, err := handler.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	row, err := handler.store.GetWorkspace(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Mission != goal || workspaceBootstrapStringValue(row.SharedData, "goal") != goal {
		t.Fatalf("goal mirrors disagree: %q / %+v", ws.Mission, row.SharedData)
	}
	if ws.MissionEnabled || ws.Cadence != nil || ws.MissionExecutionCount != 0 || len(ws.MissionRuns) != 0 || ws.GoalFirstRunState != "" {
		t.Fatal("creation acquired goal execution state")
	}
}

func TestAssistantAndFolderOfferStockGoalCreation(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	lib := handler.templatesRootResolver()
	if err := projecttemplates.EnsureLibrary(lib); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"writing-project", "code-project", "email-ops"} {
		t.Run(id, func(t *testing.T) {
			tpl, err := projecttemplates.FindLibraryTemplate(lib, id)
			if err != nil {
				t.Fatal(err)
			}
			created, err := handler.CreateFromTemplate(t.Context(), "Assistant "+tpl.Name, id)
			if err != nil {
				t.Fatal(err)
			}
			assertGoalCreationIntent(t, handler, created, tpl.Goal)
			offer, err := handler.CreateFolderOfferWorkspace(t.Context(), FolderOfferWorkspaceRequest{Name: "Offer " + tpl.Name, TemplateID: id, OfferID: "offer-" + id})
			if err != nil {
				t.Fatal(err)
			}
			assertGoalCreationIntent(t, handler, offer, tpl.Goal)
			if id == "code-project" {
				for _, wsID := range []string{created, offer} {
					ws, err := handler.workspaceStore.Get(wsID)
					if err != nil || !workspace.GoalRequiresReadConsent(ws) {
						t.Fatalf("assistant/folder Code lost consent: %v", err)
					}
				}
			}
		})
	}
}

func TestBuildSessionGoalCreationKeepsExplicitIntent(t *testing.T) {
	for _, goal := range []string{"My built outcome", ""} {
		t.Run("goal-"+goal, func(t *testing.T) {
			f := newBuildFixture(t, true)
			files, err := workspace.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			f.handler.SetWorkspaceStore(files)
			f.handler.SetWorkspaceTaskStore(workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(f.handler.store), files))
			lib := t.TempDir()
			f.handler.SetTemplatesRootResolver(func() string { return lib })
			writeGoalTemplate(t, lib, "build-goal", "Blueprint default")
			build := f.start(t)
			status, body := f.do(t, http.MethodPost, "/api/workspaces", map[string]any{"name": "Built Goal", "template_id": "build-goal", "mission": goal, "build_session_id": build.ID})
			if status != http.StatusCreated {
				t.Fatalf("create: %d %+v", status, body)
			}
			id := body["folder"].(map[string]any)["id"].(string)
			assertGoalCreationIntent(t, f.handler, id, goal)
			if f.provider.calls != 0 {
				t.Fatal("creation invoked the assistant model")
			}
		})
	}
}

func TestPlainFolderImportGoalPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mission      *string
		legacy, want string
	}{
		{"explicit", stringPointerForGoal("  My import outcome  "), "Old fallback", "My import outcome"},
		{"empty", stringPointerForGoal(""), "Old fallback", ""},
		{"legacy", nil, "Legacy import goal", "Legacy import goal"},
		{"none", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
			payload, err := json.Marshal(map[string]any{"path": t.TempDir(), "name": "Plain import", "mission": tc.mission, "workspace_bootstrap": map[string]any{"goal": tc.legacy}})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewReader(payload))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.HandleWorkspaces(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("import: %d %s", w.Code, w.Body.String())
			}
			var response struct {
				Folder struct {
					ID string `json:"id"`
				} `json:"folder"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			assertGoalCreationIntent(t, handler, response.Folder.ID, tc.want)
		})
	}
}

func stringPointerForGoal(value string) *string { return &value }

func TestImportedGoalDoesNotInheritFromTemplateProvenance(t *testing.T) {
	for _, goal := range []string{"Preserve saved goal", ""} {
		t.Run("goal-"+goal, func(t *testing.T) {
			handler, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
			if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
				t.Fatal(err)
			}
			ws := exportedWorkspaceFixture("imported-goal", "Imported Goal", "")
			ws.Mission = goal
			ws.GoalFirstRunState = workspace.GoalFirstRunAccepted
			ws.MissionExecutionCount = 1
			ws.TemplateProvenance = &workspace.TemplateProvenance{TemplateID: "code-project", Builtin: true, Version: 2, BehaviorProfile: "software_project"}
			root := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "export"), ws)
			payload, err := json.Marshal(map[string]any{"path": root, "mission": "Do not overwrite restored intent", "template_id": "writing-project"})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewReader(payload))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.HandleWorkspaces(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("import: %d %s", w.Code, w.Body.String())
			}
			got, err := handler.workspaceStore.Get(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Mission != goal || got.GoalFirstRunPending || got.GoalFirstRunState != ws.GoalFirstRunState || got.MissionExecutionCount != 1 || got.MissionEnabled || got.Cadence != nil {
				t.Fatal("import inferred a template goal or execution intent")
			}
			if !workspace.GoalRequiresReadConsent(got) {
				t.Fatal("restored Code lost consent")
			}
		})
	}
}

func TestStockGoalLibraryUpgradeAndRestartPreserveExistingWorkspaces(t *testing.T) {
	handler, root, _, cleanup := templateTestEnv(t)
	defer cleanup()
	lib := handler.templatesRootResolver()
	if err := projecttemplates.EnsureLibrary(lib); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(lib, "code-project", projecttemplates.ManifestFileName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var stale map[string]any
	if err := json.Unmarshal(data, &stale); err != nil {
		t.Fatal(err)
	}
	stale["builtin_version"], stale["goal"] = 2, "Older default"
	data, err = json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before := map[string]*workspace.Workspace{}
	for index, mission := range []string{``, `,"mission":"Custom outcome"`, `,"mission":""`} {
		w, resp := postCreateWorkspace(t, handler, `{"name":"Existing `+string(rune('A'+index))+`","template_id":"code-project"`+mission+`}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		id := resp["folder"].(map[string]any)["id"].(string)
		if err := handler.taskMutationStore().Update(id, func(ws *workspace.Workspace) error {
			ws.GoalFirstRunPending = false
			ws.GoalFirstRunState = []string{workspace.GoalFirstRunBlocked, workspace.GoalFirstRunAccepted, workspace.GoalFirstRunCleared}[index]
			ws.AutonomyPolicy = []workspace.AutonomyPolicy{workspace.AutonomyWatch, workspace.AutonomyPropose, workspace.AutonomyWatch}[index]
			if index == 1 {
				at := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
				ws.MissionEnabled = true
				ws.Cadence = &workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"}
				ws.NextMissionRunAt = &at
				ws.MissionExecutionCount = 1
				ws.MissionRuns = []workspace.MissionRunRecord{{RunID: "accepted-once", Origin: "manual", Succeeded: true}}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before[id], err = handler.workspaceStore.Get(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Preserve the old one-time bootstrap migration and its deliberate-clear semantics.
	legacy := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Legacy backfill"})
	legacy.SharedData = map[string]any{"workspace_bootstrap": map[string]any{"goal": "Legacy goal"}}
	if err := handler.taskMutationStore().Save(legacy); err != nil {
		t.Fatal(err)
	}
	markerDir := t.TempDir()
	if report, err := workspace.BackfillGoalsFromBootstrap(handler.taskMutationStore(), markerDir); err != nil || report.Written != 1 {
		t.Fatalf("first migration: %+v %v", report, err)
	}
	if err := handler.taskMutationStore().Update(legacy.ID, func(ws *workspace.Workspace) error {
		ws.Mission = ""
		ws.GoalFirstRunState = workspace.GoalFirstRunCleared
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before[legacy.ID], err = handler.workspaceStore.Get(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := projecttemplates.EnsureLibrary(lib); err != nil {
		t.Fatal(err)
	}
	restartedFiles, err := workspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	restartedSQL := session.NewHybridStoreWithDB(handler.store.DB(), 50)
	defer cleanupTestHybridStore(t, restartedSQL)()
	restarted := workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(restartedSQL), restartedFiles)
	if report, err := workspace.BackfillGoalsFromBootstrap(restarted, markerDir); err != nil || !report.AlreadyDone {
		t.Fatalf("repeat migration: %+v %v", report, err)
	}
	for id, old := range before {
		for _, read := range []func(string) (*workspace.Workspace, error){restarted.Get, restarted.GetFolderWorkspace} {
			got, err := read(id)
			if err != nil {
				t.Fatal(err)
			}
			if got.Mission != old.Mission || got.GoalFirstRunPending != old.GoalFirstRunPending || got.GoalFirstRunState != old.GoalFirstRunState || got.AutonomyPolicy != old.AutonomyPolicy || got.MissionEnabled != old.MissionEnabled || !reflect.DeepEqual(got.Cadence, old.Cadence) || !reflect.DeepEqual(got.MissionRuns, old.MissionRuns) || !reflect.DeepEqual(got.NextMissionRunAt, old.NextMissionRunAt) {
				t.Fatalf("upgrade changed existing goal state for %s", id)
			}
		}
	}
	id, err := handler.CreateFromTemplate(t.Context(), "Future project", "code-project")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := projecttemplates.FindLibraryTemplate(lib, "code-project")
	if err != nil {
		t.Fatal(err)
	}
	assertGoalCreationIntent(t, handler, id, fresh.Goal)
	if fresh.Goal == "Older default" {
		t.Fatal("upgrade fixture did not refresh")
	}
}
