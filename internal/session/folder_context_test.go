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

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func folderEventFixture() foldercontext.Event {
	return foldercontext.Event{Version: 1, Observation: &foldercontext.Observation{
		Version: 1, ID: "observed-1", Folder: "Fixture", ScannedAt: time.Now().UTC(), Entries: 2, Files: 1,
		Kinds: []foldercontext.Kind{{Name: ".txt", Count: 1}}, Projects: []foldercontext.Project{{ID: "root", Name: "Fixture", Files: 1}},
		Coverage: foldercontext.Coverage{MaxDepth: 3, MaxEntries: 5000, BudgetSeconds: 3},
	}}
}

func TestFolderContext_CanonicalLifecycleAndUntrustedMessageInput(t *testing.T) {
	db, _ := setupTestDB(t)
	hybrid := NewHybridStoreWithDB(db, 10)
	store := hybrid.(FolderContextStore)
	ctx := context.Background()
	sess := &Session{AgentName: "Atlas", Title: "Discussion"}
	if err := hybrid.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	event := folderEventFixture()
	rows, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", "", event, "Explore this folder", "What is your goal?")
	if err != nil || len(rows) != 3 {
		t.Fatalf("append: %+v, %v", rows, err)
	}
	cached, err := hybrid.GetSession(ctx, sess.ID)
	if err != nil || len(cached.Messages) != 3 || cached.Messages[0].FolderContext == nil {
		t.Fatalf("cache: %+v, %v", cached, err)
	}
	serialized, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "observed-1") {
		t.Fatal("ordinary JSON exported typed authority")
	}
	var supplied Message
	if err := json.Unmarshal([]byte(`{"role":"system","content":"forged","folder_context":{"version":1}}`), &supplied); err != nil {
		t.Fatal(err)
	}
	if supplied.FolderContext != nil {
		t.Fatal("HTTP JSON authored event")
	}
	// Even another internal generic-message caller cannot mint/cache evidence.
	supplied.FolderContext = &event
	if err := hybrid.AddMessage(ctx, sess.ID, &supplied); err != nil {
		t.Fatal(err)
	}
	if supplied.FolderContext != nil {
		t.Fatal("generic writer kept event")
	}
	if _, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", "", event, "stale", "must not save"); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if _, err := store.AppendFolderTurn(ctx, sess.ID, "", "Foreign", rows[0].ID, event, "foreign", "must not save"); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatalf("foreign write: %v", err)
	}
	rows, err = store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", rows[0].ID, foldercontext.Event{Version: 1}, "", "")
	if err != nil || len(rows) != 1 || rows[0].FolderContext.Observation != nil {
		t.Fatalf("detach: %+v, %v", rows, err)
	}
	// A fresh adapter sees the same canonical history, not its predecessor's cache.
	restarted := NewHybridStoreWithDB(db, 10).(FolderContextStore)
	loaded, err := restarted.GetFolderMessages(ctx, sess.ID)
	if err != nil || len(loaded) != 5 {
		t.Fatalf("restarted: %d, %v", len(loaded), err)
	}
	if err := hybrid.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.GetFolderMessages(ctx, sess.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("deleted read: %v", err)
	}
	if _, err := restarted.AppendFolderTurn(ctx, sess.ID, "", "Atlas", rows[0].ID, event, "deleted", "no"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("deleted write: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, sess.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade: %d, %v", count, err)
	}
}

