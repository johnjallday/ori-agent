package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

const offerRosterManifest = `{
	"name":"Offer Roster",
	"agents":[{"name":"Development Assistant","role":"general","system_prompt":"understand and plan"}]
}`

// reviewedRoster is the review a folder offer would hold for the blueprint as
// it stands now, optionally renamed.
func reviewedRoster(t *testing.T, handler *Handler, assistant string) FolderOfferRosterReview {
	t.Helper()
	roster, err := handler.FolderOfferRoster("offer-roster", assistant)
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	return FolderOfferRosterReview{PlanRevision: roster.PlanRevision, Assistant: roster.Assistant, Action: roster.Action}
}

// A folder offer's creation is held to the roster its review showed: the
// renamed assistant is created, and the workspace is this offer's.
func TestCreateFolderOfferWorkspaceCreatesTheReviewedRoster(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "offer-roster", offerRosterManifest)
	review := reviewedRoster(t, handler, "Dev Pat")

	id, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{
		Name: "Widget", TemplateID: "offer-roster", OfferID: "offer-1", Roster: &review,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ws, err := handler.store.GetWorkspace(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if currentWorkspaceEntryAgentName(ws) != "Dev Pat" || len(ws.AgentInstances) != 1 {
		t.Fatalf("roster = %#v", ws.AgentInstances)
	}
	if _, exists := handler.agentStore.GetAgent("Development Assistant"); exists {
		t.Fatal("the blueprint's own name was created beside the reviewed one")
	}
	if created, exists := handler.agentStore.GetAgent("Dev Pat"); !exists || created.Settings.SystemPrompt != "understand and plan" {
		t.Fatalf("reviewed assistant = %+v, %v", created, exists)
	}
}

// A saved agent that appeared after the review, or one the review counted on
// that is gone, is a changed roster: nothing is created, and the caller is told
// which kind of failure it was.
func TestCreateFolderOfferWorkspaceRefusesARosterThatChangedSinceTheReview(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "offer-roster", offerRosterManifest)
	review := reviewedRoster(t, handler, "") // reviewed: a new agent is created
	if err := handler.agentStore.CreateAgent("Development Assistant", &agentstore.CreateAgentConfig{SystemPrompt: "the user's own"}); err != nil {
		t.Fatal(err)
	}
	before := len(handler.agentStore.ListAgents())

	_, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{
		Name: "Widget", TemplateID: "offer-roster", OfferID: "offer-1", Roster: &review,
	})
	if !errors.Is(err, ErrFolderOfferRosterChanged) {
		t.Fatalf("err = %v, want ErrFolderOfferRosterChanged", err)
	}
	workspaces, listErr := handler.store.ListWorkspaces(context.Background())
	if listErr != nil || len(workspaces) != 0 {
		t.Fatalf("a workspace was created on a stale roster: %d, %v", len(workspaces), listErr)
	}
	if saved, _ := handler.agentStore.GetAgent("Development Assistant"); saved.Settings.SystemPrompt != "the user's own" || len(handler.agentStore.ListAgents()) != before {
		t.Fatalf("the user's agent was changed: %+v", saved.Settings)
	}
}

// failingUpdateStore refuses workspace updates on demand.
type failingUpdateStore struct {
	session.HybridStore
	fail bool
}

func (s *failingUpdateStore) UpdateWorkspace(ctx context.Context, ws *session.Workspace) error {
	if s.fail {
		return errors.New("injected workspace update failure")
	}
	return s.HybridStore.UpdateWorkspace(ctx, ws)
}

