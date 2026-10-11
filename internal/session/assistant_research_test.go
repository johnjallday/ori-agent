package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

func TestResearchAttributedSaveCanonicalRevisionCASAndReferenceOnlyRestart(t *testing.T) {
	db, _ := setupTestDB(t)
	store := NewHybridStoreWithDB(db, 10)
	ctx := context.Background()
	chat := &Session{AgentName: "Atlas"}
	if err := store.CreateSession(ctx, chat); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetSession(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := assistantcontext.SaveOwner{AgentName: "Atlas", ExpectedConversationRevision: assistantcontext.ConversationRevision(loaded.UpdatedAt, loaded.MessageCount)}
	now := time.Now().UTC()
	attr := &assistantcontext.Attribution{Version: 1, Status: assistantcontext.Available, ReadAt: now, Research: []assistantcontext.ResearchRef{{Key: "S1", SourceID: strings.Repeat("a", 32), CandidateID: strings.Repeat("b", 32), Kind: "public_document", Level: "document", Name: "Reference", URL: "https://example.com/docs", ReadAt: now, ObservedAt: now, Freshness: "current_observation"}}}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := store.(AssistantTurnStore).AppendAttributedTurn(ctx, chat.ID, owner, nil, "", "user", "answer [S1]", attr)
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
		t.Fatal("stale research response appended", success, conflict)
	}
	restarted := NewHybridStoreWithDB(db, 10)
	loaded, err = restarted.GetSession(ctx, chat.ID)
	if err != nil || len(loaded.Messages) != 2 {
		t.Fatal("research turn was not atomic", err)
	}
	for _, message := range loaded.Messages {
		if message.WorkspaceContext == nil || !message.WorkspaceContext.Historical || len(message.WorkspaceContext.Research) != 1 || message.WorkspaceContext.Research[0].URL != "https://example.com/docs" {
			t.Fatal("reference lost historical boundary")
		}
	}
}
func TestResearchFolderMetadataUsesOnlyLatestBoundedCanonicalEvent(t *testing.T) {
	db, _ := setupTestDB(t)
	store := NewHybridStoreWithDB(db, 10)
	ctx := context.Background()
	hq := &Workspace{Name: "HQ"}
	if err := store.CreateWorkspace(ctx, hq); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_assistant_state(user_id,assistant_id,status,hq_workspace_id,global_agent_profile_name,state_version,created_at,updated_at) VALUES('local','fixture','active',?,'Atlas',3,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, hq.ID); err != nil {
		t.Fatal(err)
	}
	chat := &Session{AgentName: "Atlas", FolderID: hq.ID}
	if err := store.CreateSession(ctx, chat); err != nil {
		t.Fatal(err)
	}
	owner := assistantcontext.SaveOwner{UserID: "local", WorkspaceID: hq.ID, AgentName: "Atlas", StateVersion: 3}
	event := folderEventFixture()
	rows, err := store.(AssistantTurnStore).AppendAttributedTurn(ctx, chat.ID, owner, &event, "", "question", "answer", nil)
	if err != nil {
		t.Fatal(err)
	}
	reader := store.(AssistantResearchFolderStore)
	ref, err := reader.ReadAssistantResearchFolder(ctx, chat.ID, owner)
	if err != nil || ref.Revision != rows[0].ID || ref.SelectionID != event.Observation.ID {
		t.Fatalf("folder identity: %+v %v", ref, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE messages SET folder_context_json=? WHERE id=?`, strings.Repeat("x", 100000), rows[0].ID); err == nil {
		t.Fatal("database accepted oversized event")
	}
	if _, err := db.ExecContext(ctx, `UPDATE messages SET folder_context_json='{}' WHERE id=?`, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadAssistantResearchFolder(ctx, chat.ID, owner); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("malformed latest event became authority", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE messages SET continuity_source_sequence=1 WHERE id=?`, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	ref, err = reader.ReadAssistantResearchFolder(ctx, chat.ID, owner)
	if err != nil || ref.Revision != "" {
		t.Fatal("import became folder authority", ref, err)
	}
	foreign := owner
	foreign.AgentName = "Other"
	if _, err := reader.ReadAssistantResearchFolder(ctx, chat.ID, foreign); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("foreign folder read", err)
	}
	if err := store.DeleteSession(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadAssistantResearchFolder(ctx, chat.ID, owner); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("deleted folder context resurrected", err)
	}
}
