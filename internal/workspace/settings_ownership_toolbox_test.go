package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// toolboxWorkspace adds a workspace whose two Writer-like instances pin
// different Toolbox versions, plus an unassigned, a broken and an empty one.
func toolboxWorkspace(t *testing.T, f *ownershipFixture) {
	t.Helper()
	now := time.Now()
	research := ToolboxDefinition{
		ID: "tb-research", WorkspaceID: "ws-tools", Name: "Research", Version: 2, Status: ToolboxStatusActive,
		Skills: []ToolboxSkillRef{{CapabilityID: "summarize", DisplayName: "Summarize", Source: ToolboxSourceAgentLearned}},
		History: []ToolboxRecipe{
			{Version: 1, CreatedAt: now,
				Skills: []ToolboxSkillRef{
					{CapabilityID: "summarize", DisplayName: "Summarize", Source: ToolboxSourceAgentLearned},
					{CapabilityID: "web-notes", DisplayName: "Web Notes", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-notes", Required: true},
					{CapabilityID: "gone", DisplayName: "Gone Skill", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-missing"},
				},
				MCPBindings: []ToolboxMCPRef{
					{BindingID: "mcp-github", AllowedTools: []string{"list_issues", "create_issue"}},
					{BindingID: "mcp-off", AllowedTools: []string{"read"}},
					{BindingID: "mcp-wide", InheritsBindingTools: true},
					{BindingID: "mcp-missing", AllowedTools: []string{"x"}},
				}},
			{Version: 2, CreatedAt: now,
				Skills: []ToolboxSkillRef{{CapabilityID: "summarize", DisplayName: "Summarize", Source: ToolboxSourceAgentLearned}}},
		},
	}
	empty := ToolboxDefinition{
		ID: "tb-empty", WorkspaceID: "ws-tools", Name: "Nothing", Version: 1, Status: ToolboxStatusActive,
		History: []ToolboxRecipe{{Version: 1, CreatedAt: now}},
	}
	ws := &Workspace{
		ID: "ws-tools", Name: "Tools", FolderSlug: "tools", Status: StatusActive, CreatedAt: now, UpdatedAt: now,
		AgentInstances: []AgentInstance{
			{ID: "i-v1", Name: "Writer", InstanceNumber: 1},
			{ID: "i-v2", Name: "Writer", InstanceNumber: 2},
			{ID: "i-none", Name: "Writer", InstanceNumber: 3},
			{ID: "i-broken", Name: "Writer", InstanceNumber: 4},
			{ID: "i-empty", Name: "Writer", InstanceNumber: 5},
		},
		Toolboxes: []ToolboxDefinition{research, empty},
		ToolboxAssignments: []AgentToolboxAssignment{
			{AgentInstanceID: "i-v1", ToolboxID: "tb-research", ToolboxVersion: 1},
			{AgentInstanceID: "i-v2", ToolboxID: "tb-research", ToolboxVersion: 2},
			{AgentInstanceID: "i-broken", ToolboxID: "tb-research", ToolboxVersion: 9},
			{AgentInstanceID: "i-empty", ToolboxID: "tb-empty", ToolboxVersion: 1},
		},
		SkillBindings: []SkillBinding{{ID: "sb-notes", SkillName: "Web Notes", Enabled: false}},
		MCPBindings: []MCPBinding{
			{ID: "mcp-github", ServerName: "github", Alias: "GitHub", Enabled: true, AllowedTools: []string{"list_issues"},
				Scope: map[string]any{"repo": "acme/site"}},
			{ID: "mcp-off", ServerName: "reader", Enabled: false},
			{ID: "mcp-wide", ServerName: "wide", Enabled: true},
		},
		AllowNativeMCPCLI: true,
	}
	if err := f.store.Store.Save(ws); err != nil {
		t.Fatal(err)
	}
}

func toolboxProject(t *testing.T, f *ownershipFixture, instance string, inventory func(string) ([]string, error)) SettingsOwnership {
	t.Helper()
	src := f.sources()
	src.SkillInventory = inventory
	out, err := ProjectSettingsOwnership(src, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-tools", InstanceID: instance})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func inventoryOf(names ...string) func(string) ([]string, error) {
	return func(string) ([]string, error) { return names, nil }
}

func skillByID(t *testing.T, skills []OwnershipToolboxSkill, id string) OwnershipToolboxSkill {
	t.Helper()
	for _, s := range skills {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("skill %s missing from %+v", id, skills)
	return OwnershipToolboxSkill{}
}

func TestSettingsOwnershipDirectToolboxStates(t *testing.T) {
	f := newOwnershipFixture(t)
	lib := f.library["Writer"].Definition

	if out := project(t, f, SettingsOwnershipRequest{Agent: "Writer"}); out.DirectToolbox.State != OwnershipToolboxLegacy ||
		!strings.Contains(out.DirectToolbox.Note, "every skill enabled") {
		t.Fatalf("legacy direct toolbox = %+v", out.DirectToolbox)
	}

	lib.DefaultToolbox = &types.AgentDefaultToolbox{Version: 3}
	if out := project(t, f, SettingsOwnershipRequest{Agent: "Writer"}); out.DirectToolbox.State != OwnershipToolboxEmpty {
		t.Fatalf("explicit empty = %+v", out.DirectToolbox)
	}

	lib.DefaultToolbox.Skills = []types.DefaultToolboxSkillRef{{CapabilityID: "summarize", DisplayName: "Summarize"}, {CapabilityID: "lost", DisplayName: "Lost"}}
	src := f.sources()
	src.SkillInventory = inventoryOf("Summarize", "Other")
	out, _ := ProjectSettingsOwnership(src, SettingsOwnershipRequest{Agent: "Writer"})
	if out.DirectToolbox.State != OwnershipToolboxSelected || out.DirectToolbox.Version != 3 || out.DirectToolbox.Inventory != "checked" {
		t.Fatalf("selected = %+v", out.DirectToolbox)
	}
	if skillByID(t, out.DirectToolbox.Skills, "summarize").Availability != OwnershipAvailable ||
		skillByID(t, out.DirectToolbox.Skills, "lost").Availability != OwnershipMissing {
		t.Fatalf("availability = %+v", out.DirectToolbox.Skills)
	}
	// The collection is not the selection: "Other" is collected, not selected.
	if len(out.DirectToolbox.Skills) != 2 {
		t.Fatalf("collection leaked into the selection: %+v", out.DirectToolbox.Skills)
	}

	// A failed inventory read is unknown, never available or missing.
	src.SkillInventory = func(string) ([]string, error) { return nil, errors.New("disk") }
	out, _ = ProjectSettingsOwnership(src, SettingsOwnershipRequest{Agent: "Writer"})
	if out.DirectToolbox.Inventory != "unavailable" || skillByID(t, out.DirectToolbox.Skills, "summarize").Availability != OwnershipUnknown {
		t.Fatalf("failed inventory = %+v", out.DirectToolbox)
	}
	// No inventory reader at all is also unknown (unlike the old endpoint's
	// "available when nothing to check against").
	out = project(t, f, SettingsOwnershipRequest{Agent: "Writer"})
	if skillByID(t, out.DirectToolbox.Skills, "summarize").Availability != OwnershipUnknown {
		t.Fatalf("no inventory = %+v", out.DirectToolbox)
	}

	// A workspace-owned agent has no Default Toolbox; a workspace context uses
	// its pinned Toolbox instead.
	if scout := project(t, f, SettingsOwnershipRequest{Agent: "Scout"}); scout.DirectToolbox.State != OwnershipToolboxNotApplicable {
		t.Fatalf("workspace-owned direct toolbox = %+v", scout.DirectToolbox)
	}
	if ws := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-tracked"}); ws.DirectToolbox.State != OwnershipToolboxNotApplicable ||
		!strings.Contains(ws.DirectToolbox.Note, "outside workspaces") {
		t.Fatalf("workspace context direct toolbox = %+v", ws.DirectToolbox)
	}
}

func TestSettingsOwnershipWorkspaceToolboxPinsAndRestrictions(t *testing.T) {
	f := newOwnershipFixture(t)
	toolboxWorkspace(t, f)

	v1 := toolboxProject(t, f, "i-v1", inventoryOf("Summarize"))
	tb := v1.WorkspaceToolbox
	if tb.State != OwnershipToolboxPinned || tb.ToolboxName != "Research" || tb.PinnedVersion != 1 || tb.CurrentVersion != 2 {
		t.Fatalf("pinned v1 = %+v", tb)
	}
	if skillByID(t, tb.Skills, "summarize").Availability != OwnershipAvailable ||
		skillByID(t, tb.Skills, "web-notes").Availability != OwnershipSwitchedOff ||
		!skillByID(t, tb.Skills, "web-notes").Required ||
		skillByID(t, tb.Skills, "gone").Availability != OwnershipMissing {
		t.Fatalf("skills = %+v", tb.Skills)
	}
	conns := map[string]OwnershipToolboxConnection{}
	for _, c := range tb.Connections {
		conns[c.BindingID] = c
	}
	gh := conns["mcp-github"]
	// The binding was narrowed after v1 was pinned: create_issue is no longer exposed.
	if gh.State != OwnershipAvailable || gh.Name != "GitHub" || strings.Join(gh.Tools, ",") != "list_issues" ||
		strings.Join(gh.NoLongerAllowed, ",") != "create_issue" || gh.RepoScope != "acme/site" {
		t.Fatalf("github = %+v", gh)
	}
	if conns["mcp-off"].State != OwnershipSwitchedOff || conns["mcp-missing"].State != OwnershipMissing {
		t.Fatalf("off/missing = %+v / %+v", conns["mcp-off"], conns["mcp-missing"])
	}
	if !conns["mcp-wide"].AllTools {
		t.Fatalf("unpinned wide binding = %+v", conns["mcp-wide"])
	}

	// The other instance pins v2: never v1's list, never the latest by default.
	v2 := toolboxProject(t, f, "i-v2", inventoryOf("Summarize"))
	if v2.WorkspaceToolbox.PinnedVersion != 2 || len(v2.WorkspaceToolbox.Skills) != 1 || len(v2.WorkspaceToolbox.Connections) != 0 {
		t.Fatalf("pinned v2 = %+v", v2.WorkspaceToolbox)
	}

	if none := toolboxProject(t, f, "i-none", nil); none.WorkspaceToolbox.State != OwnershipToolboxUnassigned || len(none.WorkspaceToolbox.Connections) != 0 {
		t.Fatalf("unassigned = %+v", none.WorkspaceToolbox)
	}
	broken := toolboxProject(t, f, "i-broken", nil)
	if broken.WorkspaceToolbox.State != OwnershipToolboxBroken || broken.WorkspaceToolbox.PinnedVersion != 9 ||
		len(broken.WorkspaceToolbox.Skills)+len(broken.WorkspaceToolbox.Connections) != 0 {
		t.Fatalf("broken pin fell forward: %+v", broken.WorkspaceToolbox)
	}
	if empty := toolboxProject(t, f, "i-empty", nil); empty.WorkspaceToolbox.State != OwnershipToolboxEmpty {
		t.Fatalf("explicit empty = %+v", empty.WorkspaceToolbox)
	}
	if amb := toolboxProject(t, f, "", nil); amb.WorkspaceToolbox.State != OwnershipToolboxNeedsInstance {
		t.Fatalf("ambiguous = %+v", amb.WorkspaceToolbox)
	}
	// Learned-skill availability is unknown when the collection cannot be read.
	unknown := toolboxProject(t, f, "i-v1", func(string) ([]string, error) { return nil, errors.New("x") })
	if skillByID(t, unknown.WorkspaceToolbox.Skills, "summarize").Availability != OwnershipUnknown || unknown.WorkspaceToolbox.Inventory != "unavailable" {
		t.Fatalf("unknown inventory = %+v", unknown.WorkspaceToolbox)
	}
	if v1.Permissions.WorkspaceNativeTools != "allowed" || len(v1.Permissions.Notes) == 0 {
		t.Fatalf("permissions = %+v", v1.Permissions)
	}
	if lib := project(t, f, SettingsOwnershipRequest{Agent: "Writer"}); lib.WorkspaceToolbox.State != OwnershipToolboxNotApplicable ||
		lib.Permissions.WorkspaceNativeTools != OwnershipToolboxNotApplicable {
		t.Fatalf("library context toolbox/permissions = %+v / %+v", lib.WorkspaceToolbox, lib.Permissions)
	}
}

func TestSettingsOwnershipToolboxReadsWriteNothing(t *testing.T) {
	f := newOwnershipFixture(t)
	toolboxWorkspace(t, f)
	before, _ := f.store.Get("ws-tools")
	beforeJSON, _ := json.Marshal(struct {
		A []AgentToolboxAssignment
		T []ToolboxDefinition
	}{before.ToolboxAssignments, before.Toolboxes})
	inventoryCalls := 0
	for _, inst := range []string{"", "i-v1", "i-v2", "i-none", "i-broken", "i-empty", "nope"} {
		_ = toolboxProject(t, f, inst, func(string) ([]string, error) { inventoryCalls++; return nil, nil })
	}
	after, _ := f.store.Get("ws-tools")
	afterJSON, _ := json.Marshal(struct {
		A []AgentToolboxAssignment
		T []ToolboxDefinition
	}{after.ToolboxAssignments, after.Toolboxes})
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("toolbox records changed:\n%s\n%s", beforeJSON, afterJSON)
	}
	if f.store.writes != 0 {
		t.Fatalf("%d writes", f.store.writes)
	}
	// One inventory read per projection that needs it, never per skill.
	if inventoryCalls > 7 {
		t.Fatalf("inventory read %d times for 7 projections", inventoryCalls)
	}
}

func TestSettingsOwnershipLibraryImpactFollowsTrackingNotValues(t *testing.T) {
	f := newOwnershipFixture(t)
	out := project(t, f, SettingsOwnershipRequest{Agent: "Writer"})
	got := map[string]string{}
	for _, c := range out.Impact.Copies {
		got[c.WorkspaceID] = c.Effect
	}
	want := map[string]string{
		"ws-tracked":  ImpactKeptInStep,
		"ws-custom":   ImpactCustomized,
		"ws-equal":    ImpactIndependent, // equal values, but untracked: never reached
		"ws-fallback": ImpactReadsLibrary,
		"ws-dup":      ImpactIndependent,
		"ws-broken":   ImpactUnknown,
	}
	for ws, effect := range want {
		if got[ws] != effect {
			t.Errorf("%s effect = %q, want %q", ws, got[ws], effect)
		}
	}
	if _, listed := got["ws-trashed"]; listed {
		t.Error("a trashed workspace is listed in the impact")
	}
	if out.Impact.State != "partial" {
		t.Errorf("impact state = %q, want partial (one copy unreadable)", out.Impact.State)
	}
	for _, field := range []string{"role", "description"} {
		for _, carried := range out.Impact.CarriedFields {
			if carried == field {
				t.Errorf("%s listed as carried", field)
			}
		}
	}
	if !strings.Contains(out.Impact.NotCarried, "API keys") {
		t.Errorf("not-carried note = %q", out.Impact.NotCarried)
	}
	// Workspace contexts and workspace-owned agents carry no library impact.
	if ws := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-custom"}); ws.Impact.State != OwnershipToolboxNotApplicable {
		t.Errorf("workspace impact = %+v", ws.Impact)
	}
	if scout := project(t, f, SettingsOwnershipRequest{Agent: "Scout"}); scout.Impact.State != OwnershipToolboxNotApplicable {
		t.Errorf("workspace-owned impact = %+v", scout.Impact)
	}
	if f.store.writes != 0 {
		t.Fatalf("%d writes", f.store.writes)
	}
}

func TestLibraryDefaultToolboxIsReadThroughTheLibraryRecord(t *testing.T) {
	f := newOwnershipFixture(t)
	f.library["Writer"] = store.LibraryRecord{Definition: writerDefinition("m", "p"), State: store.LibraryPresent, Owner: store.LibraryOwnerSystem}
	f.library["Writer"].Definition.DefaultToolbox = &types.AgentDefaultToolbox{Version: 1, Skills: []types.DefaultToolboxSkillRef{{CapabilityID: "a"}}}
	out := project(t, f, SettingsOwnershipRequest{Agent: "Writer"})
	if out.DirectToolbox.State != OwnershipToolboxSelected || skillByID(t, out.DirectToolbox.Skills, "a").Name != "a" {
		t.Fatalf("direct = %+v", out.DirectToolbox)
	}
}