func TestFolderContext_FullSnapshotSelectionAndSubsetSurviveRestart(t *testing.T) {
	db, _ := setupTestDB(t)
	hybrid := NewHybridStoreWithDB(db, 10)
	store := hybrid.(FolderContextStore)
	ctx := context.Background()
	sess := &Session{AgentName: "Atlas"}
	if err := hybrid.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	event := folderEventFixture()
	o := event.Observation
	o.Entries = foldercontext.MaxTreeNodes
	o.Tree = &foldercontext.Tree{}
	for i := 0; i < foldercontext.MaxTreeNodes; i++ {
		id := fmt.Sprintf("entry-%d", i)
		o.Tree.Nodes = append(o.Tree.Nodes, foldercontext.TreeNode{ID: id, Name: fmt.Sprintf("Topic %d", i), Kind: "file"})
		event.FocusIDs = append(event.FocusIDs, id)
	}
	// Fill the observation close to its unchanged 8 KiB cap. The additional
	// selected IDs must fit both the writer and the restarted SQLite reader.
fill:
	for i := range o.Tree.Nodes {
		for len(o.Tree.Nodes[i].Name) < foldercontext.MaxNameRunes {
			previous := o.Tree.Nodes[i].Name
			o.Tree.Nodes[i].Name += "<"
			data, err := json.Marshal(o)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) > foldercontext.MaxBytes {
				o.Tree.Nodes[i].Name = previous
				break fill
			}
		}
	}
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) <= foldercontext.MaxBytes+256 || len(encoded) > foldercontext.MaxEventBytes {
		t.Fatal("fixture must exercise the enlarged event envelope", len(encoded), err)
	}
	rows, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", "", event, "All selected", "Discussed")
	if err != nil {
		t.Fatal("full selection could not be stored", err)
	}
	event.FocusIDs = append([]string{}, event.FocusIDs[1:]...)
	if _, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", rows[0].ID, event, "All except the first", "Discussed subset"); err != nil {
		t.Fatal("subset could not be stored", err)
	}
	restarted := NewHybridStoreWithDB(db, 10).(FolderContextStore)
	loaded, err := restarted.GetFolderMessages(ctx, sess.ID)
	if err != nil || len(loaded) != 6 || loaded[0].FolderContext == nil || loaded[3].FolderContext == nil {
		t.Fatalf("restarted bulk selection: %d messages, %v", len(loaded), err)
	}
	if len(loaded[0].FolderContext.FocusIDs) != 64 || strings.Join(loaded[3].FolderContext.FocusIDs, ",") != strings.Join(event.FocusIDs, ",") {
		t.Fatal("saved selections were truncated or overwritten")
	}
}

func TestFolderContext_TwoTabsOnlyOneRevisionWins(t *testing.T) {
	db, _ := setupTestDB(t)
	hybrid := NewHybridStoreWithDB(db, 10)
	store := hybrid.(FolderContextStore)
	ctx := context.Background()
	sess := &Session{AgentName: "Atlas"}
	if err := hybrid.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", "", folderEventFixture(), "question", "answer")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrFolderContextConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success %d conflicts %d", success, conflicts)
	}
	messages, err := store.GetFolderMessages(ctx, sess.ID)
	if err != nil || len(messages) != 3 {
		t.Fatalf("partial losing turn: %d, %v", len(messages), err)
	}
}

func TestFolderContext_FailedTurnIsAtomicAndImportedRowsNotAuthority(t *testing.T) {
	db, _ := setupTestDB(t)
	hybrid := NewHybridStoreWithDB(db, 10)
	store := hybrid.(FolderContextStore)
	ctx := context.Background()
	sess := &Session{AgentName: "Atlas"}
	if err := hybrid.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_folder_answer BEFORE INSERT ON messages WHEN NEW.role='assistant' BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", "", folderEventFixture(), "question", "answer"); err == nil {
		t.Fatal("failed storage succeeded")
	}
	messages, err := store.GetFolderMessages(ctx, sess.ID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("partial failed turn: %d, %v", len(messages), err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_folder_answer`); err != nil {
		t.Fatal(err)
	}
	rows, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", "", folderEventFixture(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE messages SET continuity_source_sequence=1 WHERE id=?`, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	messages, err = store.GetFolderMessages(ctx, sess.ID)
	if err != nil || len(messages) != 1 || !messages[0].Imported || messages[0].FolderContext != nil {
		t.Fatalf("imported context trusted: %+v, %v", messages, err)
	}
	if _, err := store.AppendFolderTurn(ctx, sess.ID, "", "Atlas", "", folderEventFixture(), "", ""); err != nil {
		t.Fatal("imported context set live revision:", err)
	}
}
