package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/emailsetup"
	"github.com/johnjallday/ori-agent/internal/emailtriage"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type cannedInbox struct{ threads []mailbox.Thread }

func (c cannedInbox) SearchThreads(context.Context, mailbox.Account, mailbox.Query) (mailbox.ThreadPage, error) {
	return mailbox.ThreadPage{Threads: c.threads}, nil
}

func (c cannedInbox) GetThread(context.Context, mailbox.Account, string) (mailbox.Thread, error) {
	return mailbox.Thread{}, mailbox.ErrNotFound
}

func cannedThreads(owner string) []mailbox.Thread {
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	return []mailbox.Thread{
		{ID: "t-sam", Subject: "Offsite", LastMessageAt: at, Messages: []mailbox.Message{{
			From: mailbox.Participant{Name: "Sam Lee", Address: "sam@example.com"},
			To:   []mailbox.Participant{{Address: owner}}, SentAt: at,
		}}},
		{ID: "t-news", Subject: "Sale", LastMessageAt: at.Add(-time.Minute), Messages: []mailbox.Message{{
			From: mailbox.Participant{Address: "news@shop.example"}, Bulk: true, SentAt: at.Add(-time.Minute),
		}}},
	}
}

// linkedEmailOps builds a server with an Email Ops workspace whose mailbox is
// a password account.
func linkedEmailOps(t *testing.T) (*ServerBuilder, string, *vault.EmailAccount) {
	t.Helper()
	b := newEmailSetupTestBuilder(t)
	ctx := context.Background()
	vaultID, err := b.vaultKeyring.EnsureVault(ctx, "Email")
	if err != nil {
		t.Fatalf("EnsureVault: %v", err)
	}
	account, err := b.vaultStore.CreateEmailAccount(ctx, vault.EmailAccountInput{
		VaultID: vaultID, Source: emailsetup.AccountSource,
		Provider: vault.EmailProviderIMAPSMTP, AuthType: vault.EmailAuthTypeAppPassword,
		EmailAddress: "me@fastmail.example", IMAPHost: "imap.fastmail.com", SMTPHost: "smtp.fastmail.com",
		Credentials: vault.EmailAccountCredentials{Password: "app-password"},
	})
	if err != nil {
		t.Fatalf("CreateEmailAccount: %v", err)
	}
	home := emailSetupHome{b: b}
	ws, err := home.EnsureEmailOps(ctx, userprofile.LocalUserID)
	if err != nil {
		t.Fatalf("EnsureEmailOps: %v", err)
	}
	if err := home.LinkMailbox(ctx, userprofile.LocalUserID, ws.ID, account.ID); err != nil {
		t.Fatalf("LinkMailbox: %v", err)
	}
	return b, ws.ID, account
}

