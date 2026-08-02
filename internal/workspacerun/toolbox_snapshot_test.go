package workspacerun

import (
	"context"
	"testing"
	"time"
)

// Run snapshot and Wrap-up coverage (tasks 5.15–5.17; PRD FR-107–FR-122).

func testSnapshot() *RunToolboxSnapshot {
	snapshot := &RunToolboxSnapshot{
		WorkspaceID:      "ws-1",
		WorkspaceVersion: 12,
		AgentInstanceID:  "inst-1",
		AgentName:        "Coder",
		ToolboxID:        "tbx-1",
		ToolboxName:      "Research Kit",
		ToolboxVersion:   3,
		Skills: []SnapshotSkill{
			{CapabilityID: "summarize", DisplayName: "summarize", Source: "workspace_provided", BindingID: "sb-1", PromptChars: 800},
			{CapabilityID: "citations", DisplayName: "citations", Source: "agent_learned", PromptChars: 400},
		},
		MCPBindings: []SnapshotMCP{
			{
				BindingID: "mb-notes", ServerName: "notes", Alias: "Notes",
				RuntimeServerName: "ws:ws-1:mcp:notes:mb-notes",
				AllowedTools:      []string{"read_note", "write_note"},
				DefaultSideEffect: "read",
				ToolRisks:         map[string]string{"write_note": "write"},
			},
			{
				BindingID: "mb-web", ServerName: "web", Alias: "Web",
				RuntimeServerName: "ws:ws-1:mcp:web:mb-web",
				AllowedTools:      []string{"search_web"},
				DefaultSideEffect: "read",
			},
		},
		FocusState:     "Focused",
		SkillSpaces:    2,
		AutonomyPolicy: "propose",
		CreatedAt:      time.Now(),
	}
	snapshot.Hash = snapshot.ComputeHash()
	return snapshot
}

// FR-112: a run may call exactly what its snapshot names, and nothing else.
// An unknown server or an unlisted tool answers false — the default is refusal.
func TestSnapshot_AllowsOnlyWhatItNames(t *testing.T) {
	snapshot := testSnapshot()

	if !snapshot.AllowsTool("ws:ws-1:mcp:notes:mb-notes", "read_note") {
		t.Fatalf("expected a snapshotted tool to be allowed")
	}
	if !snapshot.AllowsTool("ws:ws-1:mcp:notes:mb-notes", "WRITE_NOTE") {
		t.Fatalf("expected tool matching to be case-insensitive")
	}
	if snapshot.AllowsTool("ws:ws-1:mcp:notes:mb-notes", "delete_note") {
		t.Fatalf("expected an unlisted tool to be refused")
	}
	if snapshot.AllowsTool("ws:ws-1:mcp:tracker:mb-new", "anything") {
		t.Fatalf("expected a server absent from the snapshot to be refused")
	}
	if (*RunToolboxSnapshot)(nil).AllowsTool("anything", "anything") {
		t.Fatalf("expected a missing snapshot to refuse, never to permit")
	}
}

// The hash answers "were these two runs given the same capabilities?" — so it
// ignores identity and ordering, and changes when the capabilities do.
func TestSnapshot_HashIsStableAndContentAddressed(t *testing.T) {
	first := testSnapshot()
	second := testSnapshot()
	// Same capabilities, different run identity and assembly order.
	second.AgentInstanceID = "inst-2"
	second.MCPBindings[0], second.MCPBindings[1] = second.MCPBindings[1], second.MCPBindings[0]
	second.MCPBindings[1].AllowedTools = []string{"write_note", "read_note"}

	if first.ComputeHash() != second.ComputeHash() {
		t.Fatalf("expected identical capabilities to hash identically")
	}

	changed := testSnapshot()
	changed.MCPBindings[0].AllowedTools = []string{"read_note"}
	if changed.ComputeHash() == first.ComputeHash() {
		t.Fatalf("expected a narrowed tool list to change the hash")
	}
}

// FR-110: the runtime builds its tool list from the snapshot, so a workspace
// edit during a run cannot reach it.
func TestSnapshot_ToolAllowlistIsSelfContained(t *testing.T) {
	snapshot := testSnapshot()
	allowlist := snapshot.ToolAllowlist()

	if len(allowlist["ws:ws-1:mcp:notes:mb-notes"]) != 2 {
		t.Fatalf("expected the notes binding's two operations, got %v", allowlist)
	}
	// Mutating the returned list must not reach back into the snapshot.
	allowlist["ws:ws-1:mcp:notes:mb-notes"][0] = "mutated"
	if snapshot.MCPBindings[0].AllowedTools[0] != "read_note" {
		t.Fatalf("expected the snapshot to be insulated from its callers")
	}
}

