package session

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

func TestAssistantConversationOwner_MetadataOnlyAndCanonicalOwnership(t *testing.T) {
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
	if err := store.AddMessage(ctx, chat.ID, &Message{Role: RoleUser, Content: "PRIVATE_BODY"}); err != nil {
		t.Fatal(err)
	}
	// Poison the ordinary cached projection: metadata reads must bypass it.
	if _, err := store.GetSession(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	owner := assistantcontext.SaveOwner{UserID: "local", WorkspaceID: hq.ID, AgentName: "Atlas", StateVersion: 3}
	reader := store.(AssistantConversationOwnerStore)
	loaded, err := reader.ReadAssistantConversationOwner(ctx, chat.ID, owner)
	if err != nil || loaded == nil || loaded.ID != chat.ID || loaded.FolderID != hq.ID || len(loaded.Messages) != 0 || loaded.Title != "" {
		t.Fatalf("metadata-only read: %+v %v", loaded, err)
	}
	for _, change := range []func(*assistantcontext.SaveOwner){
		func(o *assistantcontext.SaveOwner) { o.UserID = "foreign" },
		func(o *assistantcontext.SaveOwner) { o.WorkspaceID = "foreign" },
		func(o *assistantcontext.SaveOwner) { o.AgentName = "foreign" },
		func(o *assistantcontext.SaveOwner) { o.StateVersion++ },
		func(o *assistantcontext.SaveOwner) { o.UserID = "" },
	} {
		bad := owner
		change(&bad)
		if result, err := reader.ReadAssistantConversationOwner(ctx, chat.ID, bad); result != nil || !errors.Is(err, ErrFolderContextConflict) {
			t.Fatalf("invalid owner accepted: %+v %v", result, err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE personal_assistant_state SET status='paused' WHERE user_id='local'`); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadAssistantConversationOwner(ctx, chat.ID, owner); err != nil {
		t.Fatal("paused direct request refused", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE sessions SET agent_name='Replacement' WHERE id=?`, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadAssistantConversationOwner(ctx, chat.ID, owner); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("stale cache owner accepted", err)
	}
	if err := store.DeleteSession(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadAssistantConversationOwner(ctx, chat.ID, owner); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("deleted reference accepted", err)
	}
}
