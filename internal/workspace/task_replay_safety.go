package workspace

import (
	"sort"
	"strings"
)

// Replay safety: deciding whether a failed attempt may simply be repeated.
//
// Retrying a task is not free of consequences. If the first attempt already
// sent an email, moved a file, or called an external API, a retry does it
// AGAIN — and the second copy is indistinguishable from the first to whoever
// receives it. Worse is the ambiguous case: a mutating tool call that failed
// mid-flight may or may not have taken effect, and nothing in the failure tells
// us which.
//
// So replay is allowed only on positive evidence of safety: either no tool ran
// at all, or every tool that ran was proven read-only. Anything else — a
// mutation, an ambiguous failure, an unclassified tool — blocks for an explicit
// user retry, which is a decision the user is entitled to make with the
// evidence in front of them (FR 55).

// ToolSideEffectClass is the replay-relevant classification of one tool call.
type ToolSideEffectClass string

const (
	// ToolSideEffectRead: observation only. Repeating it changes nothing.
	ToolSideEffectRead ToolSideEffectClass = "read"
	// ToolSideEffectWrite: mutates state, inside or outside the workspace.
	// Repeating it may duplicate the effect.
	ToolSideEffectWrite ToolSideEffectClass = "write"
	// ToolSideEffectConfirm: creates a proposal a human must confirm. Safe to
	// repeat in principle, but it produces a second proposal, so it is treated
	// as a mutation for replay purposes.
	ToolSideEffectConfirm ToolSideEffectClass = "confirm"
	// ToolSideEffectUnknown: unclassified. Treated as unsafe — absence of
	// classification is not evidence of safety.
	ToolSideEffectUnknown ToolSideEffectClass = "unknown"
)

// ToolAttempt is one tool call observed during an execution attempt.
type ToolAttempt struct {
	// Name is the tool's name, normalized to lower case.
	Name string `json:"name"`
	// Class is its replay-relevant side-effect classification.
	Class ToolSideEffectClass `json:"class"`
	// Completed reports whether the call returned a result. A call that was
	// attempted but did not complete is the ambiguous case: for a mutating tool
	// it may have taken effect anyway.
	Completed bool `json:"completed"`
}

// TaskAttemptEvidence is what one execution attempt actually did, in the terms
// replay safety needs.
type TaskAttemptEvidence struct {
	// Attempts are every tool call observed, in name order.
	Attempts []ToolAttempt `json:"attempts,omitempty"`
}

// ReplaySafety is the verdict on whether an attempt may be repeated
// automatically, and why.
type ReplaySafety struct {
	// Safe reports whether an automatic replay is permitted.
	Safe bool
	// Reason is a short, safe explanation for logs and the blocked-task view.
	Reason string
	// UnsafeTools names the tools that made replay unsafe (empty when Safe).
	UnsafeTools []string
}

// EvaluateReplaySafety decides whether the attempt this evidence describes may
// be repeated automatically.
func (e TaskAttemptEvidence) EvaluateReplaySafety() ReplaySafety {
	if len(e.Attempts) == 0 {
		return ReplaySafety{Safe: true, Reason: "no tools ran"}
	}

	unsafe := make([]string, 0, len(e.Attempts))
	seen := make(map[string]struct{}, len(e.Attempts))
	ambiguous := false
	for _, attempt := range e.Attempts {
		switch attempt.Class {
		case ToolSideEffectRead:
			// A read that failed mid-flight still changed nothing.
			continue
		case ToolSideEffectWrite, ToolSideEffectConfirm:
			if !attempt.Completed {
				ambiguous = true
			}
		}
		if _, dup := seen[attempt.Name]; dup {
			continue
		}
		seen[attempt.Name] = struct{}{}
		unsafe = append(unsafe, attempt.Name)
	}

	if len(unsafe) == 0 {
		return ReplaySafety{Safe: true, Reason: "only read-only tools ran"}
	}
	sort.Strings(unsafe)
	reason := "a tool that can change things already ran"
	if ambiguous {
		reason = "a tool that can change things failed partway through, so its effect is unknown"
	}
	return ReplaySafety{Safe: false, Reason: reason, UnsafeTools: unsafe}
}

// readOnlyToolNames are the tools Ori ships that only observe. Naming them
// explicitly — rather than inferring from a verb prefix — keeps a tool from
// becoming replay-safe by accident when someone renames it.
var readOnlyToolNames = map[string]struct{}{
	"read_file":                {},
	"list_directory":           {},
	"list_dir":                 {},
	"directory_tree":           {},
	"search_files":             {},
	"get_file_info":            {},
	"list_allowed_directories": {},
	"web_search":               {},
	"web_fetch":                {},
	"mail_search_threads":      {},
	"mail_get_thread":          {},
	"list_notes":               {},
	"read_note":                {},
	"list_tasks":               {},
	"get_task":                 {},
	"read_memory":              {},
	"list_workspaces":          {},
	"get_workspace":            {},
}

// mutatingToolNames are tools known to change state. The list exists so a
// failed mutation is recognized as ambiguous even when its binding carried no
// side-effect classification.
var mutatingToolNames = map[string]struct{}{
	"write_file":       {},
	"edit_file":        {},
	"move_file":        {},
	"create_directory": {},
	"delete_file":      {},
	"save_note":        {},
	"update_note":      {},
	"create_task":      {},
	"update_task":      {},
	"write_memory":     {},
	"mail_draft_reply": {},
}

// ClassifyToolSideEffect classifies a tool call for replay purposes. The
// binding's own classification wins when it has one — the workspace owner
// declared it, and a mixed-capability server (a filesystem server with both
// read_file and write_file) can only be classified per tool.
//
// With no declaration, a known name decides; everything else is unknown, which
// is the safe answer rather than the convenient one.
func ClassifyToolSideEffect(toolName string, declared SideEffect) ToolSideEffectClass {
	switch declared {
	case SideEffectRead:
		return ToolSideEffectRead
	case SideEffectWrite, SideEffectExternal:
		return ToolSideEffectWrite
	}

	name := strings.ToLower(strings.TrimSpace(toolName))
	// Runtime MCP tools arrive namespaced (e.g. "ws:…:mcp:filesystem:b1__read_file");
	// classify on the bare tool name.
	if idx := strings.LastIndex(name, "__"); idx >= 0 && idx+2 < len(name) {
		name = name[idx+2:]
	}
	if _, ok := readOnlyToolNames[name]; ok {
		return ToolSideEffectRead
	}
	if _, ok := mutatingToolNames[name]; ok {
		if name == "mail_draft_reply" {
			return ToolSideEffectConfirm
		}
		return ToolSideEffectWrite
	}
	return ToolSideEffectUnknown
}
