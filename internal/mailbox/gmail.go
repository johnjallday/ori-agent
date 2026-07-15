package mailbox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// CredentialResolver resolves a mailbox account ID to an OAuth2 token source.
// It is the ONLY boundary that touches stored credentials (contract §3.3):
// implementations decrypt the Vault record and return a refreshing TokenSource,
// never raw tokens. No token value crosses back to the Gmail adapter's callers,
// logs, errors, or LLM prompts.
type CredentialResolver interface {
	// TokenSource returns a refreshing token source for accountID, or a typed
	// mailbox error (ErrDisconnected / ErrExpired) when no usable credential
	// exists.
	TokenSource(ctx context.Context, accountID string) (oauth2.TokenSource, error)
}

// gmailEndpointOverride lets tests point the adapter at an httptest server; in
// production it is empty and the default Gmail endpoint is used.
type GmailProvider struct {
	resolver         CredentialResolver
	endpointOverride string
}

// NewGmailProvider constructs the Gmail-backed MailboxProvider.
func NewGmailProvider(resolver CredentialResolver) *GmailProvider {
	return &GmailProvider{resolver: resolver}
}

var _ MailboxProvider = (*GmailProvider)(nil)

func (g *GmailProvider) service(ctx context.Context, account Account) (*gmailapi.Service, error) {
	if g == nil || g.resolver == nil {
		return nil, ErrDisconnected
	}
	ts, err := g.resolver.TokenSource(ctx, account.ID)
	if err != nil {
		return nil, err // already a typed mailbox error
	}
	opts := []option.ClientOption{option.WithTokenSource(ts)}
	if g.endpointOverride != "" {
		opts = append(opts, option.WithEndpoint(g.endpointOverride))
	}
	svc, err := gmailapi.NewService(ctx, opts...)
	if err != nil {
		return nil, ErrProvider
	}
	if g.endpointOverride != "" {
		svc.BasePath = g.endpointOverride
	}
	return svc, nil
}

// SearchThreads lists threads matching q (bounded, spam/trash/drafts excluded)
// and enriches each with bounded metadata (subject, participants, waiting-on-user).
func (g *GmailProvider) SearchThreads(ctx context.Context, account Account, q Query) (ThreadPage, error) {
	q = q.Normalized()
	svc, err := g.service(ctx, account)
	if err != nil {
		return ThreadPage{}, err
	}
	call := svc.Users.Threads.List("me").
		Q(buildGmailQuery(q)).
		MaxResults(int64(q.MaxResults)).
		Context(ctx)
	if q.PageToken != "" {
		call = call.PageToken(q.PageToken)
	}
	resp, err := call.Do()
	if err != nil {
		return ThreadPage{}, classifyGmailError(err)
	}

	page := ThreadPage{NextPageToken: resp.NextPageToken}
	for _, t := range resp.Threads {
		th, err := g.threadMetadata(ctx, svc, account, t.Id)
		if err != nil {
			// A single failing thread degrades to skipping it, not failing the
			// whole page — the caller still gets a bounded, usable result.
			continue
		}
		if q.WaitingOnUserOnly && !th.WaitingOnUser {
			continue
		}
		page.Threads = append(page.Threads, th)
	}
	return page, nil
}

// GetThread returns one thread with its bounded, sanitized messages.
func (g *GmailProvider) GetThread(ctx context.Context, account Account, threadID string) (Thread, error) {
	svc, err := g.service(ctx, account)
	if err != nil {
		return Thread{}, err
	}
	full, err := svc.Users.Threads.Get("me", threadID).Format("full").Context(ctx).Do()
	if err != nil {
		return Thread{}, classifyGmailError(err)
	}
	return mapThread(account, full, true), nil
}

// threadMetadata fetches a lightweight metadata projection of a thread.
func (g *GmailProvider) threadMetadata(ctx context.Context, svc *gmailapi.Service, account Account, threadID string) (Thread, error) {
	t, err := svc.Users.Threads.Get("me", threadID).
		Format("metadata").
		MetadataHeaders("Subject", "From", "To", "Date").
		Context(ctx).Do()
	if err != nil {
		return Thread{}, classifyGmailError(err)
	}
	return mapThread(account, t, false), nil
}

// mapThread converts a Gmail thread into the neutral projection. When withBodies
// is true each message's sanitized snippet is derived from its decoded body;
// otherwise the Gmail-provided snippet is used (metadata projection).
func mapThread(account Account, t *gmailapi.Thread, withBodies bool) Thread {
	out := Thread{ID: t.Id, AccountID: account.ID}
	seen := map[string]struct{}{}
	for _, m := range t.Messages {
		msg := mapMessage(account, m, withBodies)
		out.Messages = append(out.Messages, msg)
		if out.Subject == "" && msg.Subject != "" {
			out.Subject = msg.Subject
		}
		if p := msg.From; p.Address != "" {
			if _, dup := seen[strings.ToLower(p.Address)]; !dup {
				seen[strings.ToLower(p.Address)] = struct{}{}
				out.Participants = append(out.Participants, p)
			}
		}
		if msg.SentAt.After(out.LastMessageAt) {
			out.LastMessageAt = msg.SentAt
		}
		if msg.Unread {
			out.Unread = true
		}
	}
	if n := len(out.Messages); n > 0 {
		last := out.Messages[n-1]
		out.WaitingOnUser = !last.FromUser && out.Unread
	}
	return out
}

