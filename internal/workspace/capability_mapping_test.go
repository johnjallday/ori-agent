package workspace

import "testing"

func TestNormalizeCapabilityMappings_TrimsNormalizesAndDrops(t *testing.T) {
	in := []CapabilityMapping{
		{
			Capability: "  Calendar ",
			Operations: map[string]OperationMapping{
				" List_Events ": {Tool: "  events_list  ", ResultCollection: " /items ", Fields: map[string]string{" Title ": " /summary "}},
				"blank_tool":    {Tool: "   "}, // dropped: blank tool
			},
		},
		{Capability: "  ", Operations: map[string]OperationMapping{"x": {Tool: "y"}}}, // dropped: blank capability key
	}

	out := NormalizeCapabilityMappings(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 mapping to survive, got %d: %+v", len(out), out)
	}
	if out[0].Capability != "calendar" {
		t.Fatalf("expected lower-cased trimmed capability, got %q", out[0].Capability)
	}
	op, ok := out[0].Operations["list_events"]
	if !ok {
		t.Fatalf("expected operation key to be normalized to 'list_events', got: %+v", out[0].Operations)
	}
	if op.Tool != "events_list" || op.ResultCollection != "/items" {
		t.Fatalf("expected trimmed tool/result_collection, got: %+v", op)
	}
	if op.Fields["title"] != "/summary" {
		t.Fatalf("expected trimmed/lower-cased field key, got: %+v", op.Fields)
	}
	if _, present := out[0].Operations["blank_tool"]; present {
		t.Fatal("expected an operation with a blank tool name to be dropped")
	}
}

func TestNormalizeCapabilityMappings_MergesDuplicateCapabilityKeys(t *testing.T) {
	in := []CapabilityMapping{
		{Capability: "calendar", Operations: map[string]OperationMapping{"list_calendars": {Tool: "a"}}},
		{Capability: "CALENDAR", Operations: map[string]OperationMapping{"list_events": {Tool: "b"}}},
	}
	out := NormalizeCapabilityMappings(in)
	if len(out) != 1 {
		t.Fatalf("expected duplicate capability keys to merge into one, got %d", len(out))
	}
	if len(out[0].Operations) != 2 {
		t.Fatalf("expected operations from both entries to merge, got: %+v", out[0].Operations)
	}
}

func TestNormalizeCapabilityMappings_EmptyInputYieldsNil(t *testing.T) {
	if out := NormalizeCapabilityMappings(nil); out != nil {
		t.Fatalf("expected nil for nil input, got: %+v", out)
	}
	if out := NormalizeCapabilityMappings([]CapabilityMapping{}); out != nil {
		t.Fatalf("expected nil for empty input, got: %+v", out)
	}
}

func TestCloneCapabilityMappings_DeepCopiesNestedMaps(t *testing.T) {
	original := []CapabilityMapping{
		{
			Capability: "calendar",
			Operations: map[string]OperationMapping{
				"list_events": {
					Tool:      "events_list",
					Arguments: map[string]string{"calendar_id": "/calendarId"},
					Fields:    map[string]string{"title": "/summary"},
				},
			},
		},
	}

	clone := CloneCapabilityMappings(original)

	// Mutate the clone's nested maps and confirm the original is untouched.
	clone[0].Operations["list_events"].Arguments["calendar_id"] = "/mutated"
	clonedOp := clone[0].Operations["list_events"]
	clonedOp.Fields["title"] = "/mutated"
	clone[0].Operations["list_events"] = clonedOp

	origOp := original[0].Operations["list_events"]
	if origOp.Arguments["calendar_id"] != "/calendarId" {
		t.Fatalf("mutating the clone's Arguments map leaked into the original: %+v", origOp.Arguments)
	}
	if origOp.Fields["title"] != "/summary" {
		t.Fatalf("mutating the clone's Fields map leaked into the original: %+v", origOp.Fields)
	}
}

func TestCloneCapabilityMappings_NilInputYieldsNil(t *testing.T) {
	if out := CloneCapabilityMappings(nil); out != nil {
		t.Fatalf("expected nil, got: %+v", out)
	}
}

func TestMCPBinding_AllowsAllToolsAndToolAllowed(t *testing.T) {
	nilAllowlist := MCPBinding{}
	if !nilAllowlist.AllowsAllTools() {
		t.Fatal("expected a nil AllowedTools to allow all tools (legacy behavior)")
	}
	if !nilAllowlist.ToolAllowed("anything") {
		t.Fatal("expected ToolAllowed to be true for any tool when AllowedTools is nil")
	}

	restricted := MCPBinding{AllowedTools: []string{"list_events", "List_Calendars"}}
	if restricted.AllowsAllTools() {
		t.Fatal("expected a non-nil AllowedTools to restrict tools")
	}
	if !restricted.ToolAllowed("list_events") {
		t.Fatal("expected an exact allowlisted tool to be allowed")
	}
	if !restricted.ToolAllowed("  list_calendars ") {
		t.Fatal("expected case-insensitive/trimmed matching")
	}
	if restricted.ToolAllowed("create_event") {
		t.Fatal("expected a non-allowlisted tool to be denied")
	}

	explicitEmpty := MCPBinding{AllowedTools: []string{}}
	if explicitEmpty.AllowsAllTools() {
		t.Fatal("expected an explicit empty AllowedTools to mean 'no tools allowed', not 'all tools'")
	}
	if explicitEmpty.ToolAllowed("anything") {
		t.Fatal("expected an explicit empty AllowedTools to deny every tool")
	}
}