// bareWorkspace creates a workspace with nobody in it.
func bareWorkspace(t *testing.T, handler *Handler) string {
	t.Helper()
	recorder, response := postCreateWorkspace(t, handler, `{"name":"By hand","create_template_agents":false}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	return response["folder"].(map[string]any)["id"].(string)
}

// An existing workspace with no assistant gets the reviewed one as its entry
// agent. A repeat is a no-op, and a workspace that already has someone else is
// refused rather than re-staffed.
func TestAttachFolderOfferAssistantStaffsAnEmptyWorkspaceOnce(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "offer-roster", offerRosterManifest)
	id := bareWorkspace(t, handler)
	review := reviewedRoster(t, handler, "")

	attached, err := handler.AttachFolderOfferAssistant(context.Background(), id, "offer-roster", review)
	if err != nil || attached.Name != "Development Assistant" || !attached.Created {
		t.Fatalf("attach = %+v, %v", attached, err)
	}
	// Both copies of the workspace have it.
	row, _ := handler.store.GetWorkspace(context.Background(), id)
	saved, _ := handler.workspaceStore.Get(id)
	if currentWorkspaceEntryAgentName(row) != "Development Assistant" || saved.EntryAgentName() != "Development Assistant" || len(saved.AgentInstances) != 1 {
		t.Fatalf("entry agent: row %q, workspace.json %q", currentWorkspaceEntryAgentName(row), saved.EntryAgentName())
	}

	again, err := handler.AttachFolderOfferAssistant(context.Background(), id, "offer-roster", review)
	if err != nil || again.Created || again.Name != "Development Assistant" {
		t.Fatalf("a repeat = %+v, %v", again, err)
	}
	if saved, _ := handler.workspaceStore.Get(id); len(saved.AgentInstances) != 1 {
		t.Fatalf("a repeat attached a second instance: %d", len(saved.AgentInstances))
	}

	other := reviewedRoster(t, handler, "Somebody Else")
	if _, err := handler.AttachFolderOfferAssistant(context.Background(), id, "offer-roster", other); !errors.Is(err, ErrFolderOfferRosterChanged) {
		t.Fatalf("re-staffing a staffed workspace: %v", err)
	}
	if _, exists := handler.agentStore.GetAgent("Somebody Else"); exists {
		t.Fatal("an agent was created for a workspace that was refused")
	}
}

// When the workspace cannot be saved, an agent this call created is removed
// again, and a saved agent it was only reusing is left exactly as it was.
func TestAttachFolderOfferAssistantRollsBackOnlyWhatItCreated(t *testing.T) {
	t.Run("a created agent is removed", func(t *testing.T) {
		handler, _, _, cleanup := templateTestEnv(t)
		defer cleanup()
		writeRosterTemplate(t, handler, "offer-roster", offerRosterManifest)
		id := bareWorkspace(t, handler)
		review := reviewedRoster(t, handler, "")
		failing := &failingUpdateStore{HybridStore: handler.store, fail: true}
		handler.store = failing

		if _, err := handler.AttachFolderOfferAssistant(context.Background(), id, "offer-roster", review); err == nil {
			t.Fatal("a failed save was reported as attached")
		}
		assertAgentAbsent(t, handler, "Development Assistant")
		if saved, _ := handler.workspaceStore.Get(id); saved.EntryAgentName() != "" {
			t.Fatalf("a failed attach left an entry agent: %q", saved.EntryAgentName())
		}

		// The same confirmation succeeds once the store does.
		failing.fail = false
		attached, err := handler.AttachFolderOfferAssistant(context.Background(), id, "offer-roster", review)
		if err != nil || !attached.Created {
			t.Fatalf("retry = %+v, %v", attached, err)
		}
	})

	t.Run("a reused agent survives", func(t *testing.T) {
		handler, _, _, cleanup := templateTestEnv(t)
		defer cleanup()
		writeRosterTemplate(t, handler, "offer-roster", offerRosterManifest)
		if err := handler.agentStore.CreateAgent("Robin", &agentstore.CreateAgentConfig{SystemPrompt: "saved and unchanged"}); err != nil {
			t.Fatal(err)
		}
		id := bareWorkspace(t, handler)
		review := reviewedRoster(t, handler, "Robin")
		if review.Action != "reuse" {
			t.Fatalf("review action = %q", review.Action)
		}
		handler.store = &failingUpdateStore{HybridStore: handler.store, fail: true}

		if _, err := handler.AttachFolderOfferAssistant(context.Background(), id, "offer-roster", review); err == nil {
			t.Fatal("a failed save was reported as attached")
		}
		saved, exists := handler.agentStore.GetAgent("Robin")
		if !exists || saved.Settings.SystemPrompt != "saved and unchanged" {
			t.Fatalf("the user's saved agent did not survive a failed setup: %+v, %v", saved, exists)
		}
	})
}

// The assessment is the workspace's one first task. Seeding again finds it, a
// workspace that already has a first look keeps that one untouched, and the
// recipe marker cannot be acquired by an ordinary task.
func TestSeedFolderOfferAssessmentSeedsOneRecipeTaskAndKeepsAnExistingFirstLook(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "offer-roster", offerRosterManifest)
	review := reviewedRoster(t, handler, "")
	id, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{
		Name: "Widget", TemplateID: "offer-roster", OfferID: "offer-1", Roster: &review,
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment := FolderOfferAssessment{
		Recipe: agentworkspace.FirstTaskRecipeSoftwareAssessment, RecipeVersion: 1,
		Description: "Assess this project", Details: "read-only",
	}

	taskID, seeded, err := handler.SeedFolderOfferAssessment(id, assessment)
	if err != nil || !seeded || taskID == "" {
		t.Fatalf("seed = %q, %v, %v", taskID, seeded, err)
	}
	again, seededAgain, err := handler.SeedFolderOfferAssessment(id, assessment)
	if err != nil || seededAgain || again != taskID {
		t.Fatalf("a second seed = %q, %v, %v; want the first task", again, seededAgain, err)
	}
	ws, _ := handler.workspaceStore.Get(id)
	if len(ws.Tasks) != 1 {
		t.Fatalf("tasks = %d", len(ws.Tasks))
	}
	task := &ws.Tasks[0]
	if !personalassistant.IsFolderFirstTask(task) || agentworkspace.FirstTaskRecipe(task) != agentworkspace.FirstTaskRecipeSoftwareAssessment ||
		task.Status != agentworkspace.TaskStatusPending || task.To != "Development Assistant" {
		t.Fatalf("assessment = %+v", task)
	}

	// With no way to run the recipe here, the task is not startable, the start
	// endpoint refuses without spending its one start, and both say why.
	view, ok := handler.FolderFirstTaskView(context.Background(), id)
	if !ok || view.CanStart || view.Reason != personalassistant.FolderFirstTaskReasonRecipeUnavailable || view.Message == "" {
		t.Fatalf("first task view = %+v, %v", view, ok)
	}
	started := 0
	handler.SetTemplateSetupTaskStarter(func(string, string) error { started++; return nil })
	start := func() map[string]any {
		recorder := httptest.NewRecorder()
		handler.handleFolderFirstTaskStart(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+id+"/folder-first-task/start", nil), id)
		var decoded map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode start response: %v (%s)", err, recorder.Body.String())
		}
		return decoded
	}
	if got := start(); got["started"] != false || got["reason"] != personalassistant.FolderFirstTaskReasonRecipeUnavailable || started != 0 {
		t.Fatalf("start = %v (starter called %d times)", got, started)
	}
	if ws, _ := handler.workspaceStore.Get(id); ws.Tasks[0].Context[personalassistant.FolderFirstTaskConsumedKey] != nil {
		t.Fatal("a refused start spent the task's one start")
	}
	// Once the recipe can be run here, the same click starts it.
	handler.SetFirstTaskRecipeRunnable(func(recipe string) bool { return recipe == agentworkspace.FirstTaskRecipeSoftwareAssessment })
	if view, _ := handler.FolderFirstTaskView(context.Background(), id); !view.CanStart {
		t.Fatalf("a runnable recipe is still blocked: %+v", view)
	}
	if got := start(); got["started"] != true || started != 1 {
		t.Fatalf("start with a runnable recipe = %v (%d)", got, started)
	}
}

func TestSeedFolderOfferAssessmentKeepsALegacyFirstLook(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	id := bareWorkspace(t, handler)
	// The first look an older setup seeded, already finished.
	err := handler.taskMutationStore().Update(id, func(ws *agentworkspace.Workspace) error {
		return ws.AddTask(agentworkspace.Task{
			ID: "legacy-look", Description: "List the open TODOs", Status: agentworkspace.TaskStatusCompleted, Result: "Three TODOs.",
			Context: map[string]any{taskContextTemplateID: folderFirstTaskTemplateID, taskContextTemplateStarterTask: true},
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	taskID, seeded, err := handler.SeedFolderOfferAssessment(id, FolderOfferAssessment{
		Recipe: agentworkspace.FirstTaskRecipeSoftwareAssessment, RecipeVersion: 1, Description: "Assess this project",
	})
	if err != nil || seeded || taskID != "legacy-look" {
		t.Fatalf("seed over a legacy first look = %q, %v, %v", taskID, seeded, err)
	}
	ws, _ := handler.workspaceStore.Get(id)
	if len(ws.Tasks) != 1 || ws.Tasks[0].Result != "Three TODOs." || ws.Tasks[0].Status != agentworkspace.TaskStatusCompleted ||
		agentworkspace.FirstTaskRecipe(&ws.Tasks[0]) != "" {
		t.Fatalf("the legacy first look was rewritten: %+v", ws.Tasks)
	}
	if !strings.Contains(ws.Tasks[0].Description, "TODOs") {
		t.Fatalf("description = %q", ws.Tasks[0].Description)
	}
}