// FR-111: a delegated subtask may use less than its parent, never more.
func TestSnapshot_BoundedByParentNarrowsAndNeverWidens(t *testing.T) {
	parent := testSnapshot()
	parent.MCPBindings[0].AllowedTools = []string{"read_note"}
	parent.Skills = parent.Skills[:1]

	child := testSnapshot()
	// The child's own toolbox is wider than the parent's on every axis.
	bounded := child.BoundedBy(parent)

	if len(bounded.Skills) != 1 || bounded.Skills[0].CapabilityID != "summarize" {
		t.Fatalf("expected the child's skills to be capped by the parent, got %+v", bounded.Skills)
	}
	notes := bounded.MCPBindings[0]
	if len(notes.AllowedTools) != 1 || notes.AllowedTools[0] != "read_note" {
		t.Fatalf("expected write_note to be dropped by the parent boundary, got %v", notes.AllowedTools)
	}
	if bounded.AllowsTool("ws:ws-1:mcp:notes:mb-notes", "write_note") {
		t.Fatalf("expected a bounded snapshot to refuse what the parent could not do")
	}
	// The hash reflects the narrowed reality.
	if bounded.Hash == child.Hash {
		t.Fatalf("expected bounding to change the content hash")
	}
}

func TestSnapshot_BoundedByDropsBindingsTheParentLacked(t *testing.T) {
	parent := testSnapshot()
	parent.MCPBindings = parent.MCPBindings[:1] // notes only

	bounded := testSnapshot().BoundedBy(parent)

	if len(bounded.MCPBindings) != 1 || bounded.MCPBindings[0].BindingID != "mb-notes" {
		t.Fatalf("expected the binding the parent lacked to be dropped, got %+v", bounded.MCPBindings)
	}
}

// --- Wrap-up (FR-114–FR-120) ---

func toolCall(sequence int64, tool string) TraceEvent {
	return TraceEvent{Sequence: sequence, Kind: TraceToolCall, ToolName: tool}
}

func TestWrapUp_CountsMeasuredOperations(t *testing.T) {
	snapshot := testSnapshot()
	trace := []TraceEvent{
		toolCall(1, "read_note"),
		toolCall(2, "read_note"),
		toolCall(3, "search_web"),
		{Sequence: 4, Kind: TraceToolResult, ToolName: "search_web", Status: "error"},
	}

	wrapUp := BuildToolboxWrapUp("run-1", snapshot, trace, &CostSummary{TotalTokens: 1200, USD: 0.04}, 5000)

	if wrapUp.TotalToolCalls != 3 {
		t.Fatalf("expected 3 tool calls, got %d", wrapUp.TotalToolCalls)
	}
	if wrapUp.Operations[0].Tool != "read_note" || wrapUp.Operations[0].Calls != 2 {
		t.Fatalf("expected read_note to lead with 2 calls, got %+v", wrapUp.Operations)
	}
	// The binding is resolved through the snapshot, not guessed from the name.
	if wrapUp.Operations[0].Binding != "mb-notes" || wrapUp.Operations[0].Server != "Notes" {
		t.Fatalf("expected the operation to be attributed to its binding, got %+v", wrapUp.Operations[0])
	}
	if wrapUp.Operations[0].SideEffect != "read" {
		t.Fatalf("expected the declared side effect, got %q", wrapUp.Operations[0].SideEffect)
	}
	if wrapUp.TokensUsed != 1200 || wrapUp.CostUSD != 0.04 || wrapUp.DurationMs != 5000 {
		t.Fatalf("expected the reported cost and latency to be carried, got %+v", wrapUp)
	}
	if wrapUp.SnapshotHash != snapshot.Hash {
		t.Fatalf("expected the report to be tied to the snapshot it measured")
	}
}

// FR-116: an allowlisted operation that was never called IS unused — that part
// is concrete.
func TestWrapUp_ReportsUnusedOperations(t *testing.T) {
	wrapUp := BuildToolboxWrapUp("run-1", testSnapshot(), []TraceEvent{toolCall(1, "read_note")}, nil, 0)

	if len(wrapUp.UnusedOperations) != 2 {
		t.Fatalf("expected write_note and search_web to be unused, got %v", wrapUp.UnusedOperations)
	}
}

