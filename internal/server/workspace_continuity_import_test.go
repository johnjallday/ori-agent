package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func serveJSON(t *testing.T, handler http.Handler, method, target string, body any) (int, map[string]any) {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var payload map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec.Code, payload
}

func briefRevisionCount(t *testing.T, builder *ServerBuilder, workspaceID string) int {
	t.Helper()
	var n int
	if err := builder.sessionStore.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM daily_brief_revision WHERE workspace_id=?`, workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Two real servers, one folder between them: the destination adopts the
// assistant through the composed production wiring, and its real Daily Brief
// scheduler generates nothing for the imported HQ — even after the user
// resumes the assistant and turns the schedule on — until routines are
// enabled on this installation.
func TestImportedHQRunsNoBackgroundRoutinesUntilEnabledHere(t *testing.T) {
	ctx := context.Background()
	source, sourceHandler := newDailyBriefTestServer(t)
	if code, payload := serveJSON(t, sourceHandler, http.MethodPost, "/api/personal-assistant/hire", map[string]any{
		"request_id": "continuity-hire", "if_version": 0, "display_name": "Ada", "mandate": "Keep commitments visible.",
		"focus_areas": []string{"plan_my_day"}}); code != http.StatusCreated {
		t.Fatalf("hire: %d %v", code, payload)
	}
	hired, err := source.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if code, payload := serveJSON(t, sourceHandler, http.MethodPost, "/api/personal-assistant/hq", map[string]any{
		"request_id": "continuity-hq", "if_version": hired.StateVersion, "name": "My HQ", "timezone": "UTC"}); code != http.StatusCreated {
		t.Fatalf("hq: %d %v", code, payload)
	}
	built, err := source.personalAssistantStore.GetState(ctx, "local")
	if err != nil || built.HQWorkspaceID == "" {
		t.Fatalf("HQ not bound: %+v %v", built, err)
	}
	hq := built.HQWorkspaceID
	if _, err := source.dailyBriefService.UpdateConfig(ctx, dailybrief.Config{WorkspaceID: hq, UserID: "local", Timezone: "UTC",
		ScheduleEnabled: true, ScheduleTime: "00:00", ScheduleDays: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}}); err != nil {
		t.Fatal(err)
	}
	if source.continuityWorker == nil {
		t.Fatal("continuity preparation is not wired")
	}
	// Built through the legacy Build My HQ path, whose writers leave the SQL
	// mirror and workspace.json different: the folder still becomes ready.
	if status, err := source.continuityWorker.PrepareNow(ctx, hq); err != nil || status.State != continuityprep.StateReady {
		t.Fatalf("source HQ not ready: %+v %v", status, err)
	}
	folder, err := source.workspaceFileStore.GetFolderPath(hq)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "transfer", filepath.Base(folder))
	if err := os.MkdirAll(filepath.Dir(copied), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(copied, os.DirFS(folder)); err != nil {
		t.Fatal(err)
	}

	// A different machine: its own HOME, data directory and working directory.
	dest, destHandler := newDailyBriefTestServer(t)
	code, payload := serveJSON(t, destHandler, http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(copied), nil)
	review, _ := payload["continuity"].(map[string]any)
	if code != http.StatusOK || review["recommended_action"] != "continue" {
		t.Fatalf("review: %d %v", code, payload)
	}
	code, payload = serveJSON(t, destHandler, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "continue"})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}
	state, err := dest.personalAssistantStore.GetState(ctx, "local")
	if err != nil || state.HQWorkspaceID != hq || state.Status != personalassistant.StatusPaused {
		t.Fatalf("assistant not adopted paused: %+v %v", state, err)
	}
	cfg, err := dest.dailyBriefService.GetConfig(ctx, hq)
	if err != nil || cfg.ScheduleEnabled {
		t.Fatalf("imported schedule must arrive off: %+v %v", cfg, err)
	}

	// The user resumes the assistant and turns the schedule back on — manual
	// choices the imported workspace allows — but routines are still off here.
	resumed := state.Clone()
	resumed.Status = personalassistant.StatusActive
	if _, err := dest.personalAssistantStore.UpdateState(ctx, resumed, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	cfg.ScheduleEnabled = true
	if _, err := dest.dailyBriefService.UpdateConfig(ctx, *cfg); err != nil {
		t.Fatal(err)
	}
	before := briefRevisionCount(t, dest, hq)
	dest.dailyBriefScheduler.Tick()
	if got := briefRevisionCount(t, dest, hq); got != before {
		t.Fatalf("scheduler generated %d brief(s) for an imported workspace with routines off", got-before)
	}
	if ids := dest.automaticWorkspaces([]string{hq}); len(ids) != 0 {
		t.Fatal("imported HQ offered to background catch-up loops")
	}

	// Enabling routines here is the one step that lets the schedule run.
	code, payload = serveJSON(t, destHandler, http.MethodGet, "/api/workspaces/"+hq+"/continuity", nil)
	status, _ := payload["continuity"].(map[string]any)
	if code != http.StatusOK || status["imported"] != true || status["background_allowed"] != false {
		t.Fatalf("status: %d %v", code, payload)
	}
	code, payload = serveJSON(t, destHandler, http.MethodPost, "/api/workspaces/"+hq+"/continuity/activate",
		map[string]any{"enable": true, "version": status["attachment_version"]})
	if code != http.StatusOK {
		t.Fatalf("activate: %d %v", code, payload)
	}
	dest.dailyBriefScheduler.Tick()
	if got := briefRevisionCount(t, dest, hq); got != before+1 {
		t.Fatalf("enabled routines produced %d scheduled brief(s), want exactly one", got-before)
	}
	dest.dailyBriefScheduler.Tick()
	if got := briefRevisionCount(t, dest, hq); got != before+1 {
		t.Fatal("a second tick generated a duplicate brief")
	}
}
