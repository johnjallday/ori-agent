package mailbox

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Broker errors. They are intentionally coarse and content-free so an HTTP layer
// can map them to status codes without leaking recipients/subject/body.
var (
	ErrProposalNotFound   = errors.New("mailbox: reply proposal not found")
	ErrProposalNotDraft   = errors.New("mailbox: reply proposal cannot be sent in its current state")
	ErrProposalExpired    = errors.New("mailbox: reply proposal has expired")
	ErrPayloadChanged     = errors.New("mailbox: reply content changed since it was reviewed; re-confirm")
	ErrSendUnauthorized   = errors.New("mailbox: sending is not authorized")
	ErrBrokerUnconfigured = errors.New("mailbox: send broker is not configured")
)

// DefaultProposalTTL bounds how long a draft stays sendable before it must be
// re-created (contract §6 retention: short-lived pending confirmations).
const DefaultProposalTTL = 15 * time.Minute

// ProposalStatus is a reply proposal's lifecycle state.
type ProposalStatus string

const (
	ProposalDraft     ProposalStatus = "draft"
	ProposalSending   ProposalStatus = "sending" // in-flight; rejects concurrent sends
	ProposalSent      ProposalStatus = "sent"
	ProposalFailed    ProposalStatus = "failed" // retryable
	ProposalCancelled ProposalStatus = "cancelled"
	ProposalExpired   ProposalStatus = "expired"
)

// ReplyProposal is a LOCAL draft reply. It never touches Gmail until a send is
// confirmed (contract §4). It carries no secrets — just the content the user
// composed and reviewed.
type ReplyProposal struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	WorkspaceID   string         `json:"workspace_id"`
	Payload       ReplyPayload   `json:"payload"`
	Status        ProposalStatus `json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	ExpiresAt     time.Time      `json:"expires_at"`
	SentMessageID string         `json:"sent_message_id,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
}

// Hash returns the current payload hash — what a send must confirm against.
func (p *ReplyProposal) Hash() string { return p.Payload.Hash() }

func (p *ReplyProposal) clone() *ReplyProposal {
	cp := *p
	cp.Payload.To = append([]string(nil), p.Payload.To...)
	return &cp
}

// SendAuthorizer re-evaluates the full most-restrictive send policy (OAuth send
// scope, account grant, workspace binding, agent access, allowed action, source
// thread ownership) at both proposal creation and send consumption (task 5.4).
type SendAuthorizer interface {
	AuthorizeSend(ctx context.Context, userID, workspaceID string, payload ReplyPayload) error
}

// SendAuditEvent is a metadata-only record of a send-lifecycle event (task 5.10):
// never recipients-in-full, subject, or body.
type SendAuditEvent struct {
	Event          string    `json:"event"`
	UserID         string    `json:"user_id"`
	WorkspaceID    string    `json:"workspace_id"`
	ProposalID     string    `json:"proposal_id"`
	AccountID      string    `json:"account_id"`
	PayloadHash    string    `json:"payload_hash"`
	RecipientCount int       `json:"recipient_count"`
	At             time.Time `json:"at"`
	Detail         string    `json:"detail,omitempty"`
}

// AuditSink records send-lifecycle events. A nil sink is fine (events dropped).
type AuditSink interface {
	RecordSendEvent(event SendAuditEvent)
}

// Broker is the ONLY path that sends mail (task 5.3/5.11). It owns local reply
// proposals and turns a confirmed, unmodified proposal into exactly one send.
type Broker struct {
	sender MailSender
	authz  SendAuthorizer
	audit  AuditSink
	ttl    time.Duration
	now    func() time.Time

	mu        sync.Mutex
	proposals map[string]*ReplyProposal
}

// NewBroker constructs the broker. sender and authz are required; audit may be nil.
func NewBroker(sender MailSender, authz SendAuthorizer, audit AuditSink) *Broker {
	return &Broker{
		sender:    sender,
		authz:     authz,
		audit:     audit,
		ttl:       DefaultProposalTTL,
		now:       time.Now,
		proposals: make(map[string]*ReplyProposal),
	}
}

func (b *Broker) record(event string, p *ReplyProposal, detail string) {
	if b.audit == nil || p == nil {
		return
	}
	b.audit.RecordSendEvent(SendAuditEvent{
		Event: event, UserID: p.UserID, WorkspaceID: p.WorkspaceID, ProposalID: p.ID,
		AccountID: p.Payload.AccountID, PayloadHash: p.Hash(), RecipientCount: len(p.Payload.Recipients()),
		At: b.now().UTC(), Detail: detail,
	})
}

// CreateProposal authorizes and stores a local draft reply.
func (b *Broker) CreateProposal(ctx context.Context, userID, workspaceID string, payload ReplyPayload) (*ReplyProposal, error) {
	if b == nil || b.sender == nil || b.authz == nil {
		return nil, ErrBrokerUnconfigured
	}
	if err := b.authz.AuthorizeSend(ctx, userID, workspaceID, payload); err != nil {
		return nil, ErrSendUnauthorized
	}
	now := b.now().UTC()
	p := &ReplyProposal{
		ID: uuid.NewString(), UserID: userID, WorkspaceID: workspaceID, Payload: payload,
		Status: ProposalDraft, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(b.ttl),
	}
	b.mu.Lock()
	b.proposals[p.ID] = p
	b.mu.Unlock()
	b.record("proposed", p, "")
	return p.clone(), nil
}