// FR-116/FR-117: a prompt-only skill is NEVER reported as unused. Nothing
// observes whether the model followed it, and saying otherwise would lead a
// user to delete something that was working.
func TestWrapUp_NeverClaimsASkillWentUnused(t *testing.T) {
	wrapUp := BuildToolboxWrapUp("run-1", testSnapshot(), []TraceEvent{toolCall(1, "read_note")}, nil, 0)

	if len(wrapUp.SkillObservations) != 2 {
		t.Fatalf("expected an observation per active skill, got %+v", wrapUp.SkillObservations)
	}
	for _, observation := range wrapUp.SkillObservations {
		if observation.Evidence != WrapUpEvidenceNone {
			t.Fatalf("expected no direct evidence for a prompt-only skill, got %q", observation.Evidence)
		}
		if observation.Note == "" {
			t.Fatalf("expected the absence of evidence to be explained")
		}
		// The context cost IS measurable, and is the honest basis for a
		// "consider removing" conversation.
		if observation.PromptChars == 0 {
			t.Fatalf("expected the measurable context cost to be reported")
		}
	}
	for _, unused := range wrapUp.UnusedOperations {
		if unused == "summarize" || unused == "citations" {
			t.Fatalf("a skill must never appear in the unused-OPERATIONS list")
		}
	}
}

// A skill whose name matches an invoked tool does have evidence, and it is
// labeled as measured rather than inferred.
func TestWrapUp_LabelsMeasuredSkillEvidence(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.MCPBindings[0].AllowedTools = append(snapshot.MCPBindings[0].AllowedTools, "summarize")

	wrapUp := BuildToolboxWrapUp("run-1", snapshot, []TraceEvent{toolCall(1, "summarize")}, nil, 0)

	for _, observation := range wrapUp.SkillObservations {
		if observation.CapabilityID != "summarize" {
			continue
		}
		if observation.Evidence != WrapUpEvidenceMeasured {
			t.Fatalf("expected measured evidence for an invoked tool, got %q", observation.Evidence)
		}
	}
}

// FR-115: blocked calls, retries, approvals, and connection failures are all
// counted from the trace.
func TestWrapUp_CountsBlocksRetriesApprovalsAndFailures(t *testing.T) {
	trace := []TraceEvent{
		toolCall(1, "read_note"),
		{Sequence: 2, Kind: TraceToolResult, ToolName: "read_note", Status: "blocked"},
		{Sequence: 3, Kind: TraceError, Message: "transient failure, will retry"},
		{Sequence: 4, Kind: TraceMessage, Message: "waiting for approval to continue"},
		{Sequence: 5, Kind: TraceError, Message: "connection lost", Data: map[string]any{"binding_id": "mb-web"}},
	}

	wrapUp := BuildToolboxWrapUp("run-1", testSnapshot(), trace, nil, 0)

	if wrapUp.BlockedCalls != 1 || wrapUp.Retries != 1 || wrapUp.ApprovalRequests != 1 {
		t.Fatalf("expected 1 block, 1 retry, 1 approval, got %+v", wrapUp)
	}
	if len(wrapUp.ConnectionFailures) != 1 || wrapUp.ConnectionFailures[0] != "mb-web" {
		t.Fatalf("expected the failing binding to be named, got %v", wrapUp.ConnectionFailures)
	}
}

// FR-118: suggestions cite the evidence behind them.
func TestWrapUp_SuggestionsCiteEvidence(t *testing.T) {
	trace := []TraceEvent{
		toolCall(1, "read_note"),
		{Sequence: 2, Kind: TraceToolResult, ToolName: "read_note", Status: "blocked"},
	}

	wrapUp := BuildToolboxWrapUp("run-1", testSnapshot(), trace, nil, 0)

	kinds := make(map[string]WrapUpSuggestion, len(wrapUp.Suggestions))
	for _, suggestion := range wrapUp.Suggestions {
		if suggestion.Evidence == "" {
			t.Fatalf("every suggestion must cite what was observed, got %+v", suggestion)
		}
		kinds[suggestion.Kind] = suggestion
	}
	if _, ok := kinds[SuggestRemoveUnusedOperations]; !ok {
		t.Fatalf("expected an unused-operations suggestion, got %+v", wrapUp.Suggestions)
	}
	if _, ok := kinds[SuggestAddCapability]; !ok {
		t.Fatalf("expected a blocked call to suggest a possible gap, got %+v", wrapUp.Suggestions)
	}
	// The variant is offered only when there is something concrete to change.
	if _, ok := kinds[SuggestSaveVariant]; !ok {
		t.Fatalf("expected a variant suggestion alongside real findings")
	}
}

