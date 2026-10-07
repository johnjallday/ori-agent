package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

func TestAssistantTurnAttribution_AtomicRestartAndUntrustedInput(t *testing.T) {
	db, _ := setupTestDB(t)
	hybrid := NewHybridStoreWithDB(db, 10)
	writer := hybrid.(AssistantTurnStore)
	ctx := context.Background()
	chat := &Session{AgentName: "Atlas"}
	if err := hybrid.CreateSession(ctx, chat); err != nil {
		t.Fatal(err)
	}
	owner := assistantcontext.SaveOwner{AgentName: "Atlas"}
	attr := &assistantcontext.Attribution{Version: 1, Status: assistantcontext.Available, ReadAt: time.Now().UTC(), Subject: &assistantcontext.WorkspaceRef{ID: "project-a", Name: "Project A", Kind: "project", Version: 3}}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_attributed_answer BEFORE INSERT ON messages WHEN NEW.role='assistant' BEGIN SELECT RAISE(ABORT,'failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendAttributedTurn(ctx, chat.ID, owner, nil, "", "user", "reply", attr); err == nil {
		t.Fatal("half-turn accepted")
	}
	loaded, err := hybrid.GetSession(ctx, chat.ID)
	if err != nil || len(loaded.Messages) != 0 || loaded.MessageCount != 0 {
		t.Fatal("partial turn", loaded, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_attributed_answer`); err != nil {
		t.Fatal(err)
	}
	rows, err := writer.AppendAttributedTurn(ctx, chat.ID, owner, nil, "", "user", "reply", attr)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	restarted := NewHybridStoreWithDB(db, 10)
	loaded, err = restarted.GetSession(ctx, chat.ID)
	if err != nil || len(loaded.Messages) != 2 || loaded.MessageCount != 2 {
		t.Fatal("restart", loaded, err)
	}
	for _, msg := range loaded.Messages {
		if msg.WorkspaceContext == nil || !msg.WorkspaceContext.Historical || msg.WorkspaceContext.Subject.ID != "project-a" {
			t.Fatalf("attribution lost: %+v", msg)
		}
	}
	exported, _ := json.Marshal(loaded)
	if strings.Contains(string(exported), "project-a") {
		t.Fatal("generic JSON carried attribution")
	}
	var supplied Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"typed","workspace_context":{"version":1}}`), &supplied); err != nil {
		t.Fatal(err)
	}
	if supplied.WorkspaceContext != nil {
		t.Fatal("ordinary HTTP input authored attribution")
	}
	supplied.WorkspaceContext = attr
	if err := hybrid.AddMessage(ctx, chat.ID, &supplied); err != nil {
		t.Fatal(err)
	}
	if supplied.WorkspaceContext != nil {
		t.Fatal("generic writer cached attribution")
	}
	if _, err := db.ExecContext(ctx, `UPDATE messages SET continuity_source_sequence=1 WHERE id=?`, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	messages, err := restarted.(FolderContextStore).GetFolderMessages(ctx, chat.ID)
	if err != nil || !messages[0].Imported || !messages[0].WorkspaceContext.Historical {
		t.Fatal("imported attribution ceased to be historical", err)
	}
	if err := hybrid.DeleteSession(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendAttributedTurn(ctx, chat.ID, owner, nil, "", "later", "no", attr); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("deleted write", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id=?`, chat.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("retained deleted metadata", count, err)
	}
}

func TestAssistantTurnAttribution_FolderCASAndOwnerReplacement(t *testing.T) {
	db, _ := setupTestDB(t)
	hybrid := NewHybridStoreWithDB(db, 10)
	writer := hybrid.(AssistantTurnStore)
	ctx := context.Background()
	hq := &Workspace{Name: "HQ"}
	project := &Workspace{Name: "Project"}
	for _, ws := range []*Workspace{hq, project} {
		if err := hybrid.CreateWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_assistant_state(user_id,assistant_id,status,hq_workspace_id,global_agent_profile_name,state_version,created_at,updated_at) VALUES('local','fixture','active',?,'Atlas',3,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, hq.ID); err != nil {
		t.Fatal(err)
	}
	chat := &Session{AgentName: "Atlas", FolderID: hq.ID}
	if err := hybrid.CreateSession(ctx, chat); err != nil {
		t.Fatal(err)
	}
	owner := assistantcontext.SaveOwner{UserID: "local", WorkspaceID: hq.ID, AgentName: "Atlas", StateVersion: 3, ContextWorkspaceIDs: []string{project.ID}}
	attr := &assistantcontext.Attribution{Version: 1, Status: assistantcontext.Available, Subject: &assistantcontext.WorkspaceRef{ID: project.ID, Name: project.Name}, ReadAt: time.Now().UTC()}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			event := folderEventFixture()
			_, err := writer.AppendAttributedTurn(ctx, chat.ID, owner, &event, "", "user", "reply", attr)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrFolderContextConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("interleaved tabs", success, conflict)
	}
	loaded, err := hybrid.GetSession(ctx, chat.ID)
	if err != nil || len(loaded.Messages) != 3 || loaded.Messages[0].FolderContext == nil || loaded.Messages[1].WorkspaceContext == nil {
		t.Fatal("folder/context atomicity", loaded, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE personal_assistant_state SET state_version=4 WHERE user_id='local'`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendAttributedTurn(ctx, chat.ID, owner, nil, "", "old relationship", "no", attr); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("replacement state saved turn", err)
	}
	owner.StateVersion = 4
	if _, err := db.ExecContext(ctx, `UPDATE workspaces SET status='trashed' WHERE id=?`, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendAttributedTurn(ctx, chat.ID, owner, nil, "", "revoked context", "no", attr); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("revoked context saved turn", err)
	}
	messages, err := hybrid.GetMessages(ctx, chat.ID)
	if err != nil || len(messages) != 3 {
		t.Fatal("losing saves added rows", err)
	}
}
