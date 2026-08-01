package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Migration coverage for the legacy capability shapes real workspaces are in
// (task 1.16; PRD FR-28–FR-36).

type migrationSkillStub struct {
	byAgent map[string][]ResolvedSkill
	err     error
}

func (s *migrationSkillStub) ListEnabledAgentSkills(agentName string) ([]ResolvedSkill, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byAgent[agentName], nil
}

type migrationCapacityStub struct {
	capacity   int
	expertMode bool
	resolvable bool
}

func (s *migrationCapacityStub) ResolveAgentCapacity(string) (int, bool, bool) {
	return s.capacity, s.expertMode, s.resolvable
}

func planFor(t *testing.T, plan ToolboxMigrationPlan, instanceID string) ToolboxMigrationInstancePlan {
	t.Helper()
	for _, instancePlan := range plan.Instances {
		if instancePlan.AgentInstanceID == instanceID {
			return instancePlan
		}
	}
	t.Fatalf("no migration plan for instance %s (plan: %+v)", instanceID, plan.Instances)
	return ToolboxMigrationInstancePlan{}
}

func skillIdentities(refs []ToolboxSkillRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.CapabilityID+"/"+ref.Source)
	}
	return out
}

func bindingIDs(refs []ToolboxMCPRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.BindingID)
	}
	return out
}

// Default-all legacy access: an instance with NO access entry inherited every
// enabled binding, and migration must write exactly that down (FR-29, FR-32).
func TestPlanToolboxMigration_DefaultAllAccessBecomesExplicit(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-default-all",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		SkillBindings: []SkillBinding{
			{ID: "sb-1", SkillName: "testing", Enabled: true},
			{ID: "sb-2", SkillName: "retired", Enabled: false},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true},
			{ID: "mb-3", ServerName: "old", Enabled: false},
		},
	}

	plan := PlanToolboxMigration(ws, &migrationSkillStub{}, nil)
	instancePlan := planFor(t, plan, "inst-1")

	if !instancePlan.InheritedAllBindings {
		t.Fatalf("expected the instance to be recorded as silently inheriting every binding")
	}
	assertStringsEqual(t, "migrated skills", skillIdentities(instancePlan.Skills), []string{"testing/" + ToolboxSourceWorkspaceProvided})
	assertStringsEqual(t, "migrated bindings", bindingIDs(instancePlan.MCPBindings), []string{"mb-1", "mb-2"})

	// The restricted binding keeps its exact allowlist; the all-tools binding
	// keeps deferring rather than acquiring an invented subset (FR-31).
	for _, ref := range instancePlan.MCPBindings {
		switch ref.BindingID {
		case "mb-1":
			assertStringsEqual(t, "restricted binding tools", ref.AllowedTools, []string{"read_note"})
			if ref.InheritsBindingTools {
				t.Fatalf("expected a restricted binding not to inherit the binding's tool policy")
			}
		case "mb-2":
			if !ref.InheritsBindingTools {
				t.Fatalf("expected an all-tools binding to keep deferring to the binding's policy")
			}
			if ref.AllowedTools != nil {
				t.Fatalf("expected migration not to invent a tool subset, got %v", ref.AllowedTools)
			}
		}
	}
}

// A restricted access entry narrows; an EMPTY entry denies. Migration must not
// collapse the two (FR-29).
func TestPlanToolboxMigration_RestrictedAndEmptyAccessEntries(t *testing.T) {
	ws := &Workspace{
		ID: "ws-migrate-restricted",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
			{ID: "inst-2", Name: "Writer", NodeID: "writer-1"},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true, AllowedTools: []string{"read_doc"}},
		},
		AgentMCPAccess: []AgentMCPAccess{
			{AgentInstanceID: "inst-1", EnabledBindingIDs: []string{"mb-2"}},
			{AgentInstanceID: "inst-2"},
		},
	}

	plan := PlanToolboxMigration(ws, &migrationSkillStub{}, nil)
	assertStringsEqual(t, "narrowed instance", bindingIDs(planFor(t, plan, "inst-1").MCPBindings), []string{"mb-2"})
	if got := planFor(t, plan, "inst-2").MCPBindings; len(got) != 0 {
		t.Fatalf("expected an empty access entry to migrate to no bindings, got %+v", got)
	}
}

