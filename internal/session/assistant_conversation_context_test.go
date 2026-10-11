package session

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/database"
)

func assistantContextFixture(t *testing.T) (*database.DB, HybridStore, *Session, assistantcontext.SaveOwner) {
	t.Helper()
	db, _ := setupTestDB(t)
	hybrid := NewHybridStoreWithDB(db, 10)
	hq := &Workspace{Name: "HQ"}
	if err := hybrid.CreateWorkspace(t.Context(), hq); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO personal_assistant_state(user_id,assistant_id,status,hq_workspace_id,global_agent_profile_name,state_version,created_at,updated_at) VALUES('local','fixture','active',?,'Atlas',3,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, hq.ID); err != nil {
		t.Fatal(err)
	}
	chat := &Session{AgentName: "Atlas", FolderID: hq.ID}
	if err := hybrid.CreateSession(t.Context(), chat); err != nil {
		t.Fatal(err)
	}
	for i := range 90 {
		body := strings.Repeat("界", 2000)
		if i == 0 {
			body = "My goal is community membership. No talent coaching."
		}
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		if err := hybrid.AddMessage(t.Context(), chat.ID, &Message{Role: role, Content: body}); err != nil {
			t.Fatal(err)
		}
	}
	return db, hybrid, chat, assistantcontext.SaveOwner{UserID: "local", WorkspaceID: hq.ID, AgentName: "Atlas", StateVersion: 3}
}

func contextRecap(snapshot *AssistantConversationContext) *assistantcontext.ConversationRecap {
	return &assistantcontext.ConversationRecap{Version: 1, Items: []assistantcontext.RecapItem{{Kind: "user_goal", MessageID: snapshot.Batch[0].ID, Quote: "My goal is community membership."}}}
}

func TestAssistantContext_BoundedIncrementalRestart(t *testing.T) {
	db, hybrid, chat, owner := assistantContextFixture(t)
	store := hybrid.(AssistantConversationContextStore)
	snapshot, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || len(snapshot.Recent) == 0 || len(snapshot.Recent) > 40 || !snapshot.HasOlder || len(snapshot.Batch) > 32 || len(snapshot.Older) > 128 {
		t.Fatal(snapshot, err)
	}
	batchRunes := 0
	for _, source := range snapshot.Batch {
		batchRunes += utf8.RuneCountInString(source.Content)
		if utf8.RuneCountInString(source.Content) > 1000 {
			t.Fatal("source bound")
		}
	}
	if batchRunes > 12000 {
		t.Fatal("batch bound")
	}
	if err := store.SaveAssistantConversationRecap(t.Context(), chat.ID, owner, snapshot.Pin, snapshot.NextThrough, contextRecap(snapshot)); err != nil {
		t.Fatal(err)
	}
	restarted := NewHybridStoreWithDB(db, 10).(AssistantConversationContextStore)
	loaded, err := restarted.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || loaded.Recap == nil || loaded.Pin.Generation != 1 || loaded.Pin.Through != snapshot.NextThrough || loaded.Batch[0].ID == snapshot.Batch[0].ID {
		t.Fatal("restart/incremental", loaded, err)
	}
	if err := hybrid.AddMessage(t.Context(), chat.ID, &Message{Role: RoleUser, Content: "Later local discussion"}); err != nil {
		t.Fatal(err)
	}
	loaded, err = restarted.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || loaded.Recap == nil {
		t.Fatal("append discarded covered recap", err)
	}
	generic, err := hybrid.GetSession(t.Context(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(generic)
	if strings.Contains(string(data), "checkpoint") || strings.Contains(string(data), "context_epoch") {
		t.Fatal("generic export carried derived data")
	}
}

func TestAssistantContext_CASMutationAndDeletion(t *testing.T) {
	db, hybrid, chat, owner := assistantContextFixture(t)
	store := hybrid.(AssistantConversationContextStore)
	snapshot, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			results <- store.SaveAssistantConversationRecap(t.Context(), chat.ID, owner, snapshot.Pin, snapshot.NextThrough, contextRecap(snapshot))
		})
	}
	wg.Wait()
	close(results)
	won, lost := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, ErrFolderContextConflict) {
			lost++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatal("CAS", won, lost)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE messages SET content='Edited source of same ownership' WHERE id=?`, snapshot.Batch[0].ID); err != nil {
		t.Fatal(err)
	}
	changed, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || changed.Recap != nil || changed.Pin.Epoch <= snapshot.Pin.Epoch || changed.Pin.Revision == snapshot.Pin.Revision {
		t.Fatal("mutation eligibility", changed, err)
	}
	if err := store.SaveAssistantConversationRecap(t.Context(), chat.ID, owner, snapshot.Pin, snapshot.NextThrough, contextRecap(snapshot)); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("resurrected edited recap", err)
	}
	if err := hybrid.DeleteSession(t.Context(), chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal(err)
	}
	if err := store.SaveAssistantConversationRecap(t.Context(), chat.ID, owner, changed.Pin, changed.NextThrough, &assistantcontext.ConversationRecap{Version: 1, Items: []assistantcontext.RecapItem{}}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("deleted write", err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_conversation_checkpoints`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cascade", count, err)
	}
}

