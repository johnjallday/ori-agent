package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// sendAuthorizer implements mailbox.SendAuthorizer. It re-evaluates the send
// policy (task 5.4): the target workspace is the user's valid designated HQ, and
// the HQ's email binding names exactly the account the payload sends from. The
// OAuth send-scope gate is enforced downstream by Gmail (a read-only token
// yields ErrPermissionDenied), which is the staged-consent design.
type sendAuthorizer struct {
	hq         *personalhq.Service
	workspaces workspace.Store
}

func (a *sendAuthorizer) AuthorizeSend(ctx context.Context, userID, workspaceID string, payload mailbox.ReplyPayload) error {
	if a == nil || a.hq == nil || a.workspaces == nil {
		return mailbox.ErrSendUnauthorized
	}
	status, err := a.hq.Status(ctx, userID)
	if err != nil || status == nil || !status.Valid || !strings.EqualFold(status.WorkspaceID, strings.TrimSpace(workspaceID)) {
		return mailbox.ErrSendUnauthorized
	}
	ws, err := a.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return mailbox.ErrSendUnauthorized
	}
	binding, ok := emailBindingFor(ws)
	if !ok || !strings.EqualFold(stringFromConfig(binding.Config, "account_id"), strings.TrimSpace(payload.AccountID)) {
		return mailbox.ErrSendUnauthorized
	}
	return nil
}

// logAuditSink records send-lifecycle events to the structured log
// (metadata-only, task 5.10). Durable persistence is a future enhancement.
type logAuditSink struct{}

func (logAuditSink) RecordSendEvent(e mailbox.SendAuditEvent) {
	logger.Info("mail send audit", logger.Fields{
		"event": e.Event, "user_id": e.UserID, "workspace_id": e.WorkspaceID,
		"proposal_id": e.ProposalID, "account_id": e.AccountID,
		"recipient_count": e.RecipientCount, "detail": e.Detail,
	})
}

// replyService implements personalhqhttp.ReplyService, composing reply proposals
// from the HQ's connected account and routing every send through the broker.
type replyService struct {
	hq         *personalhq.Service
	workspaces workspace.Store
	accounts   emailAccountResolver
	reader     mailbox.MailboxProvider
	broker     *mailbox.Broker
}

func newReplyService(hq *personalhq.Service, workspaces workspace.Store, accounts emailAccountResolver, reader mailbox.MailboxProvider, broker *mailbox.Broker) *replyService {
	return &replyService{hq: hq, workspaces: workspaces, accounts: accounts, reader: reader, broker: broker}
}

// hqAccount resolves the user's HQ workspace ID and its connected mailbox account.
func (s *replyService) hqAccount(ctx context.Context, userID string) (string, mailbox.Account, error) {
	status, err := s.hq.Status(ctx, userID)
	if err != nil || status == nil || !status.Valid {
		return "", mailbox.Account{}, fmt.Errorf("no valid Personal HQ is designated")
	}
	ws, err := s.workspaces.Get(status.WorkspaceID)
	if err != nil || ws == nil {
		return "", mailbox.Account{}, fmt.Errorf("the Personal HQ workspace could not be loaded")
	}
	binding, ok := emailBindingFor(ws)
	if !ok {
		return "", mailbox.Account{}, fmt.Errorf("no email account is connected to your Personal HQ")
	}
	acc, err := s.accounts.GetEmailAccount(ctx, stringFromConfig(binding.Config, "account_id"))
	if err != nil || acc == nil {
		return "", mailbox.Account{}, fmt.Errorf("the connected email account could not be loaded")
	}
	return ws.ID, mailbox.Account{ID: acc.ID, Provider: string(acc.Provider), EmailAddress: acc.EmailAddress}, nil
}

func (s *replyService) DraftReply(ctx context.Context, userID, threadID, body string) (*mailbox.ReplyProposal, error) {
	wsID, acc, err := s.hqAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	payload, err := mailbox.ComposeReply(ctx, s.reader, acc, threadID, body)
	if err != nil {
		return nil, err
	}
	return s.broker.CreateProposal(ctx, userID, wsID, payload)
}

func (s *replyService) GetProposal(userID, id string) (*mailbox.ReplyProposal, error) {
	return s.broker.GetProposal(userID, id)
}

func (s *replyService) ListProposals(userID string) []*mailbox.ReplyProposal {
	return s.broker.ListProposals(userID)
}

func (s *replyService) EditProposal(ctx context.Context, userID, id string, to []string, subject, body string) (*mailbox.ReplyProposal, error) {
	current, err := s.broker.GetProposal(userID, id)
	if err != nil {
		return nil, err
	}
	payload := current.Payload
	if to != nil {
		payload.To = to
	}
	payload.Subject = subject
	payload.Body = body
	return s.broker.EditProposal(ctx, userID, id, payload)
}

func (s *replyService) CancelProposal(userID, id string) error {
	return s.broker.CancelProposal(userID, id)
}

func (s *replyService) ConfirmSend(ctx context.Context, userID, id, expectedHash string) (*mailbox.ReplyProposal, error) {
	return s.broker.ConfirmSend(ctx, userID, id, expectedHash)
}

var _ personalhqhttp.ReplyService = (*replyService)(nil)