// The legacy runtime silently preferred the agent-learned source on a name
// collision. Migration must pin THAT source, not re-derive one (FR-30).
func TestPlanToolboxMigration_PreservesTheWinningSourceOfACollision(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-collision",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		SkillBindings:  []SkillBinding{{ID: "sb-1", SkillName: "Code-Review", Enabled: true}},
	}
	skillSource := &migrationSkillStub{byAgent: map[string][]ResolvedSkill{
		"Coder": {{Name: "code-review", Enabled: true}},
	}}

	instancePlan := planFor(t, PlanToolboxMigration(ws, skillSource, nil), "inst-1")
	assertStringsEqual(t, "collision resolution", skillIdentities(instancePlan.Skills), []string{"code-review/" + ToolboxSourceAgentLearned})
}

// Two instances of one reusable agent get independent toolboxes reflecting
// their independent access entries (FR-16, FR-17).
func TestPlanToolboxMigration_DuplicateAgentNamesGetIndependentToolboxes(t *testing.T) {
	ws := &Workspace{
		ID: "ws-migrate-duplicate",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1", InstanceNumber: 1},
			{ID: "inst-2", Name: "Coder", NodeID: "coder-2", InstanceNumber: 2},
		},
		MCPBindings: []MCPBinding{
			{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}},
			{ID: "mb-2", ServerName: "docs", Enabled: true, AllowedTools: []string{"read_doc"}},
		},
		AgentMCPAccess: []AgentMCPAccess{
			{AgentInstanceID: "inst-1", EnabledBindingIDs: []string{"mb-1"}},
			{AgentInstanceID: "inst-2", EnabledBindingIDs: []string{"mb-2"}},
		},
	}

	plan := PlanToolboxMigration(ws, &migrationSkillStub{}, nil)
	assertStringsEqual(t, "instance 1", bindingIDs(planFor(t, plan, "inst-1").MCPBindings), []string{"mb-1"})
	assertStringsEqual(t, "instance 2", bindingIDs(planFor(t, plan, "inst-2").MCPBindings), []string{"mb-2"})

	if err := ApplyToolboxMigrationPlan(ws, plan, "test"); err != nil {
		t.Fatalf("ApplyToolboxMigrationPlan() error = %v", err)
	}
	if len(ws.Toolboxes) != 2 {
		t.Fatalf("expected one toolbox per instance, got %d", len(ws.Toolboxes))
	}
	names := map[string]struct{}{}
	for _, definition := range ws.Toolboxes {
		names[toolboxNameKey(definition.Name)] = struct{}{}
	}
	if len(names) != 2 {
		t.Fatalf("expected distinct toolbox names for same-named agents, got %v", names)
	}
}

// A grandfathered over-capacity agent keeps its exact migrated toolbox; nothing
// is trimmed to fit (FR-33).
func TestPlanToolboxMigration_GrandfathersOverCapacityAgents(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-overcap",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
	}
	skillSource := &migrationSkillStub{byAgent: map[string][]ResolvedSkill{
		"Coder": {{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}},
	}}

	plan := PlanToolboxMigration(ws, skillSource, &migrationCapacityStub{capacity: 2, resolvable: true})
	instancePlan := planFor(t, plan, "inst-1")

	if instancePlan.SkillSpacesUsed != 4 || !instancePlan.OverCapacity {
		t.Fatalf("expected 4 skills flagged over a 2-skill capacity, got %d used / over=%v", instancePlan.SkillSpacesUsed, instancePlan.OverCapacity)
	}
	if plan.Blocked() {
		t.Fatalf("expected over-capacity to warn, not block migration")
	}
	if err := ApplyToolboxMigrationPlan(ws, plan, "test"); err != nil {
		t.Fatalf("ApplyToolboxMigrationPlan() error = %v", err)
	}
	if got := ws.Toolboxes[0].SkillSpacesUsed(); got != 4 {
		t.Fatalf("expected the over-capacity toolbox to be preserved unchanged, got %d skills", got)
	}
	if len(ws.ToolboxMigration.Diagnostics) == 0 {
		t.Fatalf("expected the grandfathered position to be recorded as a diagnostic")
	}
}

