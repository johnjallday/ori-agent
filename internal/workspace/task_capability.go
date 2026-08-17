package workspace

import (
	"context"
	"errors"
	"strings"
	"time"
)

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

const runtimeFileFallbackApprovalKey = "runtime_file_fallback_approval"

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

var ErrInvalidRuntimeTaskCapability = errors.New("invalid runtime task capability")

// TaskCapabilityValidator rejects an explicitly runtime-scoped key at task
// write time when the workspace contract cannot support it. Unclaimed ordinary
// planning/toolbox keys remain valid.
type TaskCapabilityValidator interface {
	ValidateTaskCapabilities(workspaceID string, capabilities []string) error
}

// TaskCapabilityEvaluator claims and evaluates one normalized capability at a
// time. claimed=false is not success; it means this evaluator has no runtime
// meaning for the key and the composite should ask the next evaluator. This is
// how ordinary planning/toolbox capabilities remain outside execution gating.
type TaskCapabilityEvaluator interface {
	EvaluateTaskCapability(workspaceID, capability string) (claimed bool, blocked *TaskBlockedError)
}

// TaskScopedCapabilityEvaluator can additionally validate the exact assigned
// agent before provider construction. Runtime grants are agent-scoped, while
// account capabilities such as Email can keep using TaskCapabilityEvaluator.
type TaskScopedCapabilityEvaluator interface {
	EvaluateTaskCapabilityForTask(workspaceID string, task Task, capability string) (claimed bool, blocked *TaskBlockedError)
}

// TaskScopedCapabilityGate is the optional richer preflight used by task
// execution. Legacy callers and evaluators retain CheckTaskCapabilities.
type TaskScopedCapabilityGate interface {
	CheckTaskCapabilitiesForTask(workspaceID string, task Task) *TaskBlockedError
}

// CompositeTaskCapabilityGate checks task capabilities in declaration order and
// evaluators in registration order. The first claimed blocker wins, giving the
// UI one exact next action. Keys no evaluator claims preserve legacy behavior.
type CompositeTaskCapabilityGate struct {
	evaluators []TaskCapabilityEvaluator
}

func NewCompositeTaskCapabilityGate(evaluators ...TaskCapabilityEvaluator) *CompositeTaskCapabilityGate {
	gate := &CompositeTaskCapabilityGate{}
	for _, evaluator := range evaluators {
		gate.Register(evaluator)
	}
	return gate
}

// Register appends a compiled evaluator. Nil evaluators are ignored so optional
// server dependencies can be wired without a nil panic.
func (g *CompositeTaskCapabilityGate) Register(evaluator TaskCapabilityEvaluator) {
	if g == nil || evaluator == nil {
		return
	}
	g.evaluators = append(g.evaluators, evaluator)
}

func (g *CompositeTaskCapabilityGate) CheckTaskCapabilities(workspaceID string, capabilities []string) *TaskBlockedError {
	if g == nil {
		return nil
	}
	for _, capability := range NormalizeCapabilityKeys(capabilities) {
		for _, evaluator := range g.evaluators {
			claimed, blocked := evaluator.EvaluateTaskCapability(workspaceID, capability)
			if !claimed {
				continue
			}
			if blocked != nil {
				return blocked
			}
			// One evaluator owns a key. A second evaluator can never reinterpret a
			// successful claim with different domain behavior.
			break
		}
	}
	return nil
}

func (g *CompositeTaskCapabilityGate) CheckTaskCapabilitiesForTask(workspaceID string, task Task) *TaskBlockedError {
	if g == nil {
		return nil
	}
	for _, capability := range NormalizeCapabilityKeys(task.RequiredCapabilities) {
		for _, evaluator := range g.evaluators {
			var claimed bool
			var blocked *TaskBlockedError
			if scoped, ok := evaluator.(TaskScopedCapabilityEvaluator); ok {
				claimed, blocked = scoped.EvaluateTaskCapabilityForTask(workspaceID, task, capability)
			} else {
				claimed, blocked = evaluator.EvaluateTaskCapability(workspaceID, capability)
			}
			if !claimed {
				continue
			}
			if blocked != nil {
				return blocked
			}
			break
		}
	}
	return nil
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
// AllowsFileFallback reports whether task authoring explicitly declared a
// project-file fallback for this required capability. It grants nothing and
// does not mean the user has chosen that path.
func (t *Task) AllowsFileFallback(key string) bool {
	if t == nil {
		return false
	}
	target := NormalizeRuntimeIdentifier(key)
	if target == "" || !t.RequiresCapability(target) {
		return false
	}
	for _, candidate := range t.FileFallbackFor {
		if NormalizeRuntimeIdentifier(candidate) == target {
			return true
		}
	}
	return false
}

// ApproveRuntimeFileFallback records one explicit user decision made through a
// server-validated blocked workflow. The approval is consumed before the next
// provider invocation, so a rerun never silently inherits it.
func (t *Task) ApproveRuntimeFileFallback(capability, blockID string, approvedAt time.Time) bool {
	if t == nil || !t.AllowsFileFallback(capability) || strings.TrimSpace(blockID) == "" {
		return false
	}
	if t.Context == nil {
		t.Context = map[string]any{}
	}
	if approvedAt.IsZero() {
		approvedAt = time.Now().UTC()
	}
	t.Context[runtimeFileFallbackApprovalKey] = map[string]any{
		"capability":  NormalizeRuntimeIdentifier(capability),
		"block_id":    strings.TrimSpace(blockID),
		"approved_at": approvedAt.UTC().Format(time.RFC3339),
	}
	return true
}

// ApprovedRuntimeFileFallback returns the pending exact capability, if any.
func (t *Task) ApprovedRuntimeFileFallback() string {
	if t == nil || t.Context == nil {
		return ""
	}
	raw, ok := t.Context[runtimeFileFallbackApprovalKey]
	if !ok {
		return ""
	}
	var capability, blockID, approvedAt string
	switch value := raw.(type) {
	case map[string]any:
		capability, _ = value["capability"].(string)
		blockID, _ = value["block_id"].(string)
		approvedAt, _ = value["approved_at"].(string)
	case map[string]string:
		capability = value["capability"]
		blockID = value["block_id"]
		approvedAt = value["approved_at"]
	default:
		return ""
	}
	capability = NormalizeRuntimeIdentifier(capability)
	if capability == "" || strings.TrimSpace(blockID) == "" || strings.TrimSpace(approvedAt) == "" || !t.AllowsFileFallback(capability) {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, approvedAt); err != nil {
		return ""
	}
	return capability
}

func (t *Task) ConsumeRuntimeFileFallbackApproval() {
	if t != nil && t.Context != nil {
		delete(t.Context, runtimeFileFallbackApprovalKey)
	}
}

// TaskFileFallbackRun owns a staging directory containing only the
// authoritative project file. Commit promotes that one file after successful
// model execution; Abort removes staging without touching the workspace.
type TaskFileFallbackRun interface {
	PreparedTask() Task
	Commit() error
	Abort()
}

type TaskFileFallbackPreparer interface {
	PrepareTaskFileFallback(context.Context, string, Task, string) (TaskFileFallbackRun, error)
}

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
