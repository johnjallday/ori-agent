package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/emailtriage"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// The "Needs you" list: the server side of the panel on a workspace with a
// linked mailbox. It reads as the user, not as an agent, so the Inbox-only
// gate does not apply; the workspace's owner check and binding do.

// wireNeedsYou builds the list over the mailbox runtime. Called from
// wireMailboxRuntime, once the cached reader exists.
func (b *ServerBuilder) wireNeedsYou(reader mailbox.MailboxProvider) {
	if b.sessionStore == nil || reader == nil {
		return
	}
	model := func() emailtriage.Completer {
		return systemModelCompleter(b.configManager, b.llmFactory)
	}
	b.emailTriage = emailtriage.NewService(reader, emailtriage.NewSQLiteStore(b.sessionStore.DB()), model)
	if b.personalHQHandler != nil {
		b.personalHQHandler.SetNeedsYouService(needsYouService{b: b, triage: b.emailTriage})
	}
}

// systemModelCompleter is the configured system model, or nil when none is set,
// resolved on every read so a model configured later is used without a restart.
func systemModelCompleter(cfg *config.Manager, factory *llm.Factory) emailtriage.Completer {
	if cfg == nil || factory == nil || !cfg.IsSystemModelConfigured() {
		return nil
	}
	return triageCompleter{chat: &systemModelChatCompleter{configManager: cfg, llmFactory: factory}}
}

type triageCompleter struct {
	chat *systemModelChatCompleter
}

func (c triageCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	resp, err := c.chat.Chat(ctx, llm.ChatRequest{
		SystemPrompt: system,
		Messages:     []llm.Message{{Role: "user", Content: user}},
		Temperature:  0.1,
		MaxTokens:    1500,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("the model returned no answer")
	}
	return llm.StripCodeFence(resp.Content), nil
}

// needsYouService implements personalhqhttp.NeedsYouService.
type needsYouService struct {
	b      *ServerBuilder
	triage *emailtriage.Service
}

func (s needsYouService) NeedsYou(ctx context.Context, userID, workspaceID string) (emailtriage.List, error) {
	account, err := s.account(ctx, userID, workspaceID)
	if err != nil {
		return emailtriage.List{}, err
	}
	list, err := s.triage.List(ctx, workspaceID, account)
	if err != nil {
		return emailtriage.List{}, readFailure(err)
	}
	return list, nil
}

func (s needsYouService) MarkNeedsYou(ctx context.Context, userID, workspaceID, threadID string, bucket emailtriage.Bucket) error {
	account, err := s.account(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if err := s.triage.SetBucket(ctx, workspaceID, account, threadID, bucket); err != nil {
		if errors.Is(err, emailtriage.ErrUnknownThread) {
			return err
		}
		return &personalhqhttp.NeedsYouError{Code: "invalid_bucket", Message: "Mark an email as needing you, for your information, or not important.", Status: http.StatusBadRequest}
	}
	return nil
}

// TrackNeedsYou makes the thread a follow-up in this workspace. The follow-up
// remembers the thread it came from, so tracking the same thread twice makes
// one follow-up.
func (s needsYouService) TrackNeedsYou(ctx context.Context, userID, workspaceID, threadID string) error {
	account, err := s.account(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	followups := s.b.followUpService
	if followups == nil {
		return &personalhqhttp.NeedsYouError{Code: "unavailable", Message: "Follow-ups are unavailable in this build.", Status: http.StatusServiceUnavailable}
	}
	_, err = s.triage.Track(ctx, workspaceID, account, threadID, func(state emailtriage.State) (string, error) {
		category := followup.CategoryIOwe
		if state.Kind == emailtriage.KindDecision {
			category = followup.CategoryNeedsDecision
		}
		item, err := followups.Capture(ctx, followup.CaptureInput{
			UserID: userID, WorkspaceID: workspaceID,
			Category: category, Direction: followup.DirectionOutbound,
			Title:        boundedText(firstNonEmptyText(state.Subject, "Email from "+state.From), 200),
			Detail:       boundedText(state.Why, 1000),
			Counterparty: boundedText(state.From, 200),
			Source:       followup.SourceRef{Type: "email_thread", ID: state.ThreadID, AccountID: account.ID},
			Provenance:   followup.ProvenanceExplicit, Confidence: followup.ConfidenceHigh,
		})
		if err != nil {
			return "", err
		}
		return item.ID, nil
	})
	return err
}

// account resolves the mailbox linked to a workspace the user owns, and names
// the repair when it cannot be read.
func (s needsYouService) account(ctx context.Context, userID, workspaceID string) (mailbox.Account, error) {
	b := s.b
	if b == nil || b.mailboxLinker == nil || b.vaultStore == nil {
		return mailbox.Account{}, &personalhqhttp.NeedsYouError{Code: "unavailable", Message: "Email isn't available in this build.", Status: http.StatusServiceUnavailable}
	}
	ws, err := b.mailboxLinker.ownedWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return mailbox.Account{}, &personalhqhttp.NeedsYouError{Code: "workspace_unavailable", Message: "This workspace isn't available.", Status: http.StatusNotFound}
	}
	binding, ok := emailBindingFor(ws)
	if !ok {
		return mailbox.Account{}, personalhqhttp.ErrNoMailboxLinked
	}
	acc, err := b.vaultStore.GetEmailAccount(ctx, stringFromConfig(binding.Config, "account_id"))
	switch {
	case errors.Is(err, vault.ErrVaultLocked):
		return mailbox.Account{}, &personalhqhttp.NeedsYouError{Code: "vault_locked", Message: "The vault holding your email login is locked. Unlock it in Vaults to see your mail.", Status: http.StatusConflict}
	case err != nil || acc == nil:
		return mailbox.Account{}, &personalhqhttp.NeedsYouError{Code: "account_unavailable", Message: "The email account linked here is no longer available. Set up email again.", Status: http.StatusConflict}
	case !hasMailCredential(acc):
		return mailbox.Account{}, &personalhqhttp.NeedsYouError{Code: "reconnect", Message: "The linked email account needs reconnecting. Set up email again.", Status: http.StatusConflict}
	}
	return mailbox.Account{ID: acc.ID, Provider: string(acc.Provider), EmailAddress: acc.EmailAddress}, nil
}

// readFailure turns a mailbox read error into the panel's message.
func readFailure(err error) error {
	switch {
	case errors.Is(err, mailbox.ErrExpired), errors.Is(err, mailbox.ErrDisconnected):
		return &personalhqhttp.NeedsYouError{Code: "reconnect", Message: "Your mail provider refused the saved login. Set up email again to reconnect.", Status: http.StatusConflict}
	case errors.Is(err, mailbox.ErrRateLimited), errors.Is(err, mailbox.ErrTimeout):
		return &personalhqhttp.NeedsYouError{Code: "busy", Message: "Your mail server is slow to answer right now. Try again in a minute.", Status: http.StatusServiceUnavailable}
	}
	return &personalhqhttp.NeedsYouError{Code: "unreachable", Message: "Ori couldn't read your mail just now. Try again in a moment.", Status: http.StatusBadGateway}
}

func boundedText(text string, limit int) string {
	text = strings.TrimSpace(text)
	for len(text) > limit {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return text
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
