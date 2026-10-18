package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

func TestAssistantArrangementCleanup_DoesNotWaitBehindAnotherStoreOperation(t *testing.T) {
	_, store, chat, owner, _ := arrangementStoreFixture(t)
	h := store.(*hybridStore)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	h.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- h.DiscardUnansweredArrangementConversation(ctx, chat.ID, owner) }()
	select {
	case err := <-done:
		h.mu.Unlock()
		if err == nil {
			t.Fatal("cleanup competed with an active store operation")
		}
	case <-ctx.Done():
		h.mu.Unlock()
		<-done
		t.Fatal("cleanup blocked on the store lock")
	}
	if _, err := store.GetSession(t.Context(), chat.ID); err != nil {
		t.Fatal("busy cleanup deleted the conversation", err)
	}
}

func TestAssistantArrangementCleanup_OnlyUnchangedUnansweredConversation(t *testing.T) {
	for _, mode := range []string{"empty", "cancelled", "detached_cancel", "missing_pin", "foreign", "edited", "answered", "committed_arrangement"} {
		t.Run(mode, func(t *testing.T) {
			_, store, chat, owner, record := arrangementStoreFixture(t)
			ctx := t.Context()
			switch mode {
			case "cancelled", "detached_cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				if mode == "detached_cancel" {
					ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
					defer cancel()
				}
			case "missing_pin":
				owner.ExpectedConversationRevision = ""
			case "foreign":
				owner.UserID = "foreign"
			case "edited":
				chat.Title = "Another tab edited this"
				if err := store.UpdateSession(ctx, chat); err != nil {
					t.Fatal(err)
				}
			case "answered":
				if err := store.AddMessage(ctx, chat.ID, &Message{Role: RoleUser, Content: "Another tab's message"}); err != nil {
					t.Fatal(err)
				}
			case "committed_arrangement":
				event := folderEventFixture()
				if _, err := store.(AssistantArrangementStore).AppendArrangementTurn(ctx, chat.ID, owner, &event, "", "Arrange", "Saved", nil, record); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "answered" || mode == "committed_arrangement" {
				// Even a current pin cannot turn a populated session into cleanup.
				fresh, err := store.GetSession(t.Context(), chat.ID)
				if err != nil {
					t.Fatal(err)
				}
				owner.ExpectedConversationRevision = assistantcontext.ConversationRevision(fresh.UpdatedAt, fresh.MessageCount, fresh.ContextEpoch)
			}
			err := store.(AssistantArrangementCleanup).DiscardUnansweredArrangementConversation(ctx, chat.ID, owner)
			if mode == "empty" || mode == "detached_cancel" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.GetSession(t.Context(), chat.ID); !errors.Is(err, ErrSessionNotFound) {
					t.Fatal("empty session retained or cached", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe cleanup succeeded")
			}
			if _, err := store.GetSession(t.Context(), chat.ID); err != nil {
				t.Fatal("cleanup removed a protected session", err)
			}
			if mode == "committed_arrangement" {
				if _, err := store.(AssistantArrangementStore).ReadAssistantArrangement(t.Context(), chat.ID, owner, record.ID); err != nil {
					t.Fatal("cleanup removed a saved draft", err)
				}
			}
		})
	}
}
