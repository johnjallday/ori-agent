package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func arrangementStoreFixture(t *testing.T) (*database.DB, HybridStore, *Session, assistantcontext.SaveOwner, workspacearrangement.Record) {
	t.Helper()
	db, _ := setupTestDB(t)
	store := NewHybridStoreWithDB(db, 10)
	hq := &Workspace{Name: "HQ"}
	if err := store.CreateWorkspace(t.Context(), hq); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO personal_assistant_state(user_id,assistant_id,status,hq_workspace_id,global_agent_profile_name,state_version,created_at,updated_at) VALUES('local','fixture','active',?,'Atlas',3,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, hq.ID); err != nil {
		t.Fatal(err)
	}
	chat := &Session{AgentName: "Atlas", FolderID: hq.ID}
	if err := store.CreateSession(t.Context(), chat); err != nil {
		t.Fatal(err)
	}
	chat, err := store.GetSession(t.Context(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := assistantcontext.SaveOwner{UserID: "local", WorkspaceID: hq.ID, AgentName: "Atlas", StateVersion: 3, ExpectedConversationRevision: assistantcontext.ConversationRevision(chat.UpdatedAt, chat.MessageCount, chat.ContextEpoch)}
	now := time.Now().UTC()
	record := workspacearrangement.Record{Version: 1, ID: uuid.NewString(), Revision: 1, State: "draft", Origin: "manual", SourceKind: workspacearrangement.Attachment, SourceID: "observed-1",
		Draft: workspacearrangement.Draft{Version: 1, SourceRevision: "source-revision", Goal: "Leave these folders unchanged"}, Coverage: workspacearrangement.Coverage{Origin: "held_attachment", Note: "Held metadata only"}, CreatedAt: now, UpdatedAt: now}
	return db, store, chat, owner, record
}

func TestAssistantArrangement_AtomicTurnRestartAndContextInvalidation(t *testing.T) {
	db, store, chat, owner, record := arrangementStoreFixture(t)
	writer := store.(AssistantArrangementStore)
	event := folderEventFixture()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_arrangement BEFORE INSERT ON assistant_arrangements BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, "", "Arrange these", "Saved inert draft", nil, record); err == nil {
		t.Fatal("partial draft save succeeded")
	}
	loaded, err := store.GetSession(t.Context(), chat.ID)
	if err != nil || loaded.MessageCount != 0 || len(loaded.Messages) != 0 {
		t.Fatal("failed draft left a half-turn", err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_arrangement`); err != nil {
		t.Fatal(err)
	}
	rows, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, "", "Arrange these", "Saved inert draft", nil, record)
	if err != nil || len(rows) != 3 {
		t.Fatalf("save: %v %v", rows, err)
	}
	restarted := NewHybridStoreWithDB(db, 10).(AssistantArrangementStore)
	read, err := restarted.ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID)
	if err != nil || !read.ContextCurrent || read.MessageID != rows[2].ID || read.FolderRevision != rows[0].ID || read.Record.ID != record.ID {
		t.Fatalf("restart: %+v %v", read, err)
	}
	refs, err := restarted.ListAssistantArrangementRefs(t.Context(), chat.ID, owner)
	if err != nil || len(refs) != 1 || refs[0].ID != record.ID || refs[0].MessageID != rows[2].ID || !refs[0].ContextCurrent {
		t.Fatalf("hydration refs: %+v %v", refs, err)
	}
	loaded, err = store.GetSession(t.Context(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := json.Marshal(loaded)
	if strings.Contains(string(exported), record.ID) {
		t.Fatal("portable transcript exported device-local arrangement")
	}
	fresh := owner
	fresh.ExpectedConversationRevision = assistantcontext.ConversationRevision(loaded.UpdatedAt, loaded.MessageCount, loaded.ContextEpoch)
	if _, err := writer.AppendArrangementTurn(t.Context(), chat.ID, fresh, &event, rows[0].ID, "Replay", "Replay", nil, record); err == nil {
		t.Fatal("existing record ID was replaced")
	}
	// Old context is a CAS conflict, not permission to create a second record.
	if _, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, rows[0].ID, "Again", "Again", nil, record); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatal("old tab saved", err)
	}
	if _, err := store.(FolderContextStore).AppendFolderTurn(t.Context(), chat.ID, owner.WorkspaceID, owner.AgentName, rows[0].ID, foldercontext.Event{Version: 1}, "", ""); err != nil {
		t.Fatal(err)
	}
	read, err = restarted.ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID)
	if err != nil || read.ContextCurrent || read.Record.Draft.Goal == "" {
		t.Fatal("detach erased history or retained context authority", err)
	}
	refs, err = restarted.ListAssistantArrangementRefs(t.Context(), chat.ID, owner)
	if err != nil || len(refs) != 1 || refs[0].ContextCurrent {
		t.Fatal("history references revived detached context", err)
	}
	if err := store.DeleteSession(t.Context(), chat.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_arrangements`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted conversation retained draft", err)
	}
}

func TestAssistantArrangement_RejectsForeignUnboundAndReplacedOwners(t *testing.T) {
	db, store, chat, owner, record := arrangementStoreFixture(t)
	writer := store.(AssistantArrangementStore)
	event := folderEventFixture()
	for _, mutate := range []func(*assistantcontext.SaveOwner){
		func(o *assistantcontext.SaveOwner) { o.UserID = "" },
		func(o *assistantcontext.SaveOwner) { o.UserID = "foreign" },
		func(o *assistantcontext.SaveOwner) { o.AgentName = "Other" },
		func(o *assistantcontext.SaveOwner) { o.ExpectedConversationRevision = "" },
		func(o *assistantcontext.SaveOwner) { o.StateVersion++ },
	} {
		bad := owner
		mutate(&bad)
		if _, err := writer.AppendArrangementTurn(t.Context(), chat.ID, bad, &event, "", "Arrange", "Saved", nil, record); err == nil {
			t.Fatal("unbound save accepted")
		}
	}
	badRecord := record
	badRecord.SourceID = "other-attachment"
	if _, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, "", "Arrange", "Saved", nil, badRecord); err == nil {
		t.Fatal("foreign attachment saved")
	}
	if _, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, "", "Arrange", "Saved", nil, record); err != nil {
		t.Fatal(err)
	}
	foreign := owner
	foreign.UserID = "foreign"
	if got, err := writer.ReadAssistantArrangement(t.Context(), chat.ID, foreign, record.ID); err == nil || got != nil {
		t.Fatal("foreign owner read draft")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE personal_assistant_state SET state_version=4 WHERE user_id='local'`); err != nil {
		t.Fatal(err)
	}
	if got, err := writer.ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID); err == nil || got != nil {
		t.Fatal("stale owner read draft")
	}
	owner.StateVersion = 4
	got, err := writer.ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID)
	if err != nil || got.ContextCurrent {
		t.Fatal("replacement relationship revived old context", err)
	}
}

func TestAssistantArrangement_ConcurrentTabsAndImportedTextCannotCreateExtraRecords(t *testing.T) {
	db, store, chat, owner, record := arrangementStoreFixture(t)
	writer := store.(AssistantArrangementStore)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			candidate := record
			candidate.ID = uuid.NewString()
			event := folderEventFixture()
			_, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, "", "Arrange", "Saved", nil, candidate)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrFolderContextConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent tabs: wins=%d conflicts=%d", winners, conflicts)
	}
	encoded, _ := json.Marshal(record)
	if err := store.AddMessage(t.Context(), chat.ID, &Message{Role: RoleAssistant, Content: string(encoded)}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_arrangements`).Scan(&count); err != nil || count != 1 {
		t.Fatal("message text created a record", err)
	}
	refs, err := writer.ListAssistantArrangementRefs(t.Context(), chat.ID, owner)
	if err != nil || len(refs) != 1 || refs[0].ContextCurrent {
		t.Fatal("new message retained old context authority", err)
	}
}