func TestAssistantContext_ExactDraftAndOldFolderReadsStayCanonical(t *testing.T) {
	_, hybrid, chat, owner := assistantContextFixture(t)
	long := &Message{Role: RoleAssistant, Content: strings.Repeat("原", 12000)}
	if err := hybrid.AddMessage(t.Context(), chat.ID, long); err != nil {
		t.Fatal(err)
	}
	reader := hybrid.(AssistantContextMessageStore)
	_, exact, err := reader.ReadAssistantContextMessage(t.Context(), chat.ID, owner, long.ID)
	if err != nil || exact.Content != long.Content || exact.ContentTruncated {
		t.Fatal("draft source clipped", err)
	}
	context, err := hybrid.(AssistantConversationContextStore).ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || !context.Recent[len(context.Recent)-1].ContentTruncated {
		t.Fatal("model projection not marked shortened", err)
	}
	other := &Session{AgentName: owner.AgentName, FolderID: owner.WorkspaceID}
	if err := hybrid.CreateSession(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if _, foreign, err := reader.ReadAssistantContextMessage(t.Context(), other.ID, owner, long.ID); err != nil || foreign != nil {
		t.Fatal("cross-thread message returned", foreign, err)
	}
	event := folderEventFixture()
	rows, err := hybrid.(AssistantTurnStore).AppendAttributedTurn(t.Context(), chat.ID, owner, &event, "", "Folder discussion", "No setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if err := hybrid.AddMessage(t.Context(), chat.ID, &Message{Role: RoleUser, Content: strings.Repeat("Other topic. ", 200)}); err != nil {
			t.Fatal(err)
		}
	}
	context, err = hybrid.(AssistantConversationContextStore).ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || context.Recent[0].ID != rows[0].ID || context.Recent[0].FolderContext == nil {
		t.Fatal("old latest folder vanished", err)
	}
	_, saved, err := reader.ReadAssistantContextFolderEvent(t.Context(), chat.ID, owner, "", event.Observation.ID)
	if err != nil || saved == nil || saved.ID != rows[0].ID {
		t.Fatal("exact event missing", err)
	}
	_, missing, err := reader.ReadAssistantContextFolderEvent(t.Context(), other.ID, owner, "", event.Observation.ID)
	if err != nil || missing != nil {
		t.Fatal("cross-thread event", missing, err)
	}
	display, omitted, err := hybrid.(AssistantConversationDisplayStore).ReadAssistantConversationDisplay(t.Context(), chat.ID, owner)
	if err != nil || !omitted || len(display.Messages) > 601 {
		t.Fatal("display bound", err)
	}
	runes := 0
	for _, message := range display.Messages {
		runes += utf8.RuneCountInString(message.Content)
	}
	if runes > 240000 {
		t.Fatal("display text allowance", runes)
	}
}

func TestAssistantContext_HQDeletionDiscardsDerivedContextWithoutMovingAuthority(t *testing.T) {
	db, hybrid, chat, owner := assistantContextFixture(t)
	store := hybrid.(AssistantConversationContextStore)
	snapshot, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAssistantConversationRecap(t.Context(), chat.ID, owner, snapshot.Pin, snapshot.NextThrough, contextRecap(snapshot)); err != nil {
		t.Fatal(err)
	}
	if err := hybrid.DeleteWorkspace(t.Context(), owner.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_conversation_checkpoints WHERE session_id=?`, chat.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("HQ deletion retained recap", count, err)
	}
	if result, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner); result != nil || !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("orphan moved authority", result, err)
	}
}

func TestAssistantContext_ForeignReplacementAndImport(t *testing.T) {
	db, hybrid, chat, owner := assistantContextFixture(t)
	store := hybrid.(AssistantConversationContextStore)
	foreign := owner
	foreign.UserID = "foreign"
	if snapshot, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, foreign); !errors.Is(err, ErrFolderContextConflict) || snapshot != nil {
		t.Fatal("foreign bodies", snapshot, err)
	}
	snapshot, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAssistantConversationRecap(t.Context(), chat.ID, owner, snapshot.Pin, snapshot.NextThrough, contextRecap(snapshot)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE personal_assistant_state SET state_version=4 WHERE user_id='local'`); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner); !errors.Is(err, ErrFolderContextConflict) || snapshot != nil {
		t.Fatal("old relationship bodies", snapshot, err)
	}
	owner.StateVersion = 4
	fresh, err := store.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || !fresh.StaleDiscarded || fresh.Recap != nil {
		t.Fatal("replacement recap", fresh, err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE messages SET continuity_source_sequence=1 WHERE id=?`, fresh.Batch[0].ID); err != nil {
		t.Fatal(err)
	}
	fresh, err = store.ReadAssistantConversationContext(t.Context(), chat.ID, owner)
	if err != nil || !fresh.Batch[0].Imported {
		t.Fatal("import label", err)
	}
	forged := &assistantcontext.ConversationRecap{Version: 1, Items: []assistantcontext.RecapItem{{Kind: "user_goal", MessageID: fresh.Batch[0].ID, Quote: "My goal is community membership."}}}
	if err := store.SaveAssistantConversationRecap(t.Context(), chat.ID, owner, fresh.Pin, fresh.NextThrough, forged); err == nil {
		t.Fatal("import promoted to local preference")
	}
}
