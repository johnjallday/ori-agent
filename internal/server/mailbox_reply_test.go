package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeReader returns a canned thread for ComposeReply; SearchThreads is unused.
type fakeReader struct{}

func (fakeReader) SearchThreads(ctx context.Context, a mailbox.Account, q mailbox.Query) (mailbox.ThreadPage, error) {
	return mailbox.ThreadPage{}, nil
}
func (fakeReader) GetThread(ctx context.Context, a mailbox.Account, id string) (mailbox.Thread, error) {
	return mailbox.Thread{
		ID: id, Subject: "Need your review",
		Participants: []mailbox.Participant{{Address: "dana@partner.com"}},
		Messages: []mailbox.Message{{
			ID: "m1", From: mailbox.Participant{Address: "dana@partner.com"}, Subject: "Need your review",
		}},
	}, nil
}

// recordingSender counts sends.
type recordingSender struct{ calls int }

func (r *recordingSender) SendReply(ctx context.Context, a mailbox.Account, p mailbox.ReplyPayload) (mailbox.SendResult, error) {
	r.calls++
	return mailbox.SendResult{ProviderMessageID: "sent-1", ThreadID: p.SourceThreadID}, nil
}

func newReplyFixture(t *testing.T) (*replyService, *recordingSender, string) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	profiles := userprofile.NewSQLiteStore(db)
	sessionStore := session.NewSQLiteStore(db)
	hq := personalhq.NewService(profiles, sessionStore)
	userID := userprofile.LocalUserID
	_ = profiles.Upsert(ctx, &userprofile.UserProfile{ID: userID})
	_ = sessionStore.CreateWorkspace(ctx, &session.Workspace{ID: "hq-1", Name: "HQ", Kind: session.WorkspaceKindWorkspace, OwnerUserID: userID, Status: session.WorkspaceStatusActive})
	if _, err := hq.Designate(ctx, userID, "hq-1"); err != nil {
		t.Fatalf("designate: %v", err)
	}

	wstore := workspace.NewInMemoryStore()
	_ = wstore.Save(&workspace.Workspace{
		ID: "hq-1",
		MCPBindings: []workspace.MCPBinding{{
			ID: "b1", ServerName: "gmail", Enabled: true,
			Config: map[string]any{"account_id": "acct-1", "allowed_actions": []any{"read", "search"}},
		}},
	})
	accounts := fakeAccounts{acc: &vault.EmailAccount{ID: "acct-1", Provider: vault.EmailProviderGmail, EmailAddress: "me@example.com"}}
	sender := &recordingSender{}
	broker := mailbox.NewBroker(sender, &sendAuthorizer{hq: hq, workspaces: wstore}, logAuditSink{})
	return newReplyService(hq, wstore, accounts, fakeReader{}, broker), sender, userID
}

func TestReplyServiceDraftThenConfirmSends(t *testing.T) {
	svc, sender, userID := newReplyFixture(t)
	ctx := context.Background()

	p, err := svc.DraftReply(ctx, userID, "t1", "Looks good to me.")
	if err != nil {
		t.Fatalf("DraftReply: %v", err)
	}
	if p.Status != mailbox.ProposalDraft {
		t.Fatalf("expected a draft, got %v", p.Status)
	}
	if p.Payload.Subject != "Re: Need your review" || len(p.Payload.To) != 1 || p.Payload.To[0] != "dana@partner.com" {
		t.Fatalf("compose produced wrong payload: %+v", p.Payload)
	}
	// A draft must NOT have sent anything.
	if sender.calls != 0 {
		t.Fatal("drafting must never send")
	}

	final, err := svc.ConfirmSend(ctx, userID, p.ID, p.Hash())
	if err != nil {
		t.Fatalf("ConfirmSend: %v", err)
	}
	if final.Status != mailbox.ProposalSent || sender.calls != 1 {
		t.Fatalf("expected exactly one send, status=%v calls=%d", final.Status, sender.calls)
	}
}

func TestReplyServiceEditThenStaleConfirmRejected(t *testing.T) {
	svc, sender, userID := newReplyFixture(t)
	ctx := context.Background()
	p, err := svc.DraftReply(ctx, userID, "t1", "first")
	if err != nil {
		t.Fatalf("DraftReply: %v", err)
	}
	staleHash := p.Hash()
	if _, err := svc.EditProposal(ctx, userID, p.ID, nil, p.Payload.Subject, "edited body"); err != nil {
		t.Fatalf("EditProposal: %v", err)
	}
	if _, err := svc.ConfirmSend(ctx, userID, p.ID, staleHash); !errors.Is(err, mailbox.ErrPayloadChanged) {
		t.Fatalf("stale confirm must be rejected, got %v", err)
	}
	if sender.calls != 0 {
		t.Fatal("a rejected confirm must not send")
	}
}
