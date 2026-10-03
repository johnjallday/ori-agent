package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/emailsetup"
	"github.com/johnjallday/ori-agent/internal/emailsetuphttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The server side of the "Set up email" card. The card asks for an address and
// an app password; the emailsetup service checks the login and keeps it, and
// this file supplies the two things only the server knows how to do: create the
// user's Email Ops workspace and link the mailbox to it.

// emailSetupURL opens the card on any page that loads it.
const emailSetupURL = emailsetup.SetupURL

const emailOpsWorkspaceName = "Email Ops"

// wireEmailSetup builds the card once the vault store exists, and unlocks the
// vault Ori keeps for mail logins so they are readable from the first request.
// The mailbox linker, the session handler, the setup wizard, and the progression
// engine are built later; emailSetupHome reads them when a setup runs.
func (b *ServerBuilder) wireEmailSetup(vaultStore *vault.Store) {
	if vaultStore == nil {
		return
	}
	var secrets vault.SecretStore
	if b.configManager != nil {
		secrets = b.configManager.SecretStore()
	}
	keyring := vault.NewKeyring(vaultStore, secrets)
	b.vaultKeyring = keyring
	if err := keyring.UnlockRemembered(context.Background()); err != nil {
		logger.Warn("Could not unlock the vault Ori keeps for mail logins", logger.Fields{"error": err.Error()})
	}
	if b.vaultHandler != nil {
		b.vaultHandler.SetVaultDeletedHook(func(vaultID string) {
			if err := keyring.Forget(vaultID); err != nil {
				logger.Warn("Could not forget a deleted vault's password", logger.Fields{"error": err.Error()})
			}
		})
	}
	service := emailsetup.NewService(nil, mailbox.NewIMAPProvider(nil), keyring, vaultStore, emailSetupHome{b: b})
	b.emailSetupHandler = emailsetuphttp.NewHandler(service)
}

// emailSetupHome is the Email Ops workspace as the setup card sees it.
type emailSetupHome struct {
	b *ServerBuilder
}

// EnsureEmailOps returns the user's Email Ops workspace, creating it from the
// email-ops blueprint when there is none. Creation goes through the same
// workspace creation the library uses, so the workspace gets its Postmaster and
// Inbox agents, starter tasks, and Setup Wizard exactly as a hand-made one does.
func (h emailSetupHome) EnsureEmailOps(ctx context.Context, userID string) (emailsetup.Workspace, error) {
	b := h.b
	if b == nil || b.workspaceFileStore == nil || b.sessionHandler == nil {
		return emailsetup.Workspace{}, errEmailOpsQuestUnavailable
	}
	// Provenance lives in workspace.json, so only the folder store can tell an
	// Email Ops workspace from any other.
	id, err := workspace.ResolveEmailOpsWorkspace(b.workspaceFileStore, userID)
	if err != nil {
		return emailsetup.Workspace{}, err
	}
	created := false
	if id == "" {
		if id, err = createEmailOpsWorkspace(ctx, b); err != nil {
			return emailsetup.Workspace{}, err
		}
		created = true
	}
	ws, err := b.workspaceFileStore.Get(id)
	if err != nil || ws == nil {
		return emailsetup.Workspace{}, fmt.Errorf("load Email Ops: %w", err)
	}
	return emailsetup.Workspace{
		ID: ws.ID, Name: questWorkspaceLabel(ws.Name), Route: questWorkspaceRoute(ws.FolderSlug), Created: created,
	}, nil
}

// createEmailOpsWorkspace creates the workspace, numbering the name when the
// user already has an unrelated workspace called Email Ops.
func createEmailOpsWorkspace(ctx context.Context, b *ServerBuilder) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		name := emailOpsWorkspaceName
		if attempt > 1 {
			name = fmt.Sprintf("%s %d", emailOpsWorkspaceName, attempt)
		}
		id, err := b.sessionHandler.CreateFromTemplate(ctx, name, workspace.EmailOpsTemplateID)
		if err == nil {
			return id, nil
		}
		lastErr = err
		// CreateFromTemplate reports the handler's status in its message; only a
		// name clash is worth another name.
		if !strings.Contains(err.Error(), "(409)") {
			break
		}
	}
	return "", fmt.Errorf("create Email Ops: %w", lastErr)
}

// LinkMailbox links the account read and search only, records the workspace
// wizard's mailbox step so both surfaces agree, and completes the starter
// mission that asks the user to connect a source.
func (h emailSetupHome) LinkMailbox(ctx context.Context, userID, workspaceID, accountID string) error {
	b := h.b
	if b == nil || b.mailboxLinker == nil {
		return errEmailOpsQuestUnavailable
	}
	if _, err := b.mailboxLinker.LinkWorkspaceMailbox(ctx, userID, workspaceID, accountID); err != nil {
		return err
	}
	if b.setupWizardService != nil {
		// Bookkeeping: the wizard re-derives the step from the same readiness.
		if _, err := b.setupWizardService.Confirm(ctx, workspaceID, emailOpsWizardMailboxStepID, setupwizard.StepAction{Type: setupwizard.ActionConfirm}); err != nil {
			logger.Warn("Email setup could not record the wizard mailbox step", logger.Fields{"error": err.Error()})
		}
	}
	if b.progressionEngine != nil {
		b.progressionEngine.Complete(progression.ConnectSourceQuestID)
	}
	return nil
}