func TestMCPBinding_FindCapabilityMapping(t *testing.T) {
	b := MCPBinding{CapabilityMappings: []CapabilityMapping{
		{Capability: "calendar", Operations: map[string]OperationMapping{"list_events": {Tool: "events_list"}}},
	}}
	mapping, ok := b.FindCapabilityMapping("Calendar")
	if !ok {
		t.Fatal("expected case-insensitive capability lookup to find the mapping")
	}
	if _, opOK := mapping.Operation("LIST_EVENTS"); !opOK {
		t.Fatal("expected case-insensitive operation lookup")
	}
	if _, ok := b.FindCapabilityMapping("email"); ok {
		t.Fatal("expected no match for an unmapped capability")
	}
}

// --- Workspace-level deep-copy discipline (task 2.3) ------------------------

func TestGetMCPBinding_DeepCopiesAllowedToolsAndCapabilityMappings(t *testing.T) {
	ws := &Workspace{}
	original := MCPBinding{
		ID:                 "b1",
		ServerName:         "gcal",
		AllowedTools:       []string{"list_events"},
		CapabilityMappings: []CapabilityMapping{{Capability: "calendar", Operations: map[string]OperationMapping{"list_events": {Tool: "events_list"}}}},
	}
	if err := ws.UpsertMCPBinding(original); err != nil {
		t.Fatalf("UpsertMCPBinding error: %v", err)
	}

	got, ok := ws.GetMCPBinding("b1")
	if !ok {
		t.Fatal("expected binding to exist")
	}
	got.AllowedTools[0] = "mutated"
	got.CapabilityMappings[0].Operations["list_events"] = OperationMapping{Tool: "mutated"}

	again, ok := ws.GetMCPBinding("b1")
	if !ok {
		t.Fatal("expected binding to exist")
	}
	if again.AllowedTools[0] != "list_events" {
		t.Fatalf("mutating a GetMCPBinding result leaked into workspace state: %+v", again.AllowedTools)
	}
	if again.CapabilityMappings[0].Operations["list_events"].Tool != "events_list" {
		t.Fatalf("mutating a GetMCPBinding result's CapabilityMappings leaked into workspace state: %+v", again.CapabilityMappings)
	}
}

func TestGetMCPBindings_DeepCopiesEachElement(t *testing.T) {
	ws := &Workspace{}
	if err := ws.UpsertMCPBinding(MCPBinding{
		ID:           "b1",
		ServerName:   "gcal",
		AllowedTools: []string{"list_events"},
	}); err != nil {
		t.Fatalf("UpsertMCPBinding error: %v", err)
	}

	bindings := ws.GetMCPBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	bindings[0].AllowedTools[0] = "mutated"

	again := ws.GetMCPBindings()
	if again[0].AllowedTools[0] != "list_events" {
		t.Fatalf("mutating a GetMCPBindings result leaked into workspace state: %+v", again[0].AllowedTools)
	}
}

func TestUpsertMCPBinding_DoesNotAliasCallerSlices(t *testing.T) {
	ws := &Workspace{}
	tools := []string{"list_events"}
	if err := ws.UpsertMCPBinding(MCPBinding{ID: "b1", ServerName: "gcal", AllowedTools: tools}); err != nil {
		t.Fatalf("UpsertMCPBinding error: %v", err)
	}

	tools[0] = "mutated-by-caller"

	got, ok := ws.GetMCPBinding("b1")
	if !ok {
		t.Fatal("expected binding to exist")
	}
	if got.AllowedTools[0] != "list_events" {
		t.Fatalf("mutating the caller's original slice after Upsert leaked into workspace state: %+v", got.AllowedTools)
	}
}

func TestUpsertMCPBinding_LegacyBindingWithNoNewFieldsRoundTrips(t *testing.T) {
	// A binding authored before AllowedTools/CapabilityMappings existed must
	// keep working exactly as before: nil in, nil out, all-tools-allowed.
	ws := &Workspace{}
	if err := ws.UpsertMCPBinding(MCPBinding{ID: "legacy", ServerName: "filesystem"}); err != nil {
		t.Fatalf("UpsertMCPBinding error: %v", err)
	}
	got, ok := ws.GetMCPBinding("legacy")
	if !ok {
		t.Fatal("expected binding to exist")
	}
	if got.AllowedTools != nil {
		t.Fatalf("expected AllowedTools to stay nil for a legacy binding, got: %+v", got.AllowedTools)
	}
	if !got.AllowsAllTools() {
		t.Fatal("expected a legacy binding to allow all tools")
	}
	if got.CapabilityMappings != nil {
		t.Fatalf("expected CapabilityMappings to stay nil for a legacy binding, got: %+v", got.CapabilityMappings)
	}
}
