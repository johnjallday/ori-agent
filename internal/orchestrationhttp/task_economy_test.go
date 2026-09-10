package orchestrationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// economyFixture is a task handler with a real ledger behind it: the price
// check's whole job is to agree with what the ledger says, so a fake ledger
// would be testing the fake.
type economyFixture struct {
	handler *TaskHandler
	store   *canonicalTaskStore
	service *economy.Service
	ledger  *economy.SQLiteStore
}

type fixedSettings struct{ creativeMode bool }

func (f fixedSettings) EconomyCreativeMode() bool       { return f.creativeMode }
func (f fixedSettings) EconomyDailyEnergyTokens() int64 { return 0 }

func newEconomyFixture(t *testing.T, craft, harvest int64) *economyFixture {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := &canonicalTaskStore{InMemoryStore: workspace.NewInMemoryStore()}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "ws-1"})
	ws.ID = "ws-1"
	ws.AgentInstances = []workspace.AgentInstance{{ID: "agent-1", Name: "Lead", EntryPoint: true}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	ledger := economy.NewSQLiteStore(db)
	service := economy.NewService(ledger, economy.NewWorkspaceTasks(store), fixedSettings{}, nil)

	// Stock the city by hand rather than by simulating a day of work: what is
	// under test is the price check, not how the balance got there.
	seed(t, ledger, economy.ResourceCraft, craft, "seed-craft")
	seed(t, ledger, economy.ResourceHarvest, harvest, "seed-harvest")

	// The update path resolves the task through the communicator, so it needs a
	// real one — a nil communicator panics before the price check is reached.
	handler := NewTaskHandler(store, agentcomm.NewCommunicator(store), nil, nil)
	handler.SetEconomy(service)
	return &economyFixture{handler: handler, store: store, service: service, ledger: ledger}
}

func seed(t *testing.T, ledger *economy.SQLiteStore, resource string, amount int64, ref string) {
	t.Helper()
	if amount <= 0 {
		return
	}
	if _, err := ledger.Credit(context.Background(), economy.Entry{
		Resource: resource, Amount: amount, Reason: economy.ReasonBackfill,
		RefKind: economy.RefKindBackfill, RefID: ref,
	}, time.Now()); err != nil {
		t.Fatalf("seed %s: %v", resource, err)
	}
}

func (f *economyFixture) createTask(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.handleCreateTask(recorder, request)
	return recorder
}

func (f *economyFixture) updateTask(t *testing.T, taskID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/orchestration/tasks/"+taskID, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.handleUpdateTask(recorder, request)
	return recorder
}

func (f *economyFixture) balances(t *testing.T) economy.Balances {
	t.Helper()
	balances, err := f.ledger.Balances(context.Background())
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	return balances
}

func (f *economyFixture) task(t *testing.T, taskID string) *workspace.Task {
	t.Helper()
	ws, err := f.store.Get("ws-1")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	task, err := ws.GetTask(taskID)
	if err != nil {
		return nil
	}
	return task
}

// createdTaskID pulls the id out of a successful create response.
func createdTaskID(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if body.Task.ID == "" {
		t.Fatalf("create response carried no task id: %s", recorder.Body.String())
	}
	return body.Task.ID
}

const dailyFarmBody = `{"workspace_id":"ws-1","description":"Inbox triage","to":"Lead",` +
	`"schedule":{"type":"daily","time":"09:00"},"schedule_enabled":true}`

func TestCreatingAFarmChargesTheBuildPriceOnce(t *testing.T) {
	fixture := newEconomyFixture(t, economy.FarmBuildCost+10, 0)

	recorder := fixture.createTask(t, dailyFarmBody)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}

	if got := fixture.balances(t).Craft; got != 10 {
		t.Fatalf("craft = %d, want 10 — exactly one build charge", got)
	}
}