func TestNeedsYouListMarksAndTracks(t *testing.T) {
	b, workspaceID, account := linkedEmailOps(t)
	ctx := context.Background()
	if b.emailTriage == nil {
		t.Fatal("the list was not wired")
	}
	triage := emailtriage.NewService(cannedInbox{threads: cannedThreads(account.EmailAddress)}, emailtriage.NewSQLiteStore(b.sessionStore.DB()), nil)
	svc := needsYouService{b: b, triage: triage}

	list, err := svc.NeedsYou(ctx, userprofile.LocalUserID, workspaceID)
	if err != nil {
		t.Fatalf("NeedsYou: %v", err)
	}
	if len(list.NeedsYou) != 1 || list.NeedsYou[0].Subject != "Offsite" || len(list.Ignorable) != 1 {
		t.Fatalf("list = %+v", list)
	}

	if err := svc.TrackNeedsYou(ctx, userprofile.LocalUserID, workspaceID, "t-sam"); err != nil {
		t.Fatalf("TrackNeedsYou: %v", err)
	}
	if err := svc.TrackNeedsYou(ctx, userprofile.LocalUserID, workspaceID, "t-sam"); err != nil {
		t.Fatalf("second TrackNeedsYou: %v", err)
	}
	items, err := b.followUpService.List(ctx, followup.Filter{UserID: userprofile.LocalUserID, WorkspaceID: workspaceID, OpenOnly: true})
	if err != nil {
		t.Fatalf("List follow-ups: %v", err)
	}
	var tracked []*followup.FollowUp
	for _, item := range items {
		if item.Source.Type == "email_thread" {
			tracked = append(tracked, item)
		}
	}
	if len(tracked) != 1 || tracked[0].Source.ID != "t-sam" || tracked[0].Title != "Offsite" || tracked[0].Counterparty != "Sam Lee" {
		t.Fatalf("follow-ups = %+v; want one, from Sam's thread", tracked)
	}

	if err := svc.MarkNeedsYou(ctx, userprofile.LocalUserID, workspaceID, "t-sam", emailtriage.BucketIgnorable); err != nil {
		t.Fatalf("MarkNeedsYou: %v", err)
	}
	after, _ := svc.NeedsYou(ctx, userprofile.LocalUserID, workspaceID)
	if len(after.NeedsYou) != 0 || len(after.Ignorable) != 2 {
		t.Fatalf("after Not important: %+v", after)
	}

	var coded *personalhqhttp.NeedsYouError
	if err := svc.MarkNeedsYou(ctx, userprofile.LocalUserID, workspaceID, "t-sam", emailtriage.BucketHandled); !errors.As(err, &coded) || coded.Code != "invalid_bucket" {
		t.Fatalf("marking handled by hand = %v", err)
	}
	if err := svc.MarkNeedsYou(ctx, userprofile.LocalUserID, workspaceID, "t-unknown", emailtriage.BucketIgnorable); !errors.Is(err, emailtriage.ErrUnknownThread) {
		t.Fatalf("unknown thread = %v", err)
	}
}

func TestNeedsYouNamesWhatIsMissing(t *testing.T) {
	b, workspaceID, _ := linkedEmailOps(t)
	ctx := context.Background()
	svc := needsYouService{b: b, triage: b.emailTriage}

	// A workspace with nothing linked has no list.
	other, err := b.sessionHandler.CreateFromTemplate(ctx, "Notes", "")
	if err != nil {
		t.Fatalf("create a plain workspace: %v", err)
	}
	if _, err := svc.NeedsYou(ctx, userprofile.LocalUserID, other); !errors.Is(err, personalhqhttp.ErrNoMailboxLinked) {
		t.Fatalf("unlinked workspace = %v, want ErrNoMailboxLinked", err)
	}

	// A locked vault is named as such, not as a broken account.
	vaultID := b.vaultKeyring.RememberedVaultID()
	if err := b.vaultStore.Lock(ctx, vaultID); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	var coded *personalhqhttp.NeedsYouError
	if _, err := svc.NeedsYou(ctx, userprofile.LocalUserID, workspaceID); !errors.As(err, &coded) || coded.Code != "vault_locked" {
		t.Fatalf("locked vault = %v, want vault_locked", err)
	}
}

func TestTriagedBriefThreadsLeaveOutIgnorableMail(t *testing.T) {
	at := time.Now().UTC()
	list := emailtriage.List{
		NeedsYou:  []emailtriage.Item{{ThreadID: "a", Subject: "Offsite", From: "Sam", LastMessageAt: at}},
		FYI:       []emailtriage.Item{{ThreadID: "b", Subject: "Receipt", From: "Shop", LastMessageAt: at, Unread: true}},
		Ignorable: []emailtriage.Item{{ThreadID: "c", Subject: "Sale", From: "News", LastMessageAt: at, Unread: true}},
	}
	got := triagedBriefThreads(list, "ws", "acct")
	if len(got) != 2 || got[0].Ref.EntityID != "a" || !got[0].WaitingOnUser || got[1].Ref.EntityID != "b" || got[1].WaitingOnUser || !got[1].Unread {
		t.Fatalf("brief threads = %+v", got)
	}
}
