package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// newDailyBriefTestServer builds a full server (same wiring path as
// production, via ServerBuilder.Build) rooted at a temp directory, mirroring
// newRoutesTestHandler in routes_test.go. Returns the builder itself (not
// just its handler) so tests can reach unexported fields like
// dailyBriefService/workspaceStore to drive a TriggerScheduled generation,
// which has no HTTP route (only first-open/manual are exposed to clients).
func newDailyBriefTestServer(t *testing.T) (*ServerBuilder, http.Handler) {
	t.Helper()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWD) })
	// DefaultWorkspaceRoot() resolves to $HOME/Ori Workspaces regardless of
	// CWD (it does not respect ORI_DATA_DIR either) — this test actually
	// creates a workspace, so without this it would write into the real
	// user's home directory. t.Setenv restores the original HOME after the
	// test.
	t.Setenv("HOME", tmpDir)

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if builder.dailyBriefService == nil {
		t.Fatal("expected dailyBriefService to be wired")
	}
	return builder, srv.Handler()
}

// TestDailyBrief_ScheduledSuccessCreatesExactlyOneActionCenterNotification
// covers task 7.11 end-to-end: initializeDailyBrief's onRevisionReady hook
// (builder_dailybrief.go) must actually create a visible Action Center
// opportunity for a successful *scheduled* generation when the HQ opted in,
// wiring RecordNotificationIfEnabled (unit-tested in internal/dailybrief) to
// a real workspace.OpportunityStore.Upsert call — previously unverified
// past the unit boundary.
func TestDailyBrief_ScheduledSuccessCreatesExactlyOneActionCenterNotification(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()

	// Build My HQ (creates + designates the workspace in one call).
	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Command Post"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status = %d body=%s", setupRec.Code, setupRec.Body.String())
	}
	var setupResp struct {
		Status struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	workspaceID := setupResp.Status.WorkspaceID
	if workspaceID == "" {
		t.Fatal("expected a designated HQ workspace id")
	}

	// Opt in to notifications.
	putReq := httptest.NewRequest(http.MethodPut, "/api/personal-hq/brief/config", bytes.NewBufferString(`{"notify_on_ready":true}`))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("config PUT status = %d body=%s", putRec.Code, putRec.Body.String())
	}

	// Drive a TriggerScheduled generation directly — the scheduler's own
	// trigger path has no HTTP route by design (only first-open/manual are
	// client-initiated).
	cfg, err := builder.dailyBriefService.GetConfig(ctx, workspaceID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	localDate, err := dailybrief.TodayLocalDate(*cfg)
	if err != nil {
		t.Fatalf("TodayLocalDate: %v", err)
	}
	rev, err := builder.dailyBriefService.RequestGeneration(ctx, *cfg, "local", dailybrief.TriggerScheduled, localDate)
	if err != nil {
		t.Fatalf("RequestGeneration: %v", err)
	}
	if rev.Status != dailybrief.GenerationSucceeded && rev.Status != dailybrief.GenerationPartial {
		t.Fatalf("expected the scheduled generation to succeed or partially succeed, got %q", rev.Status)
	}

	opportunityStore := workspace.NewOpportunityStore(builder.workspaceStore)
	opportunities, err := opportunityStore.List(workspaceID)
	if err != nil {
		t.Fatalf("List opportunities: %v", err)
	}
	if len(opportunities) != 1 {
		t.Fatalf("expected exactly one Action Center opportunity, got %d: %#v", len(opportunities), opportunities)
	}
	if opportunities[0].WorkspaceID != workspaceID {
		t.Fatalf("expected the opportunity to be scoped to the HQ workspace, got %q", opportunities[0].WorkspaceID)
	}

	// A manual refresh on the same day must not create a second
	// notification (PRD FR63: manual/first-open never notify).
	if _, err := builder.dailyBriefService.RequestGeneration(ctx, *cfg, "local", dailybrief.TriggerManual, localDate); err != nil {
		t.Fatalf("manual RequestGeneration: %v", err)
	}
	opportunities, err = opportunityStore.List(workspaceID)
	if err != nil {
		t.Fatalf("List opportunities after manual refresh: %v", err)
	}
	if len(opportunities) != 1 {
		t.Fatalf("expected manual refresh to create no additional opportunity, got %d: %#v", len(opportunities), opportunities)
	}
}