// Expert mode is a capacity override, so an expert agent is not over capacity
// however many skills it carries (FR-60).
func TestPlanToolboxMigration_ExpertModeIsNotOverCapacity(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-expert",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
	}
	skillSource := &migrationSkillStub{byAgent: map[string][]ResolvedSkill{
		"Coder": {{Name: "a"}, {Name: "b"}, {Name: "c"}},
	}}

	instancePlan := planFor(t, PlanToolboxMigration(ws, skillSource, &migrationCapacityStub{capacity: 2, expertMode: true, resolvable: true}), "inst-1")
	if instancePlan.OverCapacity {
		t.Fatalf("expected expert mode to lift the capacity ceiling")
	}
}

// The synthesized filesystem binding is core: always present at runtime, never
// written into a migrated toolbox (FR-31, FR-59).
func TestPlanToolboxMigration_DoesNotStoreSynthesizedCoreBindings(t *testing.T) {
	ws := &Workspace{
		ID:                  "ws-migrate-core",
		AgentInstances:      []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		DirectoryReferences: []DirectoryReference{{ID: "dir-1", Name: "Project", Path: t.TempDir()}},
	}

	instancePlan := planFor(t, PlanToolboxMigration(ws, &migrationSkillStub{}, nil), "inst-1")
	for _, ref := range instancePlan.MCPBindings {
		if ref.BindingID == synthesizedFilesystemBindingID {
			t.Fatalf("expected the synthesized filesystem binding to stay core rather than be stored in the toolbox")
		}
	}
}

// Re-running migration must not create a second Workspace Default (FR-34).
func TestApplyToolboxMigrationPlan_IsIdempotent(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-idempotent",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		MCPBindings:    []MCPBinding{{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}}},
	}

	for range 3 {
		plan := PlanToolboxMigration(ws, &migrationSkillStub{}, nil)
		if err := ApplyToolboxMigrationPlan(ws, plan, "test"); err != nil {
			t.Fatalf("ApplyToolboxMigrationPlan() error = %v", err)
		}
	}

	if len(ws.Toolboxes) != 1 || len(ws.ToolboxAssignments) != 1 {
		t.Fatalf("expected exactly one toolbox and one assignment after three runs, got %d/%d", len(ws.Toolboxes), len(ws.ToolboxAssignments))
	}
	if !ws.ToolboxMigration.Migrated() {
		t.Fatalf("expected the migration state to be recorded")
	}
}

// A blocked plan writes nothing at all, so the workspace keeps its
// pre-migration behavior (FR-35).
func TestApplyToolboxMigrationPlan_BlockedPlanWritesNothing(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-blocked",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
	}
	plan := PlanToolboxMigration(ws, &migrationSkillStub{err: errors.New("skill store unavailable")}, nil)

	if !plan.Blocked() {
		t.Fatalf("expected an unreadable skill source to block migration")
	}
	if err := ApplyToolboxMigrationPlan(ws, plan, "test"); err == nil {
		t.Fatalf("expected applying a blocked plan to fail")
	}
	if len(ws.Toolboxes) != 0 || len(ws.ToolboxAssignments) != 0 || ws.ToolboxMigration.Migrated() {
		t.Fatalf("expected a blocked migration to leave the workspace untouched, got %d toolboxes", len(ws.Toolboxes))
	}
}

// A failure partway through must not leave some instances migrated (FR-35).
func TestApplyToolboxMigrationPlan_PartialFailureLeavesNothingWritten(t *testing.T) {
	ws := &Workspace{
		ID: "ws-migrate-partial",
		AgentInstances: []AgentInstance{
			{ID: "inst-1", Name: "Coder", NodeID: "coder-1"},
			{ID: "inst-2", Name: "Writer", NodeID: "writer-1"},
		},
	}
	plan := PlanToolboxMigration(ws, &migrationSkillStub{}, nil)
	// Corrupt the second instance so validation fails after the first was built.
	plan.Instances[1].Skills = []ToolboxSkillRef{{CapabilityID: "broken", Source: ToolboxSourceWorkspaceProvided}}

	if err := ApplyToolboxMigrationPlan(ws, plan, "test"); err == nil {
		t.Fatalf("expected an invalid instance plan to fail the whole migration")
	}
	if len(ws.Toolboxes) != 0 || len(ws.ToolboxAssignments) != 0 {
		t.Fatalf("expected no partial write, got %d toolboxes and %d assignments", len(ws.Toolboxes), len(ws.ToolboxAssignments))
	}
}

