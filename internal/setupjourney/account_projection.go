package setupjourney

import (
	"strings"
	"unicode"
)

// WorkspaceCreateProjection describes the workspace a host account-link quest
// creates. It is response-only; only the workspace ID is ever persisted, as the
// run's project_workspace_id receipt.
type WorkspaceCreateProjection struct {
	TemplateTitle  string `json:"template_title"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	WorkspaceLabel string `json:"workspace_label,omitempty"`
	WorkspaceRoute string `json:"workspace_route,omitempty"`
}

// AccountConnectProjection is the connection-level account verdict. It is
// response-only and carries the readiness owner's own repair label and route.
type AccountConnectProjection struct {
	Configured    bool   `json:"configured"`
	IdentityEmail string `json:"identity_email,omitempty"`
	GmailHealth   string `json:"gmail_health"`
	ActionLabel   string `json:"action_label,omitempty"`
	ActionURL     string `json:"action_url,omitempty"`
}

// AccountLinkProjection is the workspace-level link verdict and the review
// disclosure's identity. It is response-only and never persisted.
type AccountLinkProjection struct {
	WorkspaceLabel string `json:"workspace_label"`
	AccountEmail   string `json:"account_email,omitempty"`
	Linked         bool   `json:"linked"`
	Ready          bool   `json:"ready"`
}

// Closed connection-health vocabulary for AccountConnectProjection.
const (
	AccountHealthUnconfigured     = "unconfigured"
	AccountHealthNotConnected     = "not_connected"
	AccountHealthNotEnabled       = "not_enabled"
	AccountHealthUnhealthy        = "unhealthy"
	AccountHealthVaultUnavailable = "vault_unavailable"
	AccountHealthHealthy          = "healthy"
)

var validAccountHealth = map[string]struct{}{
	AccountHealthUnconfigured: {}, AccountHealthNotConnected: {}, AccountHealthNotEnabled: {},
	AccountHealthUnhealthy: {}, AccountHealthVaultUnavailable: {}, AccountHealthHealthy: {},
}

const (
	maxAccountProjectionLabelBytes  = 120
	maxAccountProjectionActionBytes = 60
	maxAccountProjectionRouteBytes  = 200
	maxAccountEmailBytes            = 254
)

func validWorkspaceCreateProjection(value *WorkspaceCreateProjection) bool {
	if value == nil {
		return true
	}
	if !safeAccountLabel(value.TemplateTitle, maxAccountProjectionLabelBytes, false) ||
		!safeAccountLabel(value.WorkspaceLabel, maxAccountProjectionLabelBytes, true) ||
		!validateCanonicalRef(value.WorkspaceID, true) {
		return false
	}
	present := value.WorkspaceID != ""
	if (value.WorkspaceLabel != "") != present || (!present && value.WorkspaceRoute != "") {
		return false
	}
	// A route is optional: a slug that would need escaping is omitted and the
	// client resolves the workspace by ID instead.
	return value.WorkspaceRoute == "" || validWorkspaceRoute(value.WorkspaceRoute)
}

func validAccountConnectProjection(value *AccountConnectProjection) bool {
	if value == nil {
		return true
	}
	if _, ok := validAccountHealth[value.GmailHealth]; !ok {
		return false
	}
	if !validAccountEmail(value.IdentityEmail, true) ||
		!safeAccountLabel(value.ActionLabel, maxAccountProjectionActionBytes, true) ||
		!validSettingsRoute(value.ActionURL) {
		return false
	}
	if !value.Configured && value.GmailHealth != AccountHealthUnconfigured {
		return false
	}
	if value.Configured && value.GmailHealth == AccountHealthUnconfigured {
		return false
	}
	return value.GmailHealth != AccountHealthHealthy || value.IdentityEmail != ""
}

func validAccountLinkProjection(value *AccountLinkProjection) bool {
	if value == nil {
		return true
	}
	if !safeAccountLabel(value.WorkspaceLabel, maxAccountProjectionLabelBytes, false) ||
		!validAccountEmail(value.AccountEmail, true) {
		return false
	}
	// A linked account can have vanished from the vault, so Linked alone does
	// not imply a readable address; Ready always does.
	return !value.Ready || (value.Linked && value.AccountEmail != "")
}

func cloneWorkspaceCreateProjection(source *WorkspaceCreateProjection) *WorkspaceCreateProjection {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func cloneAccountConnectProjection(source *AccountConnectProjection) *AccountConnectProjection {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func cloneAccountLinkProjection(source *AccountLinkProjection) *AccountLinkProjection {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func safeAccountLabel(value string, maxBytes int, allowEmpty bool) bool {
	if strings.TrimSpace(value) == "" {
		return allowEmpty && value == ""
	}
	if len(value) > maxBytes {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.In(char, unicode.Cf) {
			return false
		}
	}
	return true
}

func validAccountEmail(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	if len(value) > maxAccountEmailBytes || strings.Count(value, "@") != 1 || strings.ContainsAny(value, "<>\"'\\/") {
		return false
	}
	local, domain, _ := strings.Cut(value, "@")
	if local == "" || domain == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.IsSpace(char) || unicode.In(char, unicode.Cf) {
			return false
		}
	}
	return true
}

// validWorkspaceRoute accepts only a same-origin /workspaces/<slug> path with a
// single escaped segment and no query, fragment, or traversal.
func validWorkspaceRoute(value string) bool {
	slug, ok := strings.CutPrefix(value, "/workspaces/")
	if !ok || slug == "" || len(value) > maxAccountProjectionRouteBytes || strings.Contains(slug, "..") ||
		strings.ContainsAny(slug, "/\\?#%") {
		return false
	}
	return noControlOrSpace(slug)
}

// validSettingsRoute accepts an empty value or a same-origin Settings route.
// Readiness repairs use a fragment and an optional gc_action query token.
func validSettingsRoute(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > maxAccountProjectionRouteBytes || !strings.HasPrefix(value, "/settings") ||
		strings.Contains(value, "//") || strings.ContainsAny(value, "\\:<>\"'") {
		return false
	}
	return noControlOrSpace(value)
}

func noControlOrSpace(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) || unicode.IsSpace(char) || unicode.In(char, unicode.Cf) {
			return false
		}
	}
	return true
}