// GetProposal returns a copy of a proposal owned by userID.
func (b *Broker) GetProposal(userID, id string) (*ReplyProposal, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.proposals[id]
	if !ok || p.UserID != userID {
		return nil, ErrProposalNotFound
	}
	return p.clone(), nil
}

// EditProposal replaces a draft's payload. Because the payload hash changes, any
// send the user had reviewed is implicitly invalidated — they must review and
// confirm the new content (task 5.5).
func (b *Broker) EditProposal(ctx context.Context, userID, id string, payload ReplyPayload) (*ReplyProposal, error) {
	b.mu.Lock()
	p, ok := b.proposals[id]
	if !ok || p.UserID != userID {
		b.mu.Unlock()
		return nil, ErrProposalNotFound
	}
	if p.Status != ProposalDraft && p.Status != ProposalFailed {
		b.mu.Unlock()
		return nil, ErrProposalNotDraft
	}
	p.Payload = payload
	p.Status = ProposalDraft
	p.LastError = ""
	p.UpdatedAt = b.now().UTC()
	edited := p.clone()
	b.mu.Unlock()
	if err := b.authz.AuthorizeSend(ctx, userID, edited.WorkspaceID, payload); err != nil {
		return nil, ErrSendUnauthorized
	}
	b.record("edited", edited, "")
	return edited, nil
}

// CancelProposal marks a draft cancelled (idempotent for an already-terminal one).
func (b *Broker) CancelProposal(userID, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.proposals[id]
	if !ok || p.UserID != userID {
		return ErrProposalNotFound
	}
	if p.Status == ProposalDraft || p.Status == ProposalFailed {
		p.Status = ProposalCancelled
		p.UpdatedAt = b.now().UTC()
	}
	return nil
}

// ConfirmSend is the single, atomic send path. It rejects a non-draft, expired,
// wrong-owner, or payload-changed proposal, re-authorizes, and sends exactly
// once. The Draft→Sending transition under the lock makes replay and concurrent
// double-submit impossible; a send failure lands in Failed (retryable), a
// success in Sent (terminal).
func (b *Broker) ConfirmSend(ctx context.Context, userID, id, expectedHash string) (*ReplyProposal, error) {
	if b == nil || b.sender == nil || b.authz == nil {
		return nil, ErrBrokerUnconfigured
	}
	b.mu.Lock()
	p, ok := b.proposals[id]
	if !ok || p.UserID != userID {
		b.mu.Unlock()
		return nil, ErrProposalNotFound
	}
	// Single-use: only a draft or a previously-failed proposal may be sent.
	if p.Status != ProposalDraft && p.Status != ProposalFailed {
		b.mu.Unlock()
		return nil, ErrProposalNotDraft
	}
	if !b.now().Before(p.ExpiresAt) {
		p.Status = ProposalExpired
		p.UpdatedAt = b.now().UTC()
		expired := p.clone()
		b.mu.Unlock()
		b.record("expired", expired, "")
		return nil, ErrProposalExpired
	}
	// Bind to the exact reviewed payload: reject if it changed since the user
	// confirmed (an edit, or a stale confirmation).
	if strings.TrimSpace(expectedHash) != "" && expectedHash != p.Hash() {
		b.mu.Unlock()
		b.record("confirmation_denied", p, "payload hash mismatch")
		return nil, ErrPayloadChanged
	}
	// Claim the send: concurrent callers now see ProposalSending and are rejected.
	p.Status = ProposalSending
	p.UpdatedAt = b.now().UTC()
	toSend := p.clone()
	b.mu.Unlock()

	// Re-authorize immediately before the side effect (task 5.4).
	if err := b.authz.AuthorizeSend(ctx, userID, toSend.WorkspaceID, toSend.Payload); err != nil {
		b.finalize(id, ProposalFailed, "", "authorization revoked")
		b.record("confirmation_denied", toSend, "authorization revoked")
		return nil, ErrSendUnauthorized
	}

	b.record("send_attempted", toSend, "")
	result, err := b.sender.SendReply(ctx, Account{ID: toSend.Payload.AccountID}, toSend.Payload)
	if err != nil {
		final := b.finalize(id, ProposalFailed, "", err.Error())
		b.record("failed", final, sanitizeSendError(err))
		return final, err
	}
	final := b.finalize(id, ProposalSent, result.ProviderMessageID, "")
	b.record("sent", final, "")
	return final, nil
}

// finalize transitions a proposal to a terminal (or retryable) state and returns
// a copy.
func (b *Broker) finalize(id string, status ProposalStatus, sentMessageID, errMsg string) *ReplyProposal {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.proposals[id]
	if !ok {
		return nil
	}
	p.Status = status
	p.SentMessageID = sentMessageID
	p.LastError = errMsg
	p.UpdatedAt = b.now().UTC()
	return p.clone()
}

// PurgeExpired drops proposals past a retention horizon (contract §6). Returns
// the number removed. Callers may run this periodically.
func (b *Broker) PurgeExpired(retainSentFor time.Duration) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := b.now().Add(-retainSentFor)
	removed := 0
	for id, p := range b.proposals {
		if p.UpdatedAt.Before(cutoff) {
			delete(b.proposals, id)
			removed++
		}
	}
	return removed
}

// sanitizeSendError maps a provider error to a safe audit detail (no content).
func sanitizeSendError(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "credentials expired"
	case errors.Is(err, ErrPermissionDenied):
		return "permission denied"
	case errors.Is(err, ErrRateLimited):
		return "rate limited"
	case errors.Is(err, ErrTimeout):
		return "timed out"
	default:
		return "provider error"
	}
}
