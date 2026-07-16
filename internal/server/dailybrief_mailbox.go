package server

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// briefEmailReadTimeout bounds the mailbox read during brief generation so a
// slow provider degrades only the email source, never the whole brief (task 4.7).
const briefEmailReadTimeout = 8 * time.Second

// briefEmailMaxThreads bounds how many threads the brief pulls.
const briefEmailMaxThreads = 10

// dailyBriefMailboxSource implements dailybrief.MailboxSource. Unlike the agent
// tool path, the Daily Brief reads on behalf of the user's own HQ (it is the app
// surfacing the user's mail to the user, not an arbitrary agent), so it resolves
// the HQ's connected account directly rather than through the per-agent gate. It
// still never touches credentials — reads go through the mailbox provider, whose
// resolver holds the tokens.
type dailyBriefMailboxSource struct {
	hq         *personalhq.Service
	workspaces workspace.Store
	accounts   emailAccountResolver
	provider   mailbox.MailboxProvider
}

func newDailyBriefMailboxSource(hq *personalhq.Service, workspaces workspace.Store, accounts emailAccountResolver, provider mailbox.MailboxProvider) *dailyBriefMailboxSource {
	return &dailyBriefMailboxSource{hq: hq, workspaces: workspaces, accounts: accounts, provider: provider}
}

// BriefEmailThreads returns bounded email attention projections for the user's
// designated HQ. A missing HQ / binding / account is ErrEmailNotConfigured (not
// a gap); a provider read failure returns the error (the brief turns it into a
// named gap).
func (s *dailyBriefMailboxSource) BriefEmailThreads(ctx context.Context, userID string) ([]dailybrief.EmailThreadSnapshot, error) {
	if s == nil || s.provider == nil || s.hq == nil || s.workspaces == nil || s.accounts == nil {
		return nil, dailybrief.ErrEmailNotConfigured
	}
	status, err := s.hq.Status(ctx, userID)
	if err != nil || status == nil || !status.Valid {
		return nil, dailybrief.ErrEmailNotConfigured
	}
	ws, err := s.workspaces.Get(status.WorkspaceID)
	if err != nil || ws == nil {
		return nil, dailybrief.ErrEmailNotConfigured
	}
	binding, ok := emailBindingFor(ws)
	if !ok {
		return nil, dailybrief.ErrEmailNotConfigured
	}
	accountID := stringFromConfig(binding.Config, "account_id")
	acc, err := s.accounts.GetEmailAccount(ctx, accountID)
	if err != nil || acc == nil {
		return nil, dailybrief.ErrEmailNotConfigured
	}

	readCtx, cancel := context.WithTimeout(ctx, briefEmailReadTimeout)
	defer cancel()
	page, err := s.provider.SearchThreads(readCtx, mailbox.Account{
		ID: acc.ID, Provider: string(acc.Provider), EmailAddress: acc.EmailAddress,
	}, mailbox.Query{MaxResults: briefEmailMaxThreads})
	if err != nil {
		return nil, err // a real read failure → the brief records a named gap
	}

	out := make([]dailybrief.EmailThreadSnapshot, 0, len(page.Threads))
	for _, t := range page.Threads {
		out = append(out, dailybrief.EmailThreadSnapshot{
			Ref: dailybrief.SourceRef{
				WorkspaceID: ws.ID,
				EntityType:  "email_thread",
				EntityID:    t.ID,
				AccountID:   acc.ID,
				Timestamp:   t.LastMessageAt,
			},
			Subject:       t.Subject,
			From:          counterpartyLabel(t),
			WaitingOnUser: t.WaitingOnUser,
			Unread:        t.Unread,
		})
	}
	return out, nil
}

// counterpartyLabel picks a human sender label for a thread from its (already
// sanitized) participants.
func counterpartyLabel(t mailbox.Thread) string {
	if len(t.Participants) == 0 {
		return ""
	}
	p := t.Participants[0]
	if name := strings.TrimSpace(p.Name); name != "" {
		return name
	}
	return strings.TrimSpace(p.Address)
}
