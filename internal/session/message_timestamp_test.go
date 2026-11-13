package session

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

func TestMessageTimestampsKeepOrdinaryAndAttributedTurnsInOrder(t *testing.T) {
	for _, zone := range []*time.Location{
		time.UTC,
		time.FixedZone("EST", -5*60*60),
		time.FixedZone("JST", 9*60*60),
		time.FixedZone("NPT", 5*60*60+45*60),
	} {
		t.Run(zone.String(), func(t *testing.T) {
			db, _ := setupTestDB(t)
			store := NewHybridStoreWithDB(db, 10)
			chat := &Session{AgentName: "Atlas"}
			if err := store.CreateSession(t.Context(), chat); err != nil {
				t.Fatal(err)
			}
			// Warm the cache as well as exercising the durable read below.
			if _, err := store.GetSession(t.Context(), chat.ID); err != nil {
				t.Fatal(err)
			}
			beforeAt := time.Now().UTC().Add(-time.Hour)
			before := &Message{Role: RoleUser, Content: "before", CreatedAt: beforeAt.In(zone)}
			if err := store.AddMessage(t.Context(), chat.ID, before); err != nil {
				t.Fatal(err)
			}
			turn, err := store.(AssistantTurnStore).AppendAttributedTurn(t.Context(), chat.ID,
				assistantcontext.SaveOwner{AgentName: "Atlas"}, nil, "", "question", "answer", nil)
			if err != nil || len(turn) != 2 {
				t.Fatalf("attributed turn: %+v, %v", turn, err)
			}
			afterAt := turn[1].CreatedAt.Add(time.Second)
			after := &Message{Role: RoleUser, Content: "after", CreatedAt: afterAt.In(zone)}
			if err := store.AddMessage(t.Context(), chat.ID, after); err != nil {
				t.Fatal(err)
			}
			want := []Message{*before, turn[0], turn[1], *after}
			for _, reader := range []SessionStore{store, NewHybridStoreWithDB(db, 10)} {
				messages, err := reader.GetMessages(t.Context(), chat.ID)
				if err != nil || len(messages) != len(want) {
					t.Fatalf("read: %+v, %v", messages, err)
				}
				for n, message := range messages {
					if message.ID != want[n].ID || !message.CreatedAt.Equal(want[n].CreatedAt) {
						t.Fatalf("message %d: got %s at %s, want %s at %s", n, message.Content, message.CreatedAt, want[n].Content, want[n].CreatedAt)
					}
				}
			}
			if !before.CreatedAt.Equal(beforeAt) || !after.CreatedAt.Equal(afterAt) {
				t.Fatal("normalizing the zone changed the instant")
			}
			if preview := NewSQLiteStore(db).getSessionPreview(t.Context(), chat.ID); preview != "before" {
				t.Fatalf("preview used a later message: %q", preview)
			}
		})
	}
}

func TestSQLiteMessageTimestampNormalizesTheSuppliedInstant(t *testing.T) {
	db, _ := setupTestDB(t)
	store := NewSQLiteStore(db)
	at := time.Date(2026, 11, 13, 1, 2, 3, 456789123, time.FixedZone("EST", -5*60*60))
	chat := &Session{ID: "timestamp", AgentName: "Atlas", CreatedAt: at, UpdatedAt: at}
	if err := store.CreateSession(t.Context(), chat); err != nil {
		t.Fatal(err)
	}
	message := &Message{ID: "message", Role: RoleUser, Content: "supplied instant", CreatedAt: at}
	if err := store.AddMessage(t.Context(), chat.ID, message); err != nil {
		t.Fatal(err)
	}
	var saved time.Time
	if err := db.QueryRowContext(t.Context(), `SELECT created_at FROM messages WHERE id=?`, message.ID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	_, offset := saved.Zone()
	_, cachedOffset := message.CreatedAt.Zone()
	if offset != 0 || cachedOffset != 0 || !saved.Equal(at) || !message.CreatedAt.Equal(at) {
		t.Fatalf("timestamp not normalized without losing precision: saved=%s message=%s want=%s", saved, message.CreatedAt, at)
	}
}
