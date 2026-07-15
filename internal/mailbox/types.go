// Package mailbox defines the provider-neutral read surface for a connected
// personal mailbox (contract docs/features/personal-hq-assistant.md §3.3). No
// provider-specific field (Gmail label IDs, history IDs, raw MIME, tokens) ever
// crosses this interface: callers above it see only the normalized types here.
//
// Sending is deliberately NOT part of this package's read surface — every send
// goes through the centralized broker (Group 5), never a mailbox read path.
package mailbox

import (
	"context"
	"time"
)

// Bounds keep every query small so an eventual LLM prompt stays bounded and a
// hostile or huge mailbox can never blow up collection (contract §3.3, task
// 3.5/3.8). Requests exceeding these are clamped, never rejected.
const (
	MaxThreadsPerQuery  = 25
	MaxLookbackDays     = 30
	DefaultMaxResults   = 10
	DefaultLookbackDays = 7
	// MaxSnippetLen bounds each sanitized message snippet.
	MaxSnippetLen = 2000
)

// Health is the connection state of a mailbox account, mapped to a distinct UI
// state so an unavailable source is never silently shown as "no mail" (task
// 3.11, contract §3.3).
type Health string

const (
	HealthUnknown      Health = ""
	HealthHealthy      Health = "healthy"
	HealthDisconnected Health = "disconnected"
	// HealthExpired means the stored credential expired or was revoked and the
	// user must reconnect.
	HealthExpired Health = "expired"
	// HealthScopeUpgrade means the account is connected read-only and a send
	// requires the separate send-scope upgrade (contract §3.1).
	HealthScopeUpgrade Health = "scope_upgrade"
)

// Account is a token-free identity of a connected mailbox. Credentials never
// appear here — the concrete provider adapter (e.g. Gmail) resolves them
// internally via a Vault credential resolver (contract §3.3), so no token can
// leak to a caller, log, or LLM prompt.
type Account struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"` // "gmail" (neutral label)
	EmailAddress string `json:"email_address"`
	Health       Health `json:"health"`
}

// Participant is a normalized sender/recipient.
type Participant struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Message is a bounded, sanitized projection of one email. All text fields are
// UNTRUSTED input (contract §3): they are sanitized here and must never be
// treated as instructions downstream.
type Message struct {
	ID       string        `json:"id"` // stable provider message ID
	ThreadID string        `json:"thread_id"`
	From     Participant   `json:"from"`
	To       []Participant `json:"to,omitempty"`
	Subject  string        `json:"subject"`
	Snippet  string        `json:"snippet"` // bounded + sanitized plain text
	SentAt   time.Time     `json:"sent_at"`
	// FromUser is true when the mailbox owner sent this message, used to derive
	// WaitingOnUser without leaking provider flags.
	FromUser bool `json:"from_user"`
	Unread   bool `json:"unread"`
}

// Thread is a bounded projection of an email thread. Messages may be empty in a
// list-only projection (SearchThreads) and populated by GetThread.
type Thread struct {
	ID            string        `json:"id"` // stable provider thread ID
	AccountID     string        `json:"account_id"`
	Subject       string        `json:"subject"`
	Participants  []Participant `json:"participants,omitempty"`
	Messages      []Message     `json:"messages,omitempty"`
	LastMessageAt time.Time     `json:"last_message_at"`
	Unread        bool          `json:"unread"`
	// WaitingOnUser is a bounded heuristic: the most recent message is inbound
	// (not from the user) and unanswered — the signal the brief surfaces.
	WaitingOnUser bool `json:"waiting_on_user"`
}

// Query bounds a thread search. Excluding spam/trash/drafts is enforced by the
// provider adapter unconditionally (task 3.8) and is not expressible here.
type Query struct {
	// Labels narrows to these include-labels; empty means the default inbox.
	Labels []string `json:"labels,omitempty"`
	// MaxResults and LookbackDays are clamped to the Max* bounds.
	MaxResults   int `json:"max_results,omitempty"`
	LookbackDays int `json:"lookback_days,omitempty"`
	// PageToken is an opaque provider cursor for pagination.
	PageToken string `json:"page_token,omitempty"`
	// WaitingOnUserOnly narrows to threads awaiting the user (for the brief).
	WaitingOnUserOnly bool `json:"waiting_on_user_only,omitempty"`
}

// Normalized returns a copy with MaxResults/LookbackDays clamped into the
// allowed bounds and sane defaults applied, so no caller can request an
// unbounded read.
func (q Query) Normalized() Query {
	out := q
	if out.MaxResults <= 0 {
		out.MaxResults = DefaultMaxResults
	}
	if out.MaxResults > MaxThreadsPerQuery {
		out.MaxResults = MaxThreadsPerQuery
	}
	if out.LookbackDays <= 0 {
		out.LookbackDays = DefaultLookbackDays
	}
	if out.LookbackDays > MaxLookbackDays {
		out.LookbackDays = MaxLookbackDays
	}
	return out
}

// ThreadPage is a bounded page of thread projections plus an opaque cursor.
// An empty Threads slice with an empty NextPageToken is a HEALTHY empty
// result — distinct from an error, which is signaled by a returned error value
// (task 3.11): callers must not conflate "no mail" with "could not read".
type ThreadPage struct {
	Threads       []Thread `json:"threads"`
	NextPageToken string   `json:"next_page_token,omitempty"`
}

// MailboxProvider is the provider-neutral read surface. Implementations
// (internal/mailbox Gmail adapter) must clamp queries via Query.Normalized,
// exclude spam/trash/drafts, return stable provider IDs, and classify failures
// into the typed errors in errors.go.
type MailboxProvider interface {
	// SearchThreads returns a bounded page of thread projections for account.
	SearchThreads(ctx context.Context, account Account, q Query) (ThreadPage, error)
	// GetThread returns a single thread with its bounded, sanitized messages.
	GetThread(ctx context.Context, account Account, threadID string) (Thread, error)
}
