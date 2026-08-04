package mailbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"time"
)

// ReplyPayload is the exact, immutable content of a reply to be sent. Its Hash
// binds a send confirmation to precisely what the user reviewed, so any edit
// (which changes the hash) invalidates a prior confirmation (contract §4).
type ReplyPayload struct {
	AccountID          string   `json:"account_id"`
	SourceThreadID     string   `json:"source_thread_id"`
	InReplyToMessageID string   `json:"in_reply_to_message_id,omitempty"`
	References         string   `json:"references,omitempty"`
	To                 []string `json:"to"`
	Subject            string   `json:"subject"`
	Body               string   `json:"body"`
}

// Hash returns a stable content hash over the fields that define "the same
// send". Callers compare the hash the user confirmed against the proposal's
// current hash to reject a send whose payload changed underneath them.
func (p ReplyPayload) Hash() string {
	h := sha256.New()
	// A NUL separator between fields avoids ambiguity (e.g. To vs Subject
	// boundary) that simple concatenation would allow. Writes are discarded:
	// hash.Hash.Write is documented to never return an error.
	_, _ = io.WriteString(h, p.AccountID)
	h.Write([]byte{0})
	_, _ = io.WriteString(h, p.SourceThreadID)
	h.Write([]byte{0})
	_, _ = io.WriteString(h, p.InReplyToMessageID)
	h.Write([]byte{0})
	_, _ = io.WriteString(h, p.References)
	h.Write([]byte{0})
	_, _ = io.WriteString(h, strings.Join(p.To, ","))
	h.Write([]byte{0})
	_, _ = io.WriteString(h, p.Subject)
	h.Write([]byte{0})
	_, _ = io.WriteString(h, p.Body)
	return hex.EncodeToString(h.Sum(nil))
}

// Recipients returns the trimmed, non-empty recipient addresses.
func (p ReplyPayload) Recipients() []string {
	out := make([]string, 0, len(p.To))
	for _, r := range p.To {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// SendResult is the typed outcome of a successful send.
type SendResult struct {
	ProviderMessageID string    `json:"provider_message_id"`
	ThreadID          string    `json:"thread_id"`
	SentAt            time.Time `json:"sent_at"`
}

// MailSender sends a reply. It is deliberately SEPARATE from MailboxProvider
// (read), so a read-only wiring can never send, and so the broker can depend on
// the narrow send capability alone. A read-only Gmail account (no send scope)
// returns ErrExpired/ErrPermissionDenied when SendReply is attempted.
type MailSender interface {
	SendReply(ctx context.Context, account Account, payload ReplyPayload) (SendResult, error)
}
