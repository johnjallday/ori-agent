package dailybriefhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// A workspace that is not the HQ here — imported with its history while
// another HQ is designated — still reads its own old briefs, and only its own.
func TestWorkspaceBriefHistoryIsReadableWithoutBeingTheHQ(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspaces := session.NewSQLiteStore(db)
	hq := personalhq.NewService(userprofile.NewSQLiteStore(db), workspaces)
	briefs := dailybrief.NewSQLiteStore(db)
	handler := NewHandler(dailybrief.NewService(briefs, fakeGenerator{}), hq, userprofile.LocalUserProvider{})
	designateHQ(t, hq, workspaces, "current-hq")
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	for i, rev := range []dailybrief.Revision{
		{ID: "imported-1", WorkspaceID: "imported-hq", LocalDate: "2026-08-30"},
		{ID: "imported-2", WorkspaceID: "imported-hq", LocalDate: "2026-08-31"},
		{ID: "current-1", WorkspaceID: "current-hq", LocalDate: "2026-08-31"},
	} {
		rev.UserID, rev.RevisionNumber, rev.Trigger, rev.Status = "local", 1, dailybrief.TriggerScheduled, dailybrief.GenerationSucceeded
		rev.ContentJSON = `{"opening_summary":"` + rev.ID + `"}`
		rev.GeneratedAt, rev.CreatedAt = at.Add(time.Duration(i)*time.Hour), at.Add(time.Duration(i)*time.Hour)
		if err := briefs.CreateRevision(ctx, &rev); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/daily-briefs", handler.GetWorkspaceHistory)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/daily-briefs/{revisionID}", handler.GetWorkspaceRevision)
	get := func(path string) (int, map[string]json.RawMessage) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	code, body := get("/api/workspaces/imported-hq/daily-briefs")
	var history []dailybrief.HistorySummary
	if code != http.StatusOK || json.Unmarshal(body["history"], &history) != nil || len(history) != 2 || history[0].LocalDate != "2026-08-31" {
		t.Fatalf("imported history: %d %s", code, body["history"])
	}
	code, body = get("/api/workspaces/imported-hq/daily-briefs/imported-1")
	var rev dailybrief.Revision
	if code != http.StatusOK || json.Unmarshal(body["revision"], &rev) != nil || rev.ContentJSON != `{"opening_summary":"imported-1"}` {
		t.Fatalf("imported revision: %d %s", code, body["revision"])
	}
	// Another workspace's revision is not readable through this one.
	if code, _ := get("/api/workspaces/imported-hq/daily-briefs/current-1"); code != http.StatusNotFound {
		t.Fatalf("cross-workspace revision read: %d", code)
	}
	if code, body := get("/api/workspaces/no-briefs/daily-briefs"); code != http.StatusOK || string(body["history"]) != "[]" {
		t.Fatalf("empty history: %d %s", code, body["history"])
	}
}
