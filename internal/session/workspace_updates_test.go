package session

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func welcomeFixture(t *testing.T) (*hybridStore, WelcomeScope, LeadConversationInput) {
	t.Helper()
	store, cleanup := setupHybridStore(t, 10)
	t.Cleanup(cleanup)
	h := store.(*hybridStore)
	scope := WelcomeScope{WorkspaceID: "ws", OwnerUserID: "local", AgentInstanceID: "mira-local", AgentName: "Mira"}
	if err := h.CreateWorkspace(t.Context(), &Workspace{ID: scope.WorkspaceID, Name: "Desk", OwnerUserID: scope.OwnerUserID, Status: WorkspaceStatusActive}); err != nil {
		t.Fatal(err)
	}
	return h, scope, LeadConversationInput{WorkspaceID: scope.WorkspaceID, OwnerUserID: scope.OwnerUserID, AgentInstanceID: scope.AgentInstanceID, AgentName: scope.AgentName, Introduction: "Hello."}
}

func welcomeAck(delivery WelcomeDelivery, index int) WelcomeAcknowledgement {
	m := delivery.Messages[index]
	return WelcomeAcknowledgement{SessionID: delivery.SessionID, MessageID: m.MessageID, Revision: m.Revision}
}

func TestWelcomeUpdatesExactSeenIsMonotonicAcrossConcurrentTabs(t *testing.T) {
	h, scope, input := welcomeFixture(t)
	if _, err := h.EnsureLeadConversation(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	first, err := h.ReadWelcome(t.Context(), scope)
	if err != nil || first.Unread != 1 || len(first.Messages) != 1 || first.Messages[0].Revision != 1 {
		t.Fatalf("first projection: %+v %v", first, err)
	}
	ack := welcomeAck(first, 0)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := h.AcknowledgeWelcome(t.Context(), scope, ack); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	seen, err := h.ReadWelcome(t.Context(), scope)
	if err != nil || seen.Unread != 0 || seen.Messages[0].SeenAt == nil {
		t.Fatalf("seen projection: %+v %v", seen, err)
	}
	input.Assessment = "A saved assessment."
	wg.Go(func() {
		if _, err := h.EnsureLeadConversation(t.Context(), input); err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if err := h.AcknowledgeWelcome(t.Context(), scope, ack); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	fresh, err := h.ReadWelcome(t.Context(), scope)
	if err != nil || fresh.Unread != 1 || len(fresh.Messages) != 2 || fresh.Messages[1].Revision != 2 || fresh.Messages[1].SeenAt != nil || !fresh.Messages[0].SeenAt.Equal(*seen.Messages[0].SeenAt) {
		t.Fatalf("old ack cleared new delivery or changed original seen time: %+v %v", fresh, err)
	}
	if err := h.AcknowledgeWelcome(t.Context(), scope, welcomeAck(fresh, 1)); err != nil {
		t.Fatal(err)
	}
	final, err := h.ReadWelcome(t.Context(), scope)
	if err != nil || final.Unread != 0 {
		t.Fatalf("final projection: %+v %v", final, err)
	}
}

func TestWelcomeUpdatesRejectStaleOrForgedAcknowledgements(t *testing.T) {
	for _, mode := range []string{"owner", "instance", "name", "version", "session", "message", "revision", "future", "deleted", "moved", "message-deleted", "trashed", "missing", "ordinary-reply"} {
		t.Run(mode, func(t *testing.T) {
			h, scope, input := welcomeFixture(t)
			if _, err := h.EnsureLeadConversation(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			original := scope
			delivery, err := h.ReadWelcome(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			ack := welcomeAck(delivery, 0)
			switch mode {
			case "owner":
				scope.OwnerUserID = "other"
			case "instance":
				scope.AgentInstanceID = "namesake"
			case "name":
				scope.AgentName = "Other"
			case "version":
				scope.WorkspaceVersion++
			case "session":
				ack.SessionID = "other"
			case "message":
				ack.MessageID = "other"
			case "revision":
				ack.Revision = 2
			case "future":
				ack.Revision = 999
			case "deleted":
				if err := h.DeleteSession(t.Context(), delivery.SessionID); err != nil {
					t.Fatal(err)
				}
			case "moved":
				if _, err := h.DB().ExecContext(t.Context(), `UPDATE sessions SET workspace_id=NULL WHERE id=?`, delivery.SessionID); err != nil {
					t.Fatal(err)
				}
			case "message-deleted":
				if _, err := h.DB().ExecContext(t.Context(), `DELETE FROM messages WHERE id=?`, ack.MessageID); err != nil {
					t.Fatal(err)
				}
			case "trashed", "missing":
				if _, err := h.DB().ExecContext(t.Context(), `UPDATE workspaces SET status=? WHERE id=?`, mode, scope.WorkspaceID); err != nil {
					t.Fatal(err)
				}
			case "ordinary-reply":
				message := &Message{Role: RoleAssistant, Content: "Ordinary answer", CreatedAt: time.Now()}
				if err := h.AddMessage(t.Context(), delivery.SessionID, message); err != nil {
					t.Fatal(err)
				}
				ack.MessageID = message.ID
			}
			if err := h.AcknowledgeWelcome(t.Context(), scope, ack); err == nil {
				t.Fatal("invalid acknowledgement accepted")
			}
			var seen int
			if err := h.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_welcome_messages WHERE workspace_id=? AND seen_at IS NOT NULL`, original.WorkspaceID).Scan(&seen); err != nil || seen != 0 {
				t.Fatalf("invalid ack changed read state: %d %v", seen, err)
			}
		})
	}
}

func TestWelcomeEnrollmentIsExplicitBoundedAndNeverAdopted(t *testing.T) {
	h, scope, _ := welcomeFixture(t)
	if rows, err := h.PendingWelcomes(t.Context(), 0, 100); err != nil || len(rows) != 0 {
		t.Fatalf("ordinary store creation enrolled legacy workspace: %v %v", rows, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := h.EnrollWelcome(t.Context(), scope); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for i := range 4 {
		next := scope
		next.WorkspaceID = fmt.Sprintf("later-%d", 4-i) // UUID/name order is not delivery order
		if err := h.CreateWorkspace(t.Context(), &Workspace{ID: next.WorkspaceID, Name: "Desk", OwnerUserID: "local"}); err != nil {
			t.Fatal(err)
		}
		if err := h.EnrollWelcome(t.Context(), next); err != nil {
			t.Fatal(err)
		}
	}
	page, err := h.PendingWelcomes(t.Context(), 0, 2)
	if err != nil || len(page) != 2 || page[0].WorkspaceID != "ws" || page[1].WorkspaceID != "later-4" {
		t.Fatalf("unstable enrollment order: %+v %v", page, err)
	}
	next, err := h.PendingWelcomes(t.Context(), page[1].ID, 2)
	if err != nil || len(next) != 2 || next[0].WorkspaceID != "later-3" {
		t.Fatalf("keyset paging: %+v %v", next, err)
	}
	for _, limit := range []int{0, -1, 101} {
		if _, err := h.PendingWelcomes(t.Context(), 0, limit); !errors.Is(err, ErrInvalidID) {
			t.Fatal("unbounded enrollment query accepted")
		}
	}
	replacement := scope
	replacement.AgentInstanceID = "replacement"
	if err := h.EnrollWelcome(t.Context(), replacement); !errors.Is(err, ErrWelcomeUnavailable) {
		t.Fatalf("enrollment transferred to namesake: %v", err)
	}
	if err := h.SettleWelcome(t.Context(), page[0]); err != nil {
		t.Fatal(err)
	}
	if err := h.EnrollWelcome(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	pending, err := h.PendingWelcomes(t.Context(), 0, 100)
	if err != nil || len(pending) != 4 {
		t.Fatalf("settled enrollment rearmed: %+v %v", pending, err)
	}
	if err := h.DeleteWorkspace(t.Context(), next[0].WorkspaceID); err != nil {
		t.Fatal(err)
	}
	pending, err = h.PendingWelcomes(t.Context(), 0, 100)
	if err != nil || len(pending) != 3 {
		t.Fatalf("deleted workspace enrollment survived: %+v %v", pending, err)
	}
}

func TestWelcomeMessageAndUpdateRollBackTogether(t *testing.T) {
	h, scope, input := welcomeFixture(t)
	if _, err := h.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_welcome_update BEFORE INSERT ON workspace_welcome_messages BEGIN SELECT RAISE(ABORT,'injected update failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.EnsureLeadConversation(t.Context(), input); err == nil {
		t.Fatal("injected failure ignored")
	}
	var sessions, messages, receipts int
	if err := h.DB().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM sessions), (SELECT COUNT(*) FROM messages), (SELECT COUNT(*) FROM workspace_lead_conversations)`).Scan(&sessions, &messages, &receipts); err != nil || sessions != 0 || messages != 0 || receipts != 0 {
		t.Fatalf("phantom/undiscoverable delivery: %d %d %d %v", sessions, messages, receipts, err)
	}
	if _, err := h.DB().ExecContext(t.Context(), `DROP TRIGGER fail_welcome_update`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := h.EnsureLeadConversation(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	delivery, err := h.ReadWelcome(t.Context(), scope)
	if err != nil || delivery.Unread != 1 || len(delivery.Messages) != 1 {
		t.Fatalf("retry duplicated delivery: %+v %v", delivery, err)
	}
}
