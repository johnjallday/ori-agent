package mailbox

import (
	"context"
	"strings"
)

// ComposeReply loads an authorized source thread and builds a bounded reply
// payload skeleton: recipient (the most recent sender), a "Re:" subject, and the
// source thread id for Gmail threading. The caller supplies the body (an agent
// draft or user text). It never mutates the mailbox — it only reads the thread
// to derive reply metadata (task 5.2).
//
// v1 relies on Gmail's ThreadId for threading rather than RFC In-Reply-To
// headers, because the neutral Message projection intentionally does not expose
// raw Message-ID headers. Gmail files a reply into the thread by ThreadId, so
// this threads correctly within Gmail; richer cross-client headers are a future
// refinement.
func ComposeReply(ctx context.Context, reader MailboxProvider, account Account, threadID, body string) (ReplyPayload, error) {
	thread, err := reader.GetThread(ctx, account, threadID)
	if err != nil {
		return ReplyPayload{}, err
	}
	if len(thread.Messages) == 0 {
		return ReplyPayload{}, ErrNotFound
	}
	last := thread.Messages[len(thread.Messages)-1]

	subject := strings.TrimSpace(thread.Subject)
	if subject == "" {
		subject = strings.TrimSpace(last.Subject)
	}
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	to := strings.TrimSpace(last.From.Address)
	// If the latest message is the user's own, reply to the first other
	// participant instead of to themselves.
	if last.FromUser || to == "" || strings.EqualFold(to, account.EmailAddress) {
		for _, p := range thread.Participants {
			if a := strings.TrimSpace(p.Address); a != "" && !strings.EqualFold(a, account.EmailAddress) {
				to = a
				break
			}
		}
	}

	payload := ReplyPayload{
		AccountID:      account.ID,
		SourceThreadID: thread.ID,
		Subject:        subject,
		Body:           body,
	}
	if to != "" {
		payload.To = []string{to}
	}
	return payload, nil
}
