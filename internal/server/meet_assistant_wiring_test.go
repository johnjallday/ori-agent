package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/progression"
)

// meetAssistantHireBody is a valid, model-free hire: a name, a generated face,
// and a working agreement. The request ID is what makes a replay a replay.
const meetAssistantHireBody = `{"request_id":"meet-assistant-wiring-1","if_version":0,` +
	`"display_name":"Atlas","appearance":{"mode":"generated","generated":{"color":"#225588"}},` +
	`"mandate":"Help me keep this week's commitments visible.","focus_areas":["plan_my_day"]}`

func postMeetAssistantHire(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", strings.NewReader(meetAssistantHireBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func craftBalance(t *testing.T, builder *ServerBuilder) int64 {
	t.Helper()
	overview, err := builder.economyService.Overview(context.Background())
	if err != nil {
		t.Fatalf("economy overview: %v", err)
	}
	return overview.Craft
}

// The hire hook is bound in Phase 22.7 because the personal-assistant handler
// only exists from 22.6. This drives a hire through the REAL builder's routes,
// so a hook bound too early (nil) fails here even though a hand-assembled
// builder would pass (PRD §7.6).
func TestMeetAssistant_RealBuilderCompletesTheMissionFromTheHireOnce(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	engine := builder.progressionEngine
	if engine == nil || builder.economyService == nil || !builder.economyService.Available() {
		t.Fatal("expected progression and the economy to be wired")
	}
	if engine.HasCompleted(progression.MeetAssistantQuestID) {
		t.Fatal("a fresh install already has Meet your assistant")
	}
	before := craftBalance(t, builder)

	if rec := postMeetAssistantHire(t, handler); rec.Code != http.StatusCreated {
		t.Fatalf("hire status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !engine.HasCompleted(progression.MeetAssistantQuestID) {
		t.Fatal("the hire did not complete Meet your assistant: the hook is not wired on the real server")
	}
	if got := craftBalance(t, builder) - before; got != economy.CraftPerStarterQuest {
		t.Fatalf("the hire paid %d Craft, want %d", got, economy.CraftPerStarterQuest)
	}
	for _, mission := range engine.Status().Missions {
		if mission.Locked {
			t.Fatalf("mission %s is still locked after the hire", mission.ID)
		}
	}

	// A replay of the same request is not a second hire and pays nothing.
	if rec := postMeetAssistantHire(t, handler); rec.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := craftBalance(t, builder) - before; got != economy.CraftPerStarterQuest {
		t.Fatalf("after a replay the hire has paid %d Craft, want %d", got, economy.CraftPerStarterQuest)
	}
}

// An install that hired its assistant before Meet your assistant existed sees
// it complete on the next startup, silently and without Craft (PRD FR34).
//
// The "before" install is the real builder with the hook unbound, so the hire
// happens exactly as it did before this feature. The restart is the startup
// progression phases run again on that same builder: a new engine from the
// persisted state, over the same real stores the scanner reads.
func TestMeetAssistant_RealBuilderGrandfathersAnExistingHireWithoutCraft(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	builder.personalAssistantHandler.SetOnHired(nil)
	if rec := postMeetAssistantHire(t, handler); rec.Code != http.StatusCreated {
		t.Fatalf("hire status=%d body=%s", rec.Code, rec.Body.String())
	}
	if builder.progressionEngine.HasCompleted(progression.MeetAssistantQuestID) {
		t.Fatal("precondition: the unhooked hire completed the mission")
	}

	// This install's one-time backfill ran at first boot, before the quest
	// existed, so its reconcile key was never recorded.
	state := builder.onboardingMgr.GetProgression()
	if state.BackfilledAt.IsZero() {
		t.Fatal("precondition: first boot did not run the backfill")
	}
	delete(state.Reconciled, meetAssistantReconcileKey)
	if err := builder.onboardingMgr.SetProgression(state); err != nil {
		t.Fatal(err)
	}
	before := craftBalance(t, builder)

	builder.initializeProgression()
	builder.completeProgressionWiring()

	if !builder.progressionEngine.HasCompleted(progression.MeetAssistantQuestID) {
		t.Fatal("the reconcile did not grandfather the existing hire")
	}
	if _, recorded := builder.onboardingMgr.GetProgression().Reconciled[meetAssistantReconcileKey]; !recorded {
		t.Fatal("the reconcile pass was not recorded")
	}
	if got := craftBalance(t, builder) - before; got != 0 {
		t.Fatalf("grandfathering paid %d Craft for a hire made before the quest existed", got)
	}
}

// Meet your assistant gates every other mission, and a hire cannot be
// repeated. So a quest reset must not leave a hired user locked out: the next
// startup completes it again from the durable relationship.
func TestMeetAssistant_RealBuilderRecompletesAfterAQuestReset(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	if rec := postMeetAssistantHire(t, handler); rec.Code != http.StatusCreated {
		t.Fatalf("hire status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := builder.progressionEngine.Reset(); err != nil {
		t.Fatal(err)
	}
	before := craftBalance(t, builder)

	builder.initializeProgression()
	builder.completeProgressionWiring()

	if !builder.progressionEngine.HasCompleted(progression.MeetAssistantQuestID) {
		t.Fatal("a reset left a hired user with Meet your assistant open and every mission locked")
	}
	if got := craftBalance(t, builder) - before; got != 0 {
		t.Fatalf("re-completing after a reset paid %d Craft again", got)
	}
}