// A task with no schedule is not a Farm and costs nothing (FR20).
func TestCreatingAPlainTaskIsFree(t *testing.T) {
	fixture := newEconomyFixture(t, 30, 0)

	recorder := fixture.createTask(t, `{"workspace_id":"ws-1","description":"Just a task"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.balances(t).Craft; got != 30 {
		t.Fatalf("craft = %d, want 30 untouched", got)
	}
}

// A brand-new Farm may start at ANY tier for the flat build price (FR18).
func TestABrandNewFarmCostsTheSameAtEveryTier(t *testing.T) {
	for _, schedule := range []string{
		`{"type":"daily","time":"09:00"}`,
		`{"type":"interval","interval_minutes":15}`,
		`{"type":"weekly","time":"09:00","day_of_week":3}`,
	} {
		fixture := newEconomyFixture(t, economy.FarmBuildCost, 0)
		body := `{"workspace_id":"ws-1","description":"Farm","to":"Lead","schedule":` +
			schedule + `,"schedule_enabled":true}`
		if recorder := fixture.createTask(t, body); recorder.Code != http.StatusCreated {
			t.Fatalf("status = %d for %s: %s", recorder.Code, schedule, recorder.Body.String())
		}
		if got := fixture.balances(t).Craft; got != 0 {
			t.Fatalf("craft = %d after building at %s, want 0", got, schedule)
		}
	}
}

// The refusal body is exactly FR22's, and nothing is created.
func TestAnUnaffordableBuildIsRefusedAndCreatesNothing(t *testing.T) {
	fixture := newEconomyFixture(t, 12, 0)

	recorder := fixture.createTask(t, dailyFarmBody)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	if body["error"] != "insufficient_resources" {
		t.Fatalf("error = %v, want insufficient_resources", body["error"])
	}
	if body["resource"] != "craft" {
		t.Fatalf("resource = %v, want craft", body["resource"])
	}
	if body["action"] != "build" {
		t.Fatalf("action = %v, want build", body["action"])
	}
	if body["required"] != float64(economy.FarmBuildCost) {
		t.Fatalf("required = %v, want %d", body["required"], economy.FarmBuildCost)
	}
	if body["balance"] != float64(12) {
		t.Fatalf("balance = %v, want 12", body["balance"])
	}

	if got := fixture.balances(t).Craft; got != 12 {
		t.Fatalf("craft = %d after a refusal, want 12 untouched", got)
	}
	ws, err := fixture.store.Get("ws-1")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if len(ws.Tasks) != 0 {
		t.Fatalf("a refused create left %d tasks behind", len(ws.Tasks))
	}
}

func TestSpeedingUpAFarmChargesHarvest(t *testing.T) {
	fixture := newEconomyFixture(t, economy.FarmBuildCost, 60)

	taskID := createdTaskID(t, fixture.createTask(t, dailyFarmBody))

	// Daily (2) -> Hourly (4) is 20 + 30 = 50 Harvest (FR19).
	recorder := fixture.updateTask(t, taskID,
		`{"schedule":{"type":"interval","interval_minutes":60}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	balances := fixture.balances(t)
	if balances.Harvest != 10 {
		t.Fatalf("harvest = %d, want 10 (60 - 50)", balances.Harvest)
	}
	if balances.Craft != 0 {
		t.Fatalf("craft = %d, want 0 — an upgrade is paid in Harvest", balances.Craft)
	}
}

// Slowing a Farm down is free, and so is changing anything that is not the
// cadence (FR20).
func TestSlowingDownAndNonCadenceEditsAreFree(t *testing.T) {
	fixture := newEconomyFixture(t, economy.FarmBuildCost, 60)
	taskID := createdTaskID(t, fixture.createTask(t, dailyFarmBody))

	if recorder := fixture.updateTask(t, taskID,
		`{"schedule":{"type":"weekly","time":"09:00","day_of_week":3}}`); recorder.Code != http.StatusOK {
		t.Fatalf("slow-down status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.balances(t).Harvest; got != 60 {
		t.Fatalf("harvest = %d after slowing down, want 60", got)
	}

	if recorder := fixture.updateTask(t, taskID,
		`{"description":"A new description"}`); recorder.Code != http.StatusOK {
		t.Fatalf("description status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.balances(t).Harvest; got != 60 {
		t.Fatalf("harvest = %d after a description edit, want 60", got)
	}
}

// Disabling and re-enabling a schedule is free forever after the first build
// (FR17): the build entry is what is remembered, not the enabled flag.
func TestReEnablingAFarmIsFree(t *testing.T) {
	fixture := newEconomyFixture(t, economy.FarmBuildCost, 0)
	taskID := createdTaskID(t, fixture.createTask(t, dailyFarmBody))
	if got := fixture.balances(t).Craft; got != 0 {
		t.Fatalf("craft = %d after the build, want 0", got)
	}

	if recorder := fixture.updateTask(t, taskID, `{"schedule_enabled":false}`); recorder.Code != http.StatusOK {
		t.Fatalf("disable status = %d: %s", recorder.Code, recorder.Body.String())
	}
	// Re-enabling with no Craft at all must still succeed.
	if recorder := fixture.updateTask(t, taskID, `{"schedule_enabled":true}`); recorder.Code != http.StatusOK {
		t.Fatalf("re-enable status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.balances(t).Craft; got != 0 {
		t.Fatalf("craft = %d, want 0 — re-enabling charged something", got)
	}
}

// An unaffordable upgrade leaves the task exactly as it was (FR22).
func TestAnUnaffordableUpgradeLeavesTheTaskUntouched(t *testing.T) {
	fixture := newEconomyFixture(t, economy.FarmBuildCost, 5)
	taskID := createdTaskID(t, fixture.createTask(t, dailyFarmBody))
	before := fixture.task(t, taskID)
	if before == nil {
		t.Fatal("the Farm was not created")
	}
	beforeType := before.Schedule.Type

	recorder := fixture.updateTask(t, taskID,
		`{"schedule":{"type":"interval","interval_minutes":60}}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	if body["resource"] != "harvest" || body["action"] != "upgrade" {
		t.Fatalf("body = %v, want an upgrade priced in harvest", body)
	}

	after := fixture.task(t, taskID)
	if after == nil {
		t.Fatal("the Farm disappeared")
	}
	if after.Schedule.Type != beforeType {
		t.Fatalf("schedule type = %q after a refusal, want %q untouched",
			after.Schedule.Type, beforeType)
	}
	if got := fixture.balances(t).Harvest; got != 5 {
		t.Fatalf("harvest = %d after a refusal, want 5 untouched", got)
	}
}

// Creative mode waives every cost while leaving the action reported (FR24).
func TestCreativeModeChargesNothing(t *testing.T) {
	fixture := newEconomyFixture(t, 0, 0)
	fixture.service.SetSettingsSource(fixedSettings{creativeMode: true})

	recorder := fixture.createTask(t, dailyFarmBody)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 with no resources at all: %s", recorder.Code, recorder.Body.String())
	}
	taskID := createdTaskID(t, recorder)

	if recorder := fixture.updateTask(t, taskID,
		`{"schedule":{"type":"interval","interval_minutes":15}}`); recorder.Code != http.StatusOK {
		t.Fatalf("upgrade status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	balances := fixture.balances(t)
	if balances.Craft != 0 || balances.Harvest != 0 {
		t.Fatalf("balances = %+v, want everything at zero", balances)
	}
	// The build entry is still written, so switching creative mode back off does
	// not suddenly make an existing Farm chargeable.
	built, err := fixture.ledger.HasEntry(context.Background(), economy.ResourceCraft, economy.RefKindBuild, taskID)
	if err != nil {
		t.Fatalf("has entry: %v", err)
	}
	if !built {
		t.Fatal("creative mode skipped the build entry, so the Farm could be charged later")
	}
}

// The task sub-handler is built lazily, and SetTaskHandler can replace it long
// after the economy is wired. A service handed only to the instance that existed
// at wiring time is silently dropped by the one that replaces it — which shipped
// in a demo build where every Farm was free. The Handler must remember it.
func TestTheEconomySurvivesALateTaskSubHandler(t *testing.T) {
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := &canonicalTaskStore{InMemoryStore: workspace.NewInMemoryStore()}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "ws-1"})
	ws.ID = "ws-1"
	ws.AgentInstances = []workspace.AgentInstance{{ID: "agent-1", Name: "Lead", EntryPoint: true}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	ledger := economy.NewSQLiteStore(db)
	service := economy.NewService(ledger, economy.NewWorkspaceTasks(store), fixedSettings{}, nil)

	// Wire the economy onto a Handler whose sub-handler does not exist yet.
	handler := NewHandlerLegacy(nil, store)
	handler.eventBus = workspace.DefaultEventBus()
	handler.SetEconomy(service)
	if handler.taskHandlerSub != nil {
		t.Fatal("the fixture is wrong: the sub-handler already existed")
	}

	// Now build it, the way the server's later phase does.
	handler.SetTaskHandler(stubTaskHandler{})
	if handler.taskHandlerSub == nil {
		t.Fatal("the sub-handler was not built")
	}

	// With no Craft at all, a Farm build must be refused — which only happens
	// if the late-built sub-handler received the economy.
	request := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks",
		bytes.NewBufferString(dailyFarmBody))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.taskHandlerSub.handleCreateTask(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — the late sub-handler has no economy: %s",
			recorder.Code, recorder.Body.String())
	}
}

// stubTaskHandler satisfies workspace.TaskHandler without executing anything:
// the test only needs SetTaskHandler to build the sub-handler.
type stubTaskHandler struct{}

func (stubTaskHandler) ExecuteTask(context.Context, string, workspace.Task) (string, error) {
	return "", nil
}

// With no economy wired at all, every save behaves exactly as it did before the
// feature existed.
func TestSavesAreFreeWithoutAnEconomy(t *testing.T) {
	store := &canonicalTaskStore{InMemoryStore: workspace.NewInMemoryStore()}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "ws-1"})
	ws.ID = "ws-1"
	ws.AgentInstances = []workspace.AgentInstance{{ID: "agent-1", Name: "Lead", EntryPoint: true}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	handler := NewTaskHandler(store, nil, nil, nil)

	request := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks",
		bytes.NewBufferString(dailyFarmBody))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.handleCreateTask(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
}