// Installed Workspace Capability provenance rides along on the entries whose
// bindings that capability owns — without activating anything else it owns
// (FR-32).
func TestPlanToolboxMigration_RecordsInstalledCapabilityProvenance(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-capability",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		MCPBindings: []MCPBinding{
			{ID: "mb-owned", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}},
			{ID: "mb-plain", ServerName: "docs", Enabled: true, AllowedTools: []string{"read_doc"}},
		},
		InstalledCapabilities: []InstalledCapability{{
			ID:             CapabilityFileJanitor,
			Version:        1,
			InstalledAt:    time.Now(),
			OwnedResources: []CapabilityResource{{Kind: ResourceMCPBinding, ID: "mb-owned"}},
		}},
	}

	instancePlan := planFor(t, PlanToolboxMigration(ws, &migrationSkillStub{}, nil), "inst-1")
	for _, ref := range instancePlan.MCPBindings {
		switch ref.BindingID {
		case "mb-owned":
			if ref.OwnerCapabilityID != CapabilityFileJanitor {
				t.Fatalf("expected the owned binding to record its capability provenance, got %q", ref.OwnerCapabilityID)
			}
		case "mb-plain":
			if ref.OwnerCapabilityID != "" {
				t.Fatalf("expected an unowned binding to record no capability provenance, got %q", ref.OwnerCapabilityID)
			}
		}
	}
}

// A tombstoned (removed) capability owns nothing anymore.
func TestPlanToolboxMigration_IgnoresRemovedCapabilityOwnership(t *testing.T) {
	removedAt := time.Now()
	ws := &Workspace{
		ID:             "ws-migrate-tombstone",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		MCPBindings:    []MCPBinding{{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}}},
		InstalledCapabilities: []InstalledCapability{{
			ID:             CapabilityFileJanitor,
			Version:        1,
			OwnedResources: []CapabilityResource{{Kind: ResourceMCPBinding, ID: "mb-1"}},
			RemovedAt:      &removedAt,
		}},
	}

	instancePlan := planFor(t, PlanToolboxMigration(ws, &migrationSkillStub{}, nil), "inst-1")
	if instancePlan.MCPBindings[0].OwnerCapabilityID != "" {
		t.Fatalf("expected a tombstoned capability to own nothing, got %q", instancePlan.MCPBindings[0].OwnerCapabilityID)
	}
}

// The store-level entry point plans and applies inside one update.
func TestMigrateWorkspaceToolboxes_ThroughTheStore(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-migrate-store",
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1"}},
		MCPBindings:    []MCPBinding{{ID: "mb-1", ServerName: "notes", Enabled: true, AllowedTools: []string{"read_note"}}},
	}
	store := newTestWorkspaceStore(t, ws)

	state, err := MigrateWorkspaceToolboxes(store, ws.ID, &migrationSkillStub{}, nil, "test")
	if err != nil {
		t.Fatalf("MigrateWorkspaceToolboxes() error = %v", err)
	}
	if state == nil || state.ToolboxCount != 1 {
		t.Fatalf("expected one migrated toolbox, got %+v", state)
	}

	reloaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	definition, recipe, ok, err := reloaded.ResolveAssignedToolbox("inst-1")
	if err != nil || !ok {
		t.Fatalf("ResolveAssignedToolbox() ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(definition.Name, MigratedToolboxName) {
		t.Fatalf("expected the migrated toolbox to be named %q, got %q", MigratedToolboxName, definition.Name)
	}
	if definition.Provenance != ToolboxProvenanceMigration {
		t.Fatalf("expected migration provenance, got %q", definition.Provenance)
	}
	assertStringsEqual(t, "migrated bindings", bindingIDs(recipe.MCPBindings), []string{"mb-1"})

	// Second call is a no-op — and specifically a no-WRITE one. Store.Update
	// always saves, so an unguarded re-run would rewrite workspace.json and
	// bump Version on every boot, which other sessions would see as a
	// concurrent change.
	migratedVersion := reloaded.Version
	again, err := MigrateWorkspaceToolboxes(store, ws.ID, &migrationSkillStub{}, nil, "test")
	if err != nil {
		t.Fatalf("second MigrateWorkspaceToolboxes() error = %v", err)
	}
	if again.CompletedAt != state.CompletedAt {
		t.Fatalf("expected the second migration run to be a no-op")
	}

	afterSecondRun, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("store.Get() after the second run error = %v", err)
	}
	if afterSecondRun.Version != migratedVersion {
		t.Fatalf("expected an already-migrated workspace not to be rewritten, version went %d -> %d", migratedVersion, afterSecondRun.Version)
	}
}