func mapMessage(account Account, m *gmailapi.Message, withBody bool) Message {
	msg := Message{ID: m.Id, ThreadID: m.ThreadId, Unread: hasLabel(m.LabelIds, "UNREAD")}
	if m.Payload != nil {
		for _, h := range m.Payload.Headers {
			switch strings.ToLower(h.Name) {
			case "subject":
				msg.Subject = SanitizeText(h.Value, 500)
			case "from":
				msg.From = parseParticipant(h.Value)
			case "to":
				msg.To = parseParticipants(h.Value)
			}
		}
	}
	msg.FromUser = account.EmailAddress != "" &&
		strings.EqualFold(msg.From.Address, account.EmailAddress)
	if m.InternalDate > 0 {
		msg.SentAt = time.UnixMilli(m.InternalDate).UTC()
	}
	if withBody {
		msg.Snippet = SanitizeText(StripQuotedHistory(extractBody(m.Payload)), MaxSnippetLen)
	} else {
		msg.Snippet = SanitizeText(m.Snippet, MaxSnippetLen)
	}
	return msg
}

// extractBody walks a MIME payload and returns the best text representation,
// preferring text/plain, falling back to text/html (which the sanitizer strips
// to text). Recurses into multipart parts.
func extractBody(part *gmailapi.MessagePart) string {
	if part == nil {
		return ""
	}
	if strings.HasPrefix(part.MimeType, "text/plain") && part.Body != nil && part.Body.Data != "" {
		return decodeBody(part.Body.Data)
	}
	// Depth-first: prefer a text/plain descendant, else the first text/html.
	var htmlFallback string
	for _, p := range part.Parts {
		if strings.HasPrefix(p.MimeType, "text/plain") && p.Body != nil && p.Body.Data != "" {
			return decodeBody(p.Body.Data)
		}
		if htmlFallback == "" {
			if got := extractBody(p); got != "" {
				htmlFallback = got
			}
		}
	}
	if htmlFallback != "" {
		return htmlFallback
	}
	if strings.HasPrefix(part.MimeType, "text/html") && part.Body != nil && part.Body.Data != "" {
		return decodeBody(part.Body.Data)
	}
	return ""
}

func decodeBody(data string) string {
	b, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		// Gmail sometimes omits padding; try the raw variant.
		if b2, err2 := base64.RawURLEncoding.DecodeString(data); err2 == nil {
			return string(b2)
		}
		return ""
	}
	return string(b)
}

func hasLabel(labels []string, want string) bool {
	return slices.Contains(labels, want)
}

func parseParticipant(raw string) Participant {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Participant{}
	}
	if addr, err := mail.ParseAddress(raw); err == nil {
		return Participant{Name: addr.Name, Address: addr.Address}
	}
	return Participant{Address: raw}
}

func parseParticipants(raw string) []Participant {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if addrs, err := mail.ParseAddressList(raw); err == nil {
		out := make([]Participant, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, Participant{Name: a.Name, Address: a.Address})
		}
		return out
	}
	return []Participant{{Address: raw}}
}

// buildGmailQuery renders the bounded query string, unconditionally excluding
// spam/trash/drafts (task 3.8) and bounding the lookback window. Extra include
// labels are ANDed in.
func buildGmailQuery(q Query) string {
	parts := []string{
		"-in:spam", "-in:trash", "-in:drafts",
		fmt.Sprintf("newer_than:%dd", q.LookbackDays),
	}
	for _, l := range q.Labels {
		l = strings.TrimSpace(l)
		if l != "" {
			parts = append(parts, "label:"+l)
		}
	}
	return strings.Join(parts, " ")
}

// classifyGmailError maps a Gmail/transport error to a typed mailbox error,
// never surfacing the raw provider error (which can carry tokens/PII).
func classifyGmailError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case 401:
			return ErrExpired
		case 403:
			if isRateLimit(gerr) {
				return &RateLimitError{RetryAfter: retryAfter(gerr)}
			}
			return ErrPermissionDenied
		case 404:
			return ErrNotFound
		case 429:
			return &RateLimitError{RetryAfter: retryAfter(gerr)}
		}
	}
	return ErrProvider
}

func isRateLimit(gerr *googleapi.Error) bool {
	if strings.Contains(strings.ToLower(gerr.Message), "rate limit") {
		return true
	}
	for _, e := range gerr.Errors {
		r := strings.ToLower(e.Reason)
		if strings.Contains(r, "ratelimit") || strings.Contains(r, "userratelimit") {
			return true
		}
	}
	return false
}

func retryAfter(gerr *googleapi.Error) time.Duration {
	if gerr == nil || gerr.Header == nil {
		return 0
	}
	if v := gerr.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}
