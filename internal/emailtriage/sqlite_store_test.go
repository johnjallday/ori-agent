package emailtriage

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)
	store.now = func() time.Time { return base }
	return store
}

func TestSQLiteStoreRoundTripsAndReplaces(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	scope := Scope{WorkspaceID: "ws", AccountID: "acct"}
	first := State{
		ThreadID: "t1", LastMessageAt: base, Bucket: BucketNeedsYou, Kind: KindReply, Rule: RuleModel,
		Why: "Sam asks about Friday.", Subject: "Offsite", From: "Sam Lee", UpdatedAt: base,
	}
	if err := store.Save(ctx, scope, []State{first}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load(ctx, scope)
	if err != nil || got["t1"] != first {
		t.Fatalf("Load = %+v, %v; want %+v", got["t1"], err, first)
	}

	updated := first
	updated.UserBucket, updated.FollowUpID = BucketIgnorable, "fu-1"
	if err := store.Save(ctx, scope, []State{updated}); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	got, _ = store.Load(ctx, scope)
	if len(got) != 1 || got["t1"] != updated {
		t.Fatalf("after update = %+v", got)
	}

	other, _ := store.Load(ctx, Scope{WorkspaceID: "ws", AccountID: "another"})
	if len(other) != 0 {
		t.Fatalf("another account read this account's state: %+v", other)
	}
}

func TestSQLiteStoreForgetsQuietThreads(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	scope := Scope{WorkspaceID: "ws", AccountID: "acct"}
	old := State{ThreadID: "old", LastMessageAt: base.Add(-stateRetention - time.Hour), Bucket: BucketFYI, UpdatedAt: base}
	recent := State{ThreadID: "recent", LastMessageAt: base.Add(-time.Hour), Bucket: BucketFYI, UpdatedAt: base}
	if err := store.Save(ctx, scope, []State{old, recent}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _ := store.Load(ctx, scope)
	if _, kept := got["old"]; kept || len(got) != 1 {
		t.Fatalf("states = %+v; a thread quiet past retention was kept", got)
	}
}

// The service runs against the real store end to end.
func TestServiceWithTheSQLiteStore(t *testing.T) {
	svc := NewService(inbox(), newSQLiteStore(t), nil)
	if _, err := svc.List(context.Background(), "ws", account); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := svc.SetBucket(context.Background(), "ws", account, "t-sam", BucketIgnorable); err != nil {
		t.Fatalf("SetBucket: %v", err)
	}
	list, err := svc.List(context.Background(), "ws", account)
	if err != nil || len(list.NeedsYou) != 0 {
		t.Fatalf("list = %+v, %v; want nothing needing the user", list, err)
	}
	for _, item := range list.Ignorable {
		if item.ThreadID == "t-sam" && item.ByYou {
			return
		}
	}
	t.Fatalf("ignorable = %+v; want the user's own call on t-sam", list.Ignorable)
}
