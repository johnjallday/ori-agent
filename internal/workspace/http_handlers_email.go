package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/vault"
)

type emailAccountStore interface {
	GetEmailAccount(ctx context.Context, id string) (*vault.EmailAccount, error)
}

func (h *HTTPHandler) SetEmailAccountStore(store emailAccountStore) {
	if h == nil {
		return
	}
	h.emailAccounts = store
}

func (h *HTTPHandler) normalizeBindingForPersistence(ctx context.Context, workspaceID string, binding MCPBinding) (MCPBinding, error) {
	if !isEmailMCPServer(binding.ServerName) {
		return binding, nil
	}
	if h == nil || h.emailAccounts == nil {
		return binding, fmt.Errorf("email account store is not configured")
	}

	normalizedConfig, _, err := h.normalizeEmailBindingConfig(ctx, workspaceID, binding.Config)
	if err != nil {
		return binding, err
	}
	binding.Config = normalizedConfig
	return binding, nil
}

func (h *HTTPHandler) normalizeEmailBindingConfig(ctx context.Context, workspaceID string, config map[string]any) (map[string]any, *vault.EmailAccount, error) {
	normalized := cloneInterfaceMap(config)
	if normalized == nil {
		normalized = make(map[string]any)
	}

	accountID, ok := stringFromConfigValue(normalized["account_id"])
	if !ok {
		accountID, ok = stringFromConfigValue(normalized["account_vault_record_id"])
	}
	if !ok {
		return nil, nil, fmt.Errorf("email MCP bindings require config.account_id")
	}

	account, err := h.emailAccounts.GetEmailAccount(ctx, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("load email account %q: %w", accountID, err)
	}
	if account == nil {
		return nil, nil, fmt.Errorf("email account %q was not found", accountID)
	}
	if strings.TrimSpace(account.WorkspaceID) != "" && !strings.EqualFold(strings.TrimSpace(account.WorkspaceID), strings.TrimSpace(workspaceID)) {
		return nil, nil, fmt.Errorf("email account %q is scoped to workspace %s", accountID, account.WorkspaceID)
	}

	actions, ok := stringSliceFromConfigValue(normalized["allowed_actions"])
	if !ok || len(actions) == 0 {
		actions = []string{"read", "search"}
	}
	actions, err = normalizeEmailAllowedActions(actions)
	if err != nil {
		return nil, nil, err
	}

	normalized["account_id"] = account.ID
	delete(normalized, "account_vault_record_id")
	normalized["allowed_actions"] = actions

	if mailboxes, ok := stringSliceFromConfigValue(normalized["mailboxes"]); ok && len(mailboxes) > 0 {
		normalized["mailboxes"] = mailboxes
	} else {
		delete(normalized, "mailboxes")
	}

	if hasString(actions, "send") {
		if confirmation, ok := boolFromConfigValue(normalized["require_send_confirmation"]); ok {
			normalized["require_send_confirmation"] = confirmation
		} else {
			normalized["require_send_confirmation"] = true
		}
	} else {
		delete(normalized, "require_send_confirmation")
	}

	return normalized, account, nil
}

func (h *HTTPHandler) mcpBindingResponse(ctx context.Context, binding MCPBinding) map[string]any {
	resp := map[string]any{
		"id":          binding.ID,
		"server_name": binding.ServerName,
		"alias":       binding.Alias,
		"enabled":     binding.Enabled,
		"scope":       binding.Scope,
		"config":      binding.Config,
		"created_at":  binding.CreatedAt,
		"updated_at":  binding.UpdatedAt,
	}

	account, err := h.lookupEmailAccountForBinding(ctx, binding)
	if account != nil {
		resp["email_account"] = emailAccountSummary(account)
	} else if err != nil && isEmailMCPServer(binding.ServerName) {
		resp["email_account_missing"] = true
	}

	return resp
}

func (h *HTTPHandler) mcpBindingResponses(ctx context.Context, bindings []MCPBinding) []map[string]any {
	items := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, h.mcpBindingResponse(ctx, binding))
	}
	return items
}

func (h *HTTPHandler) lookupEmailAccountForBinding(ctx context.Context, binding MCPBinding) (*vault.EmailAccount, error) {
	if !isEmailMCPServer(binding.ServerName) || h == nil || h.emailAccounts == nil {
		return nil, nil
	}

	accountID, ok := stringFromConfigValue(binding.Config["account_id"])
	if !ok {
		accountID, ok = stringFromConfigValue(binding.Config["account_vault_record_id"])
	}
	if !ok {
		return nil, fmt.Errorf("account_id is not configured")
	}

	return h.emailAccounts.GetEmailAccount(ctx, accountID)
}

func emailAccountSummary(account *vault.EmailAccount) map[string]any {
	if account == nil {
		return nil
	}
	return map[string]any{
		"id":            account.ID,
		"vault_id":      account.VaultID,
		"workspace_id":  account.WorkspaceID,
		"label":         account.Label,
		"provider":      account.Provider,
		"email_address": account.EmailAddress,
		"display_name":  account.DisplayName,
		"auth_type":     account.AuthType,
		"credentials":   account.CredentialsStatus,
		"created_at":    account.CreatedAt,
		"updated_at":    account.UpdatedAt,
	}
}

func isEmailMCPServer(serverName string) bool {
	switch strings.ToLower(strings.TrimSpace(serverName)) {
	case "email", "gmail", "microsoft-mail", "microsoft", "outlook-mail", "imap-smtp", "imap_smtp":
		return true
	default:
		return false
	}
}

func normalizeEmailAllowedActions(actions []string) ([]string, error) {
	seen := make(map[string]struct{}, len(actions))
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		action = strings.ToLower(strings.TrimSpace(action))
		if action == "" {
			continue
		}
		switch action {
		case "read", "search", "draft", "send":
		default:
			return nil, fmt.Errorf("unsupported email allowed_action %q", action)
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		out = append(out, action)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one email allowed_action is required")
	}
	return out, nil
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func boolFromConfigValue(value any) (bool, bool) {
	typed, ok := value.(bool)
	return typed, ok
}
