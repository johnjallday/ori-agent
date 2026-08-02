package workspacerun

import (
	"encoding/json"
	"strings"
	"testing"
)

// Cross-package security and audit regressions for the run half of the feature
// (task 6.14; PRD FR-156–FR-161, FR-167).
//
// The workspace package proves these properties on the authoring side. These
// prove they survive the boundary — a snapshot is the only thing a run sees, so
// a leak or a widening that the authoring tests miss would show up here.

// FR-161: a snapshot names the exact instance and the exact Toolbox version.
// Without both, an audit trail says "the Coder agent used Research Kit" and
// cannot answer which Coder or which Research Kit.
func TestSnapshot_CarriesStableInstanceAndExactVersion(t *testing.T) {
	snapshot := testSnapshot()

	if snapshot.AgentInstanceID == "" {
		t.Fatalf("snapshot has no agent instance ID")
	}
	if snapshot.ToolboxID == "" || snapshot.ToolboxVersion == 0 {
		t.Fatalf("snapshot does not pin an exact Toolbox version: id=%q version=%d",
			snapshot.ToolboxID, snapshot.ToolboxVersion)
	}
	// The workspace version too — it is what makes a later "what changed since"
	// question answerable.
	if snapshot.WorkspaceVersion == 0 {
		t.Fatalf("snapshot does not record the workspace version it was taken from")
	}

	// The name is decoration. Two runs of the same version must be identical
	// regardless of what the Toolbox was called at the time, or the hash stops
	// being a content address and becomes a label.
	renamed := snapshot.Clone()
	renamed.ToolboxName = "Renamed Kit"
	if renamed.ComputeHash() != snapshot.ComputeHash() {
		t.Fatalf("renaming a toolbox changed its content hash — cosmetics reached identity (FR-169)")
	}

	// Changing what it can DO must change the hash.
	widened := snapshot.Clone()
	widened.MCPBindings[0].AllowedTools = append(widened.MCPBindings[0].AllowedTools, "delete_note")
	if widened.ComputeHash() == snapshot.ComputeHash() {
		t.Fatalf("adding an operation left the hash unchanged — two different capabilities share one identity")
	}
}

// FR-157: nothing downstream of a snapshot may widen what it permits. Clone is
// the seam every consumer goes through, so a shared slice here would let a
// caller quietly grant itself another tool.
func TestSnapshot_CloneCannotWidenTheOriginal(t *testing.T) {
	original := testSnapshot()
	clone := original.Clone()

	clone.MCPBindings[0].AllowedTools[0] = "delete_note"
	clone.Skills[0].CapabilityID = "something-else"
	clone.MCPBindings[0].ToolRisks["write_note"] = "read"

	if original.MCPBindings[0].ToolRisks["write_note"] != "write" {
		t.Fatalf("a clone aliased the original's risk classifications — a caller could downgrade a write to a read")
	}
	if original.MCPBindings[0].AllowedTools[0] != "read_note" {
		t.Fatalf("a clone aliased the original's allowed tools")
	}
	if original.Skills[0].CapabilityID != "summarize" {
		t.Fatalf("a clone aliased the original's skills")
	}
	if original.AllowsTool("ws:ws-1:mcp:notes:mb-notes", "delete_note") {
		t.Fatalf("mutating a clone widened what the original permits")
	}
}

// FR-159: an operation with no side-effect classification must not become
// implicitly safe just because it survived into a run.
func TestSnapshot_UnclassifiedOperationIsNotSilentlySafe(t *testing.T) {
	snapshot := testSnapshot()
	// mb-web exposes search_web under a binding default. Strip the default and
	// the tool has no classification at all — which is the state the authoring
	// side blocks on, and which must not quietly become "read" downstream.
	snapshot.MCPBindings[1].DefaultSideEffect = ""

	wrapUp := BuildToolboxWrapUp("run-1", snapshot, []TraceEvent{
		{Kind: TraceToolCall, ToolName: "search_web"},
	}, nil, 1000)

	var searched *WrapUpOperation
	for i := range wrapUp.Operations {
		if wrapUp.Operations[i].Tool == "search_web" {
			searched = &wrapUp.Operations[i]
		}
	}
	if searched == nil {
		t.Fatalf("the wrap-up lost the operation that was actually called")
	}
	if searched.SideEffect == "read" {
		t.Fatalf("an unclassified operation was reported as read-only")
	}
	if searched.SideEffect != "" {
		t.Fatalf("an unclassified operation invented a classification: %q", searched.SideEffect)
	}

	// A classified sibling still reports correctly, so the empty value above is
	// genuinely "unknown" rather than the wrap-up failing to classify anything.
	classified := BuildToolboxWrapUp("run-2", snapshot, []TraceEvent{
		{Kind: TraceToolCall, ToolName: "write_note"},
	}, nil, 1000)
	found := false
	for _, operation := range classified.Operations {
		if operation.Tool == "write_note" {
			found = true
			if operation.SideEffect != "write" {
				t.Fatalf("write_note lost its classification: %q", operation.SideEffect)
			}
		}
	}
	if !found {
		t.Fatalf("the classified control case did not appear in the wrap-up")
	}
}

// FR-158: a snapshot describes a Toolbox. It is not, and must never become, the
// place where native MCP gets switched on — that opt-in lives on the workspace
// and the agent, and both still have to say yes.
func TestSnapshot_DoesNotCarryNativeMCPOptIn(t *testing.T) {
	encoded, err := json.Marshal(testSnapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	lowered := strings.ToLower(string(encoded))

	for _, forbidden := range []string{"native_mcp", "nativemcp", "allow_native", "use_native_mcp"} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("snapshot carries a native-MCP opt-in field (%s) — a Toolbox must not be able to grant it", forbidden)
		}
	}
}

// FR-167: a failed run must not rewrite history. The snapshot a run started
// with stays exactly as it was, so "what was it allowed to do when it broke"
// has an answer.
func TestSnapshot_WrapUpNeverMutatesTheSnapshot(t *testing.T) {
	snapshot := testSnapshot()
	before := snapshot.ComputeHash()

	_ = BuildToolboxWrapUp("run-1", snapshot, []TraceEvent{
		{Kind: TraceToolCall, ToolName: "write_note"},
		{Kind: TraceToolResult, ToolName: "write_note", Status: "error"},
		{Kind: TraceError, Message: "the run failed"},
	}, nil, 4200)

	if snapshot.ComputeHash() != before {
		t.Fatalf("building a wrap-up mutated the snapshot it described")
	}
}
