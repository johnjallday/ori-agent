package mailbox

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeSender struct {
	mu    sync.Mutex
	calls int
	err   error
	last  ReplyPayload
}

func (f *fakeSender) SendReply(ctx context.Context, a Account, p ReplyPayload) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = p
	if f.err != nil {
		return SendResult{}, f.err
	}
	return SendResult{ProviderMessageID: "m1", ThreadID: p.SourceThreadID, SentAt: time.Now()}, nil
}
func (f *fakeSender) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type fakeAuthz struct {
	mu  sync.Mutex
	err error
}

func (f *fakeAuthz) AuthorizeSend(ctx context.Context, userID, workspaceID string, p ReplyPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
func (f *fakeAuthz) set(err error) { f.mu.Lock(); defer f.mu.Unlock(); f.err = err }

type fakeAudit struct {
	mu     sync.Mutex
	events []SendAuditEvent
}

func (f *fakeAudit) RecordSendEvent(e SendAuditEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}
func (f *fakeAudit) kinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.events {
		out = append(out, e.Event)
	}
	return out
}

func samplePayload() ReplyPayload {
	return ReplyPayload{AccountID: "acct-1", SourceThreadID: "t1", To: []string{"dana@x.com"}, Subject: "Re: hi", Body: "Sounds good."}
}

func newBrokerFixture() (*Broker, *fakeSender, *fakeAuthz, *fakeAudit) {
	s, a, au := &fakeSender{}, &fakeAuthz{}, &fakeAudit{}
	return NewBroker(s, a, au), s, a, au
}

func TestBrokerHappyPathSendsOnce(t *testing.T) {
	b, sender, _, audit := newBrokerFixture()
	p, err := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	final, err := b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash())
	if err != nil {
		t.Fatalf("ConfirmSend: %v", err)
	}
	if final.Status != ProposalSent || final.SentMessageID != "m1" {
		t.Fatalf("expected Sent with message id, got %+v", final)
	}
	if sender.count() != 1 {
		t.Fatalf("expected exactly one send, got %d", sender.count())
	}
	kinds := audit.kinds()
	for _, want := range []string{"proposed", "send_attempted", "sent"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("audit missing %q event; got %v", want, kinds)
		}
	}
}

func TestBrokerRejectsPayloadTampering(t *testing.T) {
	b, sender, _, _ := newBrokerFixture()
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	if _, err := b.ConfirmSend(context.Background(), "u1", p.ID, "not-the-real-hash"); !errors.Is(err, ErrPayloadChanged) {
		t.Fatalf("expected ErrPayloadChanged, got %v", err)
	}
	if sender.count() != 0 {
		t.Fatal("a tampered payload must not send")
	}
}

func TestBrokerEditInvalidatesPriorHash(t *testing.T) {
	b, sender, _, _ := newBrokerFixture()
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	oldHash := p.Hash()
	edited := samplePayload()
	edited.Body = "Actually, let's reschedule."
	if _, err := b.EditProposal(context.Background(), "u1", p.ID, edited); err != nil {
		t.Fatalf("EditProposal: %v", err)
	}
	// Confirming with the pre-edit hash must be rejected.
	if _, err := b.ConfirmSend(context.Background(), "u1", p.ID, oldHash); !errors.Is(err, ErrPayloadChanged) {
		t.Fatalf("edit must invalidate the old hash, got %v", err)
	}
	if sender.count() != 0 {
		t.Fatal("stale confirmation must not send")
	}
	// Confirming with the new hash works.
	if _, err := b.ConfirmSend(context.Background(), "u1", p.ID, edited.Hash()); err != nil {
		t.Fatalf("send with current hash: %v", err)
	}
}

func TestBrokerSingleUseRejectsReplay(t *testing.T) {
	b, sender, _, _ := newBrokerFixture()
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	if _, err := b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash()); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if _, err := b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash()); !errors.Is(err, ErrProposalNotDraft) {
		t.Fatalf("replay must be rejected, got %v", err)
	}
	if sender.count() != 1 {
		t.Fatalf("replay must not re-send; sends=%d", sender.count())
	}
}

func TestBrokerConcurrentSendSendsOnce(t *testing.T) {
	b, sender, _, _ := newBrokerFixture()
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash()) })
	}
	wg.Wait()
	if sender.count() != 1 {
		t.Fatalf("concurrent confirmations must send exactly once, got %d", sender.count())
	}
}

func TestBrokerExpiry(t *testing.T) {
	b, sender, _, _ := newBrokerFixture()
	now := time.Unix(1000, 0)
	b.now = func() time.Time { return now }
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	now = now.Add(DefaultProposalTTL + time.Second)
	if _, err := b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash()); !errors.Is(err, ErrProposalExpired) {
		t.Fatalf("expected ErrProposalExpired, got %v", err)
	}
	if sender.count() != 0 {
		t.Fatal("an expired proposal must not send")
	}
}

func TestBrokerUnauthorizedCreateAndSend(t *testing.T) {
	b, sender, authz, _ := newBrokerFixture()
	authz.set(errors.New("denied"))
	if _, err := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload()); !errors.Is(err, ErrSendUnauthorized) {
		t.Fatalf("unauthorized create must fail, got %v", err)
	}

	// Authorize creation, then revoke before send.
	authz.set(nil)
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	authz.set(errors.New("revoked"))
	if _, err := b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash()); !errors.Is(err, ErrSendUnauthorized) {
		t.Fatalf("revoked send must fail, got %v", err)
	}
	if sender.count() != 0 {
		t.Fatal("revoked authorization must not send")
	}
}

func TestBrokerWrongOwnerCannotSend(t *testing.T) {
	b, _, _, _ := newBrokerFixture()
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	if _, err := b.ConfirmSend(context.Background(), "someone-else", p.ID, p.Hash()); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("another user must not see/send the proposal, got %v", err)
	}
}

func TestBrokerSendFailureIsRetryable(t *testing.T) {
	b, sender, _, audit := newBrokerFixture()
	sender.err = ErrRateLimited
	p, _ := b.CreateProposal(context.Background(), "u1", "hq-1", samplePayload())
	final, err := b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash())
	if err == nil || final.Status != ProposalFailed {
		t.Fatalf("expected a retryable failure, got status=%v err=%v", final.Status, err)
	}
	// Retry after the provider recovers.
	sender.err = nil
	final, err = b.ConfirmSend(context.Background(), "u1", p.ID, p.Hash())
	if err != nil || final.Status != ProposalSent {
		t.Fatalf("retry should succeed, got status=%v err=%v", final.Status, err)
	}
	// Audit records the failed then the sent outcome; never message content.
	if !slices.Contains(audit.kinds(), "failed") || !slices.Contains(audit.kinds(), "sent") {
		t.Fatalf("audit should record failed then sent, got %v", audit.kinds())
	}
	for _, e := range audit.events {
		if e.Detail == "Sounds good." || e.PayloadHash == "" {
			t.Fatalf("audit must be metadata-only (hash present, no body): %+v", e)
		}
	}
}
