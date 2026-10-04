package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// generateTestBrief gives the HQ a current Daily Brief revision.
func generateTestBrief(t *testing.T, builder *ServerBuilder, workspaceID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := builder.dailyBriefService.UpdateConfig(ctx, dailybrief.Config{WorkspaceID: workspaceID, UserID: userprofile.LocalUserID}); err != nil {
		t.Fatalf("brief config: %v", err)
	}
	rev, err := builder.dailyBriefService.RequestGenerationNow(ctx, workspaceID, userprofile.LocalUserID, dailybrief.TriggerManual)
	if err != nil {
		t.Fatalf("generate brief: %v", err)
	}
	if rev.Status != dailybrief.GenerationSucceeded && rev.Status != dailybrief.GenerationPartial {
		t.Fatalf("brief generation ended %q", rev.Status)
	}
}

func postBriefSeen(t *testing.T, handler http.Handler) bool {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/personal-hq/brief/seen", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("brief seen status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Seen bool `json:"seen"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode brief seen: %v", err)
	}
	return got.Seen
}

// Read your first Daily Brief completes when the Daily Brief panel in My HQ
// reports a brief as seen. Reading Today, which the assistant drawer does on
// every open, no longer completes it: the brief is not displayed there.
//
// Driven through the real builder's routes, so a hook bound to the wrong owner
// or too early fails here.
func TestFirstBrief_RealBuilderCompletesFromThePanelAndNotFromToday(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	engine := builder.progressionEngine
	if engine == nil || builder.economyService == nil || !builder.economyService.Available() {
		t.Fatal("expected progression and the economy to be wired")
	}

	if rec := postMeetAssistantHire(t, handler); rec.Code != http.StatusCreated {
		t.Fatalf("hire status=%d body=%s", rec.Code, rec.Body.String())
	}
	state := activateTestRelationshipWithHQ(t, builder, handler, userprofile.LocalUserID)

	// No brief yet: a report has nothing to stand on.
	if postBriefSeen(t, handler) {
		t.Fatal("the panel reported a brief seen before one existed")
	}
	if engine.HasCompleted(progression.FirstBriefQuestID) {
		t.Fatal("the mission completed with no brief")
	}

	generateTestBrief(t, builder, state.HQWorkspaceID)

	// Today serves the brief, as many times as the drawer opens.
	for range 3 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("today status=%d body=%s", rec.Code, rec.Body.String())
		}
		var today struct {
			Today struct {
				Brief struct {
					RevisionID string `json:"revision_id"`
				} `json:"brief"`
			} `json:"today"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &today); err != nil {
			t.Fatalf("decode today: %v", err)
		}
		if today.Today.Brief.RevisionID == "" {
			t.Fatalf("precondition: Today did not serve the brief: %s", rec.Body.String())
		}
	}
	if engine.HasCompleted(progression.FirstBriefQuestID) {
		t.Fatal("serving Today completed Read your first Daily Brief; only the panel may")
	}

	before := craftBalance(t, builder)
	if !postBriefSeen(t, handler) {
		t.Fatal("the panel's report was not accepted with a brief present")
	}
	if !engine.HasCompleted(progression.FirstBriefQuestID) {
		t.Fatal("the panel showing a brief did not complete Read your first Daily Brief")
	}
	if got := craftBalance(t, builder) - before; got != economy.CraftPerStarterQuest {
		t.Fatalf("the mission paid %d Craft, want %d", got, economy.CraftPerStarterQuest)
	}

	// The panel reports every brief it shows. Only the first one counts.
	for range 2 {
		if !postBriefSeen(t, handler) {
			t.Fatal("a repeated report was refused")
		}
	}
	if got := craftBalance(t, builder) - before; got != economy.CraftPerStarterQuest {
		t.Fatalf("after repeated reports the mission has paid %d Craft, want %d", got, economy.CraftPerStarterQuest)
	}
}

// The mission is locked until the assistant is hired. A brief in an HQ that has
// no assistant must not complete it, exactly as before, when the trigger was a
// Today that only an active or paused relationship could reach.
func TestFirstBrief_RealBuilderNeedsAHiredAssistant(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)

	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Command Post"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status=%d body=%s", setupRec.Code, setupRec.Body.String())
	}
	var setup struct {
		Status struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setup); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	generateTestBrief(t, builder, setup.Status.WorkspaceID)

	if !postBriefSeen(t, handler) {
		t.Fatal("precondition: the report was not accepted with a brief present")
	}
	if builder.progressionEngine.HasCompleted(progression.FirstBriefQuestID) {
		t.Fatal("a brief in an HQ with no hired assistant completed a mission locked until the hire")
	}
}
