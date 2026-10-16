package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// groupRecord is a saved PA proposal with two plain-group intentions over held
// candidates of the fixture observation.
func groupRecord(record workspacearrangement.Record) workspacearrangement.Record {
	record.ID = uuid.NewString()
	record.Origin = "manual"
	record.Draft.Operations = []workspacearrangement.Operation{
		{Kind: workspacearrangement.CreateGroup, Name: "Client work", Members: workspacearrangement.Selection{IDs: []string{"candidate-1", "candidate-2"}}},
		{Kind: workspacearrangement.CreateGroup, Name: "Experiments", Members: workspacearrangement.Selection{IDs: []string{"candidate-3"}}},
	}
	record.ItemRevisions = map[string]string{"candidate-1": "observed-1", "candidate-2": "observed-1", "candidate-3": "observed-1"}
	return record
}

func savedGroupRecord(t *testing.T, store HybridStore, chat *Session, owner assistantcontext.SaveOwner, record workspacearrangement.Record, revision string) (workspacearrangement.Record, string) {
	t.Helper()
	event := folderEventFixture()
	loaded, err := store.GetSession(t.Context(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner.ExpectedConversationRevision = assistantcontext.ConversationRevision(loaded.UpdatedAt, loaded.MessageCount, loaded.ContextEpoch)
	record = groupRecord(record)
	rows, err := store.(AssistantArrangementStore).AppendArrangementTurn(t.Context(), chat.ID, owner, &event, revision, "Organize these", "Here is a proposal", nil, record)
	if err != nil || len(rows) != 3 {
		t.Fatalf("save: %v %v", rows, err)
	}
	return record, rows[0].ID
}

func reviewOf(record workspacearrangement.Record, operation int, folderRevision, offer string) AssistantArrangementReview {
	event := folderEventFixture()
	event.OfferID = offer
	return AssistantArrangementReview{RecordID: record.ID, RecordRevision: record.Revision, Operation: operation, FolderRevision: folderRevision, Event: event}
}

func TestAssistantArrangementExecution_BindsOneReviewPerIntentionUnderCAS(t *testing.T) {
	db, store, chat, owner, base := arrangementStoreFixture(t)
	execution := store.(AssistantArrangementExecutionStore)
	record, folder := savedGroupRecord(t, store, chat, owner, base, "")
	// A stale folder revision, record revision, operation or observation binds nothing.
	stale := reviewOf(record, 0, "old-folder-row", "offer-1")
	if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, stale); !errors.Is(err, ErrFolderContextConflict) {
		t.Fatalf("stale folder revision: %v", err)
	}
	for name, mutate := range map[string]func(*AssistantArrangementReview){
		"old record revision": func(r *AssistantArrangementReview) { r.RecordRevision = 2 },
		"missing operation":   func(r *AssistantArrangementReview) { r.Operation = 2 },
		"other observation":   func(r *AssistantArrangementReview) { r.Event.Observation.ID = "observed-2" },
		"unknown record":      func(r *AssistantArrangementReview) { r.RecordID = uuid.NewString() },
	} {
		review := reviewOf(record, 0, folder, "offer-1")
		mutate(&review)
		if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, review); err == nil {
			t.Fatalf("%s bound a review", name)
		}
	}
	foreign := owner
	foreign.StateVersion = 4
	if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, foreign, reviewOf(record, 0, folder, "offer-1")); err == nil {
		t.Fatal("a replaced assistant bound a review")
	}
	rows, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, reviewOf(record, 0, folder, "offer-1"))
	if err != nil || len(rows) != 1 || rows[0].FolderContext == nil || rows[0].FolderContext.OfferID != "offer-1" {
		t.Fatalf("bind: %+v %v", rows, err)
	}
	folder = rows[0].ID
	// The review's own event keeps a current record current.
	if read, err := store.(AssistantArrangementStore).ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID); err != nil || !read.ContextCurrent {
		t.Fatalf("the review's own event staled the record: %+v %v", read, err)
	}
	effects, err := execution.ListAssistantArrangementEffects(t.Context(), chat.ID, owner, record.ID)
	if err != nil || len(effects) != 1 || effects[0].OfferID != "offer-1" || effects[0].State != ArrangementEffectAwaitingReview || effects[0].MessageID != folder {
		t.Fatalf("effects: %+v %v", effects, err)
	}
	// The same open review may be bound again on a fresh event; another review
	// of the same intention may not replace it while it is open.
	rows, err = execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, reviewOf(record, 0, folder, "offer-1"))
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	folder = rows[0].ID
	if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, reviewOf(record, 0, folder, "offer-2")); !errors.Is(err, ErrArrangementEffectConflict) {
		t.Fatalf("replaced an open review: %v", err)
	}
	// Closing is recorded by reconciliation; a closed review may be replaced.
	open := effects[0]
	closed := open
	closed.State = ArrangementEffectClosed
	if err := execution.ReconcileAssistantArrangementEffect(t.Context(), chat.ID, owner, record.ID, open, closed); err != nil {
		t.Fatal(err)
	}
	rows, err = execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, reviewOf(record, 0, folder, "offer-2"))
	if err != nil {
		t.Fatalf("replace closed review: %v", err)
	}
	folder = rows[0].ID
	effects, _ = execution.ListAssistantArrangementEffects(t.Context(), chat.ID, owner, record.ID)
	awaiting := effects[0]
	if awaiting.OfferID != "offer-2" || awaiting.State != ArrangementEffectAwaitingReview {
		t.Fatalf("replaced effect: %+v", awaiting)
	}
	applying := awaiting
	applying.State = ArrangementEffectApplying
	if err := execution.ReconcileAssistantArrangementEffect(t.Context(), chat.ID, owner, record.ID, awaiting, applying); err != nil {
		t.Fatal(err)
	}
	// A confirmed run cannot be replaced, closed, or completed without its receipt.
	if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, reviewOf(record, 0, folder, "offer-3")); !errors.Is(err, ErrArrangementEffectConflict) {
		t.Fatalf("replaced a confirmed run: %v", err)
	}
	for name, next := range map[string]AssistantArrangementEffect{
		"closed after confirmation": {Operation: 0, OfferID: "offer-2", State: ArrangementEffectClosed},
		"completed without receipt": {Operation: 0, OfferID: "offer-2", State: ArrangementEffectCompleted, WorkspaceID: "group-1"},
		"another offer":             {Operation: 0, OfferID: "offer-9", State: ArrangementEffectPartial},
		"a route that is not ours":  {Operation: 0, OfferID: "offer-2", State: ArrangementEffectCompleted, WorkspaceID: "g", Workspaces: 2, ReceiptDigest: strings.Repeat("a", 64), Route: "https://example.com"},
	} {
		if err := execution.ReconcileAssistantArrangementEffect(t.Context(), chat.ID, owner, record.ID, applying, next); err == nil {
			t.Fatalf("%s was recorded", name)
		}
	}
	// A stale expected state is a CAS conflict.
	if err := execution.ReconcileAssistantArrangementEffect(t.Context(), chat.ID, owner, record.ID, awaiting, applying); !errors.Is(err, ErrArrangementEffectConflict) {
		t.Fatalf("stale reconcile: %v", err)
	}
	completed := applying
	completed.State, completed.WorkspaceID, completed.Route, completed.Workspaces, completed.ReceiptDigest = ArrangementEffectCompleted, "group-1", "/workspaces/client-work", 2, strings.Repeat("b", 64)
	if err := execution.ReconcileAssistantArrangementEffect(t.Context(), chat.ID, owner, record.ID, applying, completed); err != nil {
		t.Fatal(err)
	}
	// Work already done is never undone by declining.
	if _, err := execution.DeclineAssistantArrangement(t.Context(), chat.ID, owner, record.ID, record.Revision); !errors.Is(err, ErrArrangementEffectConflict) {
		t.Fatalf("declined applied work: %v", err)
	}
	restarted := NewHybridStoreWithDB(db, 10).(AssistantArrangementExecutionStore)
	effects, err = restarted.ListAssistantArrangementEffects(t.Context(), chat.ID, owner, record.ID)
	if err != nil || len(effects) != 1 || effects[0].State != ArrangementEffectCompleted || effects[0].WorkspaceID != "group-1" || effects[0].Workspaces != 2 {
		t.Fatalf("restart: %+v %v", effects, err)
	}
	// The table itself refuses a completion without its receipt.
	if _, err := db.ExecContext(t.Context(), `UPDATE assistant_arrangement_effects SET receipt_digest='' WHERE arrangement_id=?`, record.ID); err == nil {
		t.Fatal("schema accepted a completion without a receipt digest")
	}
	if err := store.DeleteSession(t.Context(), chat.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_arrangement_effects`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted conversation retained effects", err)
	}
}

func TestAssistantArrangementExecution_DeclineFencesOpenReviewsAndKeepsHistory(t *testing.T) {
	_, store, chat, owner, base := arrangementStoreFixture(t)
	execution := store.(AssistantArrangementExecutionStore)
	record, folder := savedGroupRecord(t, store, chat, owner, base, "")
	// An unrelated turn moves the conversation on; a later review must not
	// make that older pin current again.
	unrelated := owner
	unrelated.ExpectedConversationRevision = ""
	if _, err := store.(AssistantTurnStore).AppendAttributedTurn(t.Context(), chat.ID, unrelated, nil, "", "Why these two?", "They share a client.", nil); err != nil {
		t.Fatal(err)
	}
	rows, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, reviewOf(record, 1, folder, "offer-1"))
	if err != nil {
		t.Fatal(err)
	}
	folder = rows[0].ID
	if read, err := store.(AssistantArrangementStore).ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID); err != nil || read.ContextCurrent {
		t.Fatalf("a review laundered an unrelated change into a current pin: %+v %v", read, err)
	}
	if _, err := execution.DeclineAssistantArrangement(t.Context(), chat.ID, owner, record.ID, 2); !errors.Is(err, workspacearrangement.ErrStale) {
		t.Fatalf("declined a revision that does not exist: %v", err)
	}
	declined, err := execution.DeclineAssistantArrangement(t.Context(), chat.ID, owner, record.ID, 1)
	if err != nil || declined.State != "cancelled" || declined.Revision != 2 || len(declined.Draft.Operations) != 2 {
		t.Fatalf("decline: %+v %v", declined, err)
	}
	effects, err := execution.ListAssistantArrangementEffects(t.Context(), chat.ID, owner, record.ID)
	if err != nil || len(effects) != 1 || effects[0].State != ArrangementEffectClosed {
		t.Fatalf("open review not fenced: %+v %v", effects, err)
	}
	read, err := store.(AssistantArrangementStore).ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID)
	if err != nil || read.Record.State != "cancelled" || read.Record.Draft.Goal == "" {
		t.Fatalf("history after decline: %+v %v", read, err)
	}
	// Neither the old revision nor the cancelled one can be reviewed or declined again.
	for _, revision := range []int64{1, 2} {
		review := reviewOf(record, 0, folder, "offer-2")
		review.RecordRevision = revision
		if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, review); !errors.Is(err, workspacearrangement.ErrStale) {
			t.Fatalf("revision %d reviewed after decline: %v", revision, err)
		}
		if _, err := execution.DeclineAssistantArrangement(t.Context(), chat.ID, owner, record.ID, revision); !errors.Is(err, workspacearrangement.ErrStale) {
			t.Fatalf("revision %d declined twice: %v", revision, err)
		}
	}
}

func TestAssistantArrangementExecution_ImportedAndForeignRecordsAreHistoryOnly(t *testing.T) {
	db, store, chat, owner, base := arrangementStoreFixture(t)
	execution := store.(AssistantArrangementExecutionStore)
	record, folder := savedGroupRecord(t, store, chat, owner, base, "")
	other := owner
	other.UserID = "someone-else"
	if _, err := execution.ListAssistantArrangementEffects(t.Context(), chat.ID, other, record.ID); err == nil {
		t.Fatal("a foreign owner listed effects")
	}
	// Mark the originating answer as imported history.
	if _, err := db.ExecContext(t.Context(), `UPDATE messages SET continuity_source_sequence=1 WHERE id=(SELECT message_id FROM assistant_arrangements WHERE id=?)`, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, reviewOf(record, 0, folder, "offer-1")); !errors.Is(err, workspacearrangement.ErrStale) {
		t.Fatalf("imported history bound a review: %v", err)
	}
	read, err := store.(AssistantArrangementStore).ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID)
	if err != nil || !read.Imported {
		t.Fatalf("imported flag: %+v %v", read, err)
	}
	if _, err := execution.BindAssistantArrangementReview(t.Context(), chat.ID, owner, AssistantArrangementReview{RecordID: record.ID, RecordRevision: 1, FolderRevision: folder, Event: foldercontext.Event{Version: 1}}); err == nil {
		t.Fatal("a review without an observation or offer was bound")
	}
}