// A run that used everything it had gets no suggestions — an empty report is
// the right answer, not a manufactured one.
func TestWrapUp_NoSuggestionsWhenNothingToImprove(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.MCPBindings[0].AllowedTools = []string{"read_note"}
	snapshot.MCPBindings = snapshot.MCPBindings[:1]

	wrapUp := BuildToolboxWrapUp("run-1", snapshot, []TraceEvent{toolCall(1, "read_note")}, nil, 0)

	if len(wrapUp.Suggestions) != 0 {
		t.Fatalf("expected no suggestions for a fully-used toolbox, got %+v", wrapUp.Suggestions)
	}
}

// FR-121: the report carries no secrets — only identities, counts, and
// classifications.
func TestWrapUp_StoresNoSecrets(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.MCPBindings[0].Scope = map[string]any{"roots": []string{"/tmp/notes"}}

	wrapUp := BuildToolboxWrapUp("run-1", snapshot, []TraceEvent{toolCall(1, "read_note")}, nil, 0)

	// The wrap-up records what was called, not what was said or configured.
	for _, operation := range wrapUp.Operations {
		if operation.Tool == "" {
			t.Fatalf("expected a named operation")
		}
	}
	if wrapUp.SnapshotHash == "" {
		t.Fatalf("expected the report to reference the snapshot by hash rather than copying it")
	}
}

// A run with no snapshot produces no wrap-up: there is nothing to measure
// against, and inventing one would misreport a historical run.
func TestWrapUp_NilSnapshotProducesNoReport(t *testing.T) {
	if wrapUp := BuildToolboxWrapUp("run-1", nil, []TraceEvent{toolCall(1, "read_note")}, nil, 0); wrapUp != nil {
		t.Fatalf("expected no wrap-up without a snapshot, got %+v", wrapUp)
	}
}

// --- Persistence (FR-107, FR-114) ---

func TestMemoryStore_SnapshotAndWrapUpRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	run := &Run{ID: "run-1", WorkspaceID: "ws-1", Prompt: "do the thing"}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	snapshot := testSnapshot()
	if err := store.SetToolboxSnapshot(ctx, "ws-1", "run-1", *snapshot); err != nil {
		t.Fatalf("SetToolboxSnapshot() error = %v", err)
	}
	wrapUp := BuildToolboxWrapUp("run-1", snapshot, []TraceEvent{toolCall(1, "read_note")}, nil, 0)
	if err := store.SetToolboxWrapUp(ctx, "ws-1", "run-1", *wrapUp); err != nil {
		t.Fatalf("SetToolboxWrapUp() error = %v", err)
	}

	stored, err := store.GetRun(ctx, "ws-1", "run-1")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if stored.ToolboxSnapshot == nil || stored.ToolboxSnapshot.Hash != snapshot.Hash {
		t.Fatalf("expected the snapshot to survive, got %+v", stored.ToolboxSnapshot)
	}
	if stored.ToolboxWrapUp == nil || stored.ToolboxWrapUp.TotalToolCalls != 1 {
		t.Fatalf("expected the wrap-up to survive, got %+v", stored.ToolboxWrapUp)
	}

	// FR-110: a stored snapshot is immutable — a caller mutating what it got
	// back must not reach the record.
	stored.ToolboxSnapshot.MCPBindings[0].AllowedTools[0] = "mutated"
	reread, err := store.GetRun(ctx, "ws-1", "run-1")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if reread.ToolboxSnapshot.MCPBindings[0].AllowedTools[0] != "read_note" {
		t.Fatalf("expected the stored snapshot to be insulated from callers")
	}
}

// Runs that predate snapshots read back as nil, not as an empty snapshot that
// would misreport them as having had no capabilities.
func TestMemoryStore_HistoricalRunHasNoSnapshot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.CreateRun(ctx, &Run{ID: "run-old", WorkspaceID: "ws-1", Prompt: "legacy"}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	stored, err := store.GetRun(ctx, "ws-1", "run-old")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if stored.ToolboxSnapshot != nil || stored.ToolboxWrapUp != nil {
		t.Fatalf("expected a historical run to carry neither, got %+v", stored)
	}
}
