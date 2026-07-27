package workspace

import "strings"

// Task-level capability preconditions.
//
// Some tasks cannot do anything useful until an external connection is in
// place: an inbox-triage task with no mailbox linked will burn a model call and
// fail in a way that reads like a model problem. A task declares the abstract
// capabilities it needs — provider-neutral keys, never "gmail" — and execution
// checks them BEFORE starting a run.
//
// The keys are deliberately the same vocabulary as the template system's
// CapabilityRequirement: this package stays domain-blind and only carries the
// key; what "email" requires is decided by the gate implementation.

// CapabilityEmail is the mailbox capability: the workspace must have a healthy,
// linked email account before mail-dependent work can run.
const CapabilityEmail = "email"

// Stable blocked-reason codes for an unmet connection precondition. They are
// machine codes, safe to log and to switch on in the UI, and they never name a
// provider — "email_not_linked", not "gmail_not_linked".
const (
	// BlockedReasonConnectionRequired: the underlying account is not connected
	// at all.
	BlockedReasonConnectionRequired = "connection_required"
	// BlockedReasonCapabilityNotEnabled: the account is connected but this
	// capability was never enabled for it.
	BlockedReasonCapabilityNotEnabled = "capability_not_enabled"
	// BlockedReasonReconnectRequired: the capability was enabled but its
	// authorization is no longer healthy.
	BlockedReasonReconnectRequired = "reconnect_required"
	// BlockedReasonVaultRepairRequired: the credential vault needs an unlock or
	// a selection before the credential can be read.
	BlockedReasonVaultRepairRequired = "vault_repair_required"
	// BlockedReasonNotLinkedToWorkspace: the capability is healthy globally but
	// this workspace has no binding to it.
	BlockedReasonNotLinkedToWorkspace = "not_linked_to_workspace"
	// BlockedReasonAccountUnavailable: the workspace's binding points at an
	// account that is missing or has no usable credential.
	BlockedReasonAccountUnavailable = "account_unavailable"
)

// TaskCapabilityGate reports whether a workspace can satisfy a task's declared
// capability requirements right now. It returns nil when execution may proceed,
// or a TaskBlockedError naming the exact repair.
//
// Implemented outside this package (internal/server) because deciding what
// "email" needs means consulting the account connection, the vault, and the
// workspace binding — none of which the workspace domain knows about.
type TaskCapabilityGate interface {
	CheckTaskCapabilities(workspaceID string, capabilities []string) *TaskBlockedError
}

// NormalizeCapabilityKeys trims, lower-cases, drops blanks, and de-duplicates a
// capability list while preserving order.
func NormalizeCapabilityKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RequiresCapability reports whether a task declares the given capability.
func (t *Task) RequiresCapability(key string) bool {
	if t == nil {
		return false
	}
	target := strings.ToLower(strings.TrimSpace(key))
	for _, have := range t.RequiredCapabilities {
		if strings.ToLower(strings.TrimSpace(have)) == target {
			return true
		}
	}
	return false
}
