package sessionhttp

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// 100 conversations × 100 messages travel in bounded chunks and come back
// complete and in order. Timings are logged as observations, not a guarantee.
func TestContinuityLargeHistoryRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("large-history round trip")
	}
	ctx := t.Context()
	at := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Long Running"})
	ws.OwnerUserID, ws.FolderSlug, ws.CreatedAt, ws.UpdatedAt = "local", "long-running", at, at
	if err := source.sync.Save(ws); err != nil {
		t.Fatal(err)
	}
	const sessions, perSession = 100, 100
	seedStart := time.Now()
	for s := 0; s < sessions; s++ {
		id := fmt.Sprintf("chat-%03d", s)
		if err := source.store.CreateSession(ctx, &session.Session{ID: id, Title: id, AgentName: "Planner", FolderID: ws.ID,
			CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		for m := 0; m < perSession; m++ {
			role := session.RoleUser
			if m%2 == 1 {
				role = session.RoleAssistant
			}
			// Pairs share a timestamp: order must come from the source, not the clock.
			if err := source.store.AddMessage(ctx, id, &session.Message{Role: role, Content: fmt.Sprintf("%s message %03d", id, m),
				CreatedAt: at.Add(time.Duration(m/2) * time.Minute)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("seeded %d messages in %s", sessions*perSession, time.Since(seedStart))

	prepareStart := time.Now()
	if status, err := source.worker.PrepareNow(ctx, ws.ID); err != nil || status.State != continuityprep.StateReady {
		prep, _ := source.local.Preparation(ctx, ws.ID)
		t.Fatalf("large source not ready: %+v %v %+v", status, err, prep)
	}
	t.Logf("prepared in %s", time.Since(prepareStart))
	folder, _ := source.files.GetFolderPath(ws.ID)
	copied := copyContinuityFolder(t, folder)

	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	history, _ := review["history"].(map[string]any)
	if history["sessions"] != float64(sessions) || history["messages"] != float64(sessions*perSession) {
		t.Fatalf("review counts: %v", history)
	}
	importStart := time.Now()
	if code, payload := importWorkspaceOnly(t, dest, copied, review); code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}
	t.Logf("imported in %s", time.Since(importStart))
	var messages int
	if err := dest.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m JOIN sessions s ON s.id=m.session_id WHERE s.workspace_id=?`,
		ws.ID).Scan(&messages); err != nil || messages != sessions*perSession {
		t.Fatalf("restored %d messages, want %d: %v", messages, sessions*perSession, err)
	}
	last, err := dest.store.GetSession(ctx, "chat-099")
	if err != nil || len(last.Messages) != perSession {
		t.Fatalf("last conversation incomplete: %v", err)
	}
	for m, message := range last.Messages {
		if want := fmt.Sprintf("chat-099 message %03d", m); message.Content != want {
			t.Fatalf("message %d out of order: %q want %q", m, message.Content, want)
		}
	}
}