func TestAssistantArrangement_ByteLimitRejectsWholeTurn(t *testing.T) {
	db, store, chat, owner, record := arrangementStoreFixture(t)
	writer := store.(AssistantArrangementStore)
	record.ItemRevisions = map[string]string{}
	for i := 0; i < 1500; i++ {
		id := fmt.Sprintf("item-%04d-%s", i, strings.Repeat("x", 140))
		record.Draft.Unchanged.IDs = append(record.Draft.Unchanged.IDs, id)
		record.ItemRevisions[id] = strings.Repeat("r", 160)
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	event := folderEventFixture()
	event.Observation.Entries = 1500
	folderRevision, saved := "", 0
	for ; saved < workspacearrangement.MaxContextRecords; saved++ {
		current, err := store.GetSession(t.Context(), chat.ID)
		if err != nil {
			t.Fatal(err)
		}
		owner.ExpectedConversationRevision = assistantcontext.ConversationRevision(current.UpdatedAt, current.MessageCount, current.ContextEpoch)
		record.ID = uuid.NewString()
		rows, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, folderRevision, "Arrange", "Saved", nil, record)
		if errors.Is(err, workspacearrangement.ErrLimit) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		folderRevision = rows[0].ID
	}
	if saved == 0 || saved >= workspacearrangement.MaxContextRecords {
		t.Fatalf("byte cap was not exercised: %d", saved)
	}
	var count, bytes int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*),SUM(length(CAST(record_json AS BLOB))) FROM assistant_arrangements`).Scan(&count, &bytes); err != nil || count != saved || bytes > workspacearrangement.MaxContextRecordBytes {
		t.Fatal("storage bounds", err)
	}
	current, err := store.GetSession(t.Context(), chat.ID)
	if err != nil || current.MessageCount != saved*3 {
		t.Fatal("overflow left half-turn", err)
	}
}

func TestAssistantArrangement_RecordLimitRejectsWholeTurnWithoutEviction(t *testing.T) {
	db, store, chat, owner, record := arrangementStoreFixture(t)
	writer := store.(AssistantArrangementStore)
	event := folderEventFixture()
	folderRevision := ""
	for i := 0; i <= workspacearrangement.MaxContextRecords; i++ {
		current, err := store.GetSession(t.Context(), chat.ID)
		if err != nil {
			t.Fatal(err)
		}
		owner.ExpectedConversationRevision = assistantcontext.ConversationRevision(current.UpdatedAt, current.MessageCount, current.ContextEpoch)
		record.ID = uuid.NewString()
		rows, err := writer.AppendArrangementTurn(t.Context(), chat.ID, owner, &event, folderRevision, "Arrange", "Saved", nil, record)
		if i == workspacearrangement.MaxContextRecords {
			if !errors.Is(err, workspacearrangement.ErrLimit) {
				t.Fatal("record cap not enforced", err)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		folderRevision = rows[0].ID
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_arrangements`).Scan(&count); err != nil || count != workspacearrangement.MaxContextRecords {
		t.Fatal("drafts evicted", count, err)
	}
	loaded, err := store.GetSession(t.Context(), chat.ID)
	if err != nil || loaded.MessageCount != workspacearrangement.MaxContextRecords*3 {
		t.Fatal("overflow left an answered unsaved turn", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := writer.ReadAssistantArrangement(ctx, chat.ID, owner, record.ID); err == nil {
		t.Fatal("cancelled read succeeded")
	}
}
