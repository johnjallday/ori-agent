package personalassistanthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
)

// The backend checkpoint for the guided HQ quest: three seeded relationships
// read through the real SQLite store, the real projection service, and the real
// HTTP handler — not fakes — so the shape of GET /api/personal-assistant is
// pinned against the actual persistence and migration stack.

type checkpointHQReader struct{ statuses map[string]*personalhq.Status }

func (r checkpointHQReader) Status(_ context.Context, userID string) (*personalhq.Status, error) {
	return r.statuses[userID], nil
}

type checkpointBriefReader struct{ configs map[string]*dailybrief.Config }

func (r checkpointBriefReader) GetConfig(_ context.Context, workspaceID string) (*dailybrief.Config, error) {
	cfg, ok := r.configs[workspaceID]
	if !ok {
		return nil, dailybrief.ErrConfigNotFound
	}
	return cfg, nil
}

type checkpointModelReader struct{}

func (checkpointModelReader) PersonalAssistantModelAvailability() personalassistant.SourceAvailability {
	return personalassistant.SourceAvailability{
		Status: personalassistant.AvailabilityNotConfigured, Reason: "model_not_configured",
	}
}

type checkpointProfileReader struct{ owners map[string]string }

func (r checkpointProfileReader) PersonalAssistantProfileProvenance(name string) (personalassistant.ProfileProvenance, bool) {
	assistantID, ok := r.owners[name]
	if !ok {
		return personalassistant.ProfileProvenance{}, false
	}
	return personalassistant.ProfileProvenance{Name: name, AssistantID: assistantID}, true
}

func seedCheckpointRelationships(t *testing.T) *personalassistant.SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := personalassistant.NewSQLiteStore(db)
	hiredAt := time.Now().UTC().Round(0)

	// needs_hire: no row at all — the fresh-install shape.

	awaiting := personalassistant.NewState("user-awaiting")
	awaiting.AssistantID = "assistant-awaiting"
	awaiting.Status = personalassistant.StatusAwaitingHQ
	awaiting.DisplayName = "Atlas"
	awaiting.GlobalAgentProfileName = "Atlas"
	awaiting.Mandate = "Keep the important work visible."
	awaiting.FocusAreas = []personalassistant.FocusArea{personalassistant.FocusPlanMyDay}
	awaiting.LastHireRequestID = "hire-req-1"
	awaiting.HiredAt = &hiredAt
	if _, err := store.CreateState(context.Background(), awaiting); err != nil {
		t.Fatalf("seed awaiting_hq: %v", err)
	}

	active := personalassistant.NewState("user-active")
	active.AssistantID = "assistant-active"
	active.Status = personalassistant.StatusActive
	active.DisplayName = "Ada"
	active.GlobalAgentProfileName = "Ada"
	active.HQWorkspaceID = "ws-active"
	active.HQEntryAgentInstanceID = "inst-active"
	active.Mandate = "Keep launches visible."
	active.HiredAt = &hiredAt
	if _, err := store.CreateState(context.Background(), active); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	return store
}

func checkpointHandler(t *testing.T, userID string) *Handler {
	t.Helper()
	store := seedCheckpointRelationships(t)
	service := personalassistant.NewService(
		store,
		checkpointHQReader{statuses: map[string]*personalhq.Status{
			"user-active": {
				UserID: "user-active", WorkspaceID: "ws-active", Valid: true,
				Workspace: &session.Workspace{
					ID: "ws-active", OwnerUserID: "user-active",
					AgentInstances: []session.AgentInstance{{
						ID: "inst-active", Name: "Ada", EntryPoint: true,
					}},
				},
			},
		}},
		checkpointBriefReader{configs: map[string]*dailybrief.Config{
			"ws-active": {
				WorkspaceID: "ws-active", UserID: "user-active", Timezone: "UTC",
				ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ConfigRevision: 1,
			},
		}},
		checkpointModelReader{},
	).WithProfileReader(checkpointProfileReader{owners: map[string]string{
		"Atlas": "assistant-awaiting",
		"Ada":   "assistant-active",
	}})
	return NewHandler(service, fakeUserProvider{userID: userID})
}

func readCheckpointState(t *testing.T, userID string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	checkpointHandler(t, userID).GetState(recorder,
		httptest.NewRequest(http.MethodGet, "/api/personal-assistant", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/personal-assistant for %s = %d", userID, recorder.Code)
	}
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	body, ok := envelope["personal_assistant"].(map[string]any)
	if !ok {
		t.Fatalf("response envelope = %v", envelope)
	}
	return body
}

func TestCheckpoint_NeedsHireIsUnchanged(t *testing.T) {
	body := readCheckpointState(t, "user-fresh")
	if body["state"] != "needs_hire" || body["next_action"] != "hire" {
		t.Fatalf("fresh install = %v", body)
	}
	if _, present := body["assistant_id"]; present {
		t.Fatalf("fresh install invented an identity: %v", body)
	}
}

func TestCheckpoint_AwaitingHQIsHealthyButIncomplete(t *testing.T) {
	body := readCheckpointState(t, "user-awaiting")

	if body["state"] != "needs_hq" || body["next_action"] != "build_hq" {
		t.Fatalf("state=%v next_action=%v", body["state"], body["next_action"])
	}
	// The named identity survives intact — this is a real hire.
	if body["assistant_id"] != "assistant-awaiting" || body["display_name"] != "Atlas" ||
		body["global_agent_profile_name"] != "Atlas" || body["appearance"] == nil {
		t.Fatalf("named identity missing: %v", body)
	}
	if body["state_version"] == nil {
		t.Fatalf("state version missing: %v", body)
	}
	// No HQ claim of any kind may appear.
	for _, key := range []string{"hq_workspace_id", "hq_agent_instance_id", "daily_brief"} {
		if _, present := body[key]; present {
			t.Fatalf("pre-HQ read advertised %s: %v", key, body)
		}
	}
	availability, ok := body["availability"].(map[string]any)
	if !ok {
		t.Fatalf("availability missing: %v", body)
	}
	for _, key := range []string{"personal_hq", "agent_instance", "daily_brief"} {
		source, sourceOK := availability[key].(map[string]any)
		if !sourceOK {
			t.Fatalf("availability.%s missing", key)
		}
		if source["status"] != "not_configured" || source["reason"] != "hq_not_built" {
			t.Fatalf("availability.%s = %v; want not_configured/hq_not_built", key, source)
		}
		if source["available"] != false {
			t.Fatalf("availability.%s claimed availability before HQ exists", key)
		}
	}
	// Absence of a model is an independent flag, not an assistant failure.
	model, _ := availability["model"].(map[string]any)
	if model["status"] != "not_configured" {
		t.Fatalf("model availability = %v", model)
	}
}

func TestCheckpoint_ExistingActiveRelationshipIsUnaffected(t *testing.T) {
	body := readCheckpointState(t, "user-active")

	if body["state"] != "active" || body["next_action"] != "ask" {
		t.Fatalf("state=%v next_action=%v", body["state"], body["next_action"])
	}
	if body["hq_workspace_id"] != "ws-active" || body["hq_agent_instance_id"] != "inst-active" {
		t.Fatalf("active linkage changed: %v", body)
	}
	brief, ok := body["daily_brief"].(map[string]any)
	if !ok || brief["schedule_time"] != "08:00" {
		t.Fatalf("daily brief projection changed: %v", body["daily_brief"])
	}
}
