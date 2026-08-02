package workspace

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Migration of legacy implicit capability state into explicit named Toolboxes
// (PRD FR-28–FR-36).
//
// Before this feature, "what may this agent instance use" was recomputed on
// every resolution by merging four sources, one of which — an ABSENT access
// entry meaning "everything enabled in the workspace" — changed meaning
// whenever the workspace changed. Migration's job is to write that computed
// answer down, exactly as it resolved at migration time, so it stops moving.
//
// The design constraint that shapes everything here: migration must be a
// no-behavior-change event. A user who migrates and then runs the same task
// must get the same tools. So the planner reads the legacy resolution rather
// than re-deriving an idealized one, and preserves the winning source of every
// collision instead of applying a fresh precedence rule (FR-30).
//
// Two structural notes:
//
//   - CORE capabilities (the synthesized filesystem binding, workspace-settings
//     managed skills) are NOT written into migrated Toolboxes. They are
//     "always present" by definition, the runtime keeps synthesizing them
//     unconditionally, and storing them would create a second place they could
//     drift out of. They remain visible in previews and Focus as `core`
//     (FR-31, FR-59).
//   - Migration is planned READ-ONLY first and applied second, so a workspace
//     that cannot be migrated safely is reported rather than half-written
//     (FR-35).

// ToolboxMigrationVersion is the algorithm version stamped onto a migrated
// workspace. Bumping it is how a future build re-runs migration deliberately;
// an unchanged version is what makes re-running a no-op (FR-34).
const ToolboxMigrationVersion = 1

// ToolboxMigrationState records that a workspace's legacy capability state has
// been made explicit. Its presence at the current version is the idempotency
// guard: migration that runs at startup, on demand, and after a restore must
// not produce three `Workspace Default` Toolboxes (FR-34).
type ToolboxMigrationState struct {
	// Version is the ToolboxMigrationVersion that produced this state.
	Version int `json:"version"`
	// CompletedAt is when migration finished successfully.
	CompletedAt time.Time `json:"completed_at"`
	// ToolboxCount and AssignmentCount are what the run produced, kept for
	// diagnostics and for the delivery audit.
	ToolboxCount    int `json:"toolbox_count"`
	AssignmentCount int `json:"assignment_count"`
	// Diagnostics carries any non-fatal findings — over-capacity instances,
	// unresolvable sources — so a user can be told what needs attention
	// without re-running the planner (FR-33, FR-35).
	Diagnostics []ToolboxMigrationDiagnostic `json:"diagnostics,omitempty"`
}

// Migrated reports whether this state satisfies the current migration version.
func (s *ToolboxMigrationState) Migrated() bool {
	return s != nil && s.Version >= ToolboxMigrationVersion
}

// Diagnostic severities.
const (
	ToolboxMigrationInfo    = "info"
	ToolboxMigrationWarning = "warning"
	ToolboxMigrationError   = "error"
)

// ToolboxMigrationDiagnostic is one finding from planning or applying a
// migration. Errors block the apply; warnings do not.
type ToolboxMigrationDiagnostic struct {
	Severity        string `json:"severity"`
	AgentInstanceID string `json:"agent_instance_id,omitempty"`
	AgentName       string `json:"agent_name,omitempty"`
	Message         string `json:"message"`
}

// ToolboxMigrationInstancePlan is the explicit Toolbox that one agent instance
// will receive, derived from what that instance could actually use before
// migration.
type ToolboxMigrationInstancePlan struct {
	AgentInstanceID string `json:"agent_instance_id"`
	AgentName       string `json:"agent_name"`
	NodeID          string `json:"node_id,omitempty"`
	ToolboxName     string `json:"toolbox_name"`

	Skills      []ToolboxSkillRef `json:"skills,omitempty"`
	MCPBindings []ToolboxMCPRef   `json:"mcp_bindings,omitempty"`

	// SkillSpacesUsed and StageCapacity report the capacity position at
	// migration time. An over-capacity instance keeps its exact migrated
	// Toolbox and is grandfathered rather than trimmed (FR-33).
	SkillSpacesUsed int  `json:"skill_spaces_used"`
	StageCapacity   int  `json:"stage_capacity"`
	ExpertMode      bool `json:"expert_mode,omitempty"`
	OverCapacity    bool `json:"over_capacity,omitempty"`

	// InheritedAllBindings records that this instance had NO explicit access
	// entry and was therefore silently inheriting every enabled workspace
	// binding. Those are precisely the instances this feature exists to fix,
	// and the number of them is the interesting migration statistic.
	InheritedAllBindings bool `json:"inherited_all_bindings,omitempty"`
}

// ToolboxMigrationPlan is the complete read-only proposal for one workspace.
//
// Nothing is written while producing it. A caller may show it, diff it, or
// discard it; ApplyToolboxMigrationPlan is the only thing that persists
// anything (FR-28, FR-35).
type ToolboxMigrationPlan struct {
	WorkspaceID      string                         `json:"workspace_id"`
	WorkspaceVersion int64                          `json:"workspace_version"`
	AlreadyMigrated  bool                           `json:"already_migrated"`
	Instances        []ToolboxMigrationInstancePlan `json:"instances,omitempty"`
	Diagnostics      []ToolboxMigrationDiagnostic   `json:"diagnostics,omitempty"`
}

// Blocked reports whether any diagnostic is fatal. A blocked plan must not be
// applied: the workspace keeps its pre-migration behavior and the caller
// reports a recoverable diagnostic (FR-35).
func (p ToolboxMigrationPlan) Blocked() bool {
	for _, diagnostic := range p.Diagnostics {
		if diagnostic.Severity == ToolboxMigrationError {
			return true
		}
	}
	return false
}

// ToolboxMigrationSkillSource supplies the global agent's enabled skills — the
// "learned" half of the legacy merge. SkillResolver already satisfies it, so
// the server passes its existing adapter rather than growing a new one.
type ToolboxMigrationSkillSource interface {
	ListEnabledAgentSkills(agentName string) ([]ResolvedSkill, error)
}

// ToolboxMigrationCapacitySource supplies one agent's stage-based active-skill
// capacity, used only to REPORT the grandfathered over-capacity position —
// migration never trims a Toolbox to fit (FR-33).
type ToolboxMigrationCapacitySource interface {
	ResolveAgentCapacity(agentName string) (capacity int, expertMode bool, ok bool)
}

// PlanToolboxMigration computes, without writing anything, the explicit
// Toolbox each agent instance in this workspace would receive.
//
// skillSource may be nil, in which case agent-learned skills are omitted and a
// warning is recorded: a workspace whose skills cannot be enumerated must not
// silently migrate to a Toolbox that drops them.
func PlanToolboxMigration(ws *Workspace, skillSource ToolboxMigrationSkillSource, capacitySource ToolboxMigrationCapacitySource) ToolboxMigrationPlan {
	plan := ToolboxMigrationPlan{}
	if ws == nil {
		plan.Diagnostics = append(plan.Diagnostics, ToolboxMigrationDiagnostic{
			Severity: ToolboxMigrationError,
			Message:  "workspace is nil",
		})
		return plan
	}

	plan.WorkspaceID = ws.ID
	plan.WorkspaceVersion = ws.Version
	plan.AlreadyMigrated = ws.ToolboxMigration.Migrated()

	instances := ws.GetAgentInstances()
	skillBindings := ws.GetSkillBindings()
	mcpBindings := ws.GetMCPBindings()
	ownerByResource := ownedResourceCapabilityIndex(ws)

	// Cache per agent NAME: two instances of the same reusable agent share one
	// learned-skill collection, and re-listing it per instance would be both
	// wasteful and a chance for the two to disagree.
	learnedByAgent := make(map[string][]ResolvedSkill, len(instances))

	for _, instance := range instances {
		instancePlan := ToolboxMigrationInstancePlan{
			AgentInstanceID: instance.ID,
			AgentName:       instance.Name,
			NodeID:          instance.NodeID,
			ToolboxName:     MigratedToolboxName,
		}

		learned, cached := learnedByAgent[instance.Name]
		if !cached {
			if skillSource != nil {
				resolved, err := skillSource.ListEnabledAgentSkills(instance.Name)
				if err != nil {
					plan.Diagnostics = append(plan.Diagnostics, ToolboxMigrationDiagnostic{
						Severity:        ToolboxMigrationError,
						AgentInstanceID: instance.ID,
						AgentName:       instance.Name,
						Message:         fmt.Sprintf("could not read learned skills for %s: %v", instance.Name, err),
					})
				} else {
					learned = resolved
				}
			} else {
				plan.Diagnostics = append(plan.Diagnostics, ToolboxMigrationDiagnostic{
					Severity:        ToolboxMigrationWarning,
					AgentInstanceID: instance.ID,
					AgentName:       instance.Name,
					Message:         "no skill source configured; agent-learned skills were not migrated into the toolbox",
				})
			}
			learnedByAgent[instance.Name] = learned
		}

		// Agent-learned skills first, mirroring the legacy precedence in which
		// they won every name collision (FR-30).
		learnedIdentities := make(map[string]struct{}, len(learned))
		for _, skill := range learned {
			identity := NormalizeToolboxCapabilityID(skill.Name)
			if identity == "" {
				continue
			}
			if _, exists := learnedIdentities[identity]; exists {
				continue
			}
			learnedIdentities[identity] = struct{}{}
			instancePlan.Skills = append(instancePlan.Skills, ToolboxSkillRef{
				CapabilityID: identity,
				DisplayName:  strings.TrimSpace(skill.Name),
				Source:       ToolboxSourceAgentLearned,
				Required:     true,
			})
		}

		allowedSkillIDs, skillAccessExplicit := legacyAllowedSkillBindingIDs(ws.GetAgentSkillAccess(instance.ID))
		for _, binding := range skillBindings {
			if !binding.Enabled || strings.TrimSpace(binding.SkillName) == "" {
				continue
			}
			if skillAccessExplicit && !allowedSkillIDs[strings.ToLower(strings.TrimSpace(binding.ID))] {
				continue
			}
			identity := NormalizeToolboxCapabilityID(binding.SkillName)
			if identity == "" {
				continue
			}
			// A workspace binding shadowed by a learned skill contributed
			// nothing before migration, so recording it now would ADD a
			// capability the agent did not have (FR-30).
			if _, shadowed := learnedIdentities[identity]; shadowed {
				continue
			}
			instancePlan.Skills = append(instancePlan.Skills, ToolboxSkillRef{
				CapabilityID:      identity,
				DisplayName:       strings.TrimSpace(binding.SkillName),
				Source:            ToolboxSourceWorkspaceProvided,
				BindingID:         binding.ID,
				OwnerCapabilityID: ownerByResource[resourceKey(ResourceMCPBinding, binding.ID)],
				Required:          true,
			})
		}

		allowedMCPIDs, mcpAccessExplicit := legacyAllowedMCPBindingIDs(ws.GetAgentMCPAccess(instance.ID))
		instancePlan.InheritedAllBindings = !mcpAccessExplicit && !skillAccessExplicit
		for _, binding := range mcpBindings {
			if !binding.Enabled || strings.TrimSpace(binding.ServerName) == "" {
				continue
			}
			if mcpAccessExplicit && !allowedMCPIDs[strings.ToLower(strings.TrimSpace(binding.ID))] {
				continue
			}
			ref := ToolboxMCPRef{
				BindingID:         binding.ID,
				OwnerCapabilityID: ownerByResource[resourceKey(ResourceMCPBinding, binding.ID)],
				Required:          true,
			}
			if binding.AllowsAllTools() {
				// See ToolboxMCPRef.InheritsBindingTools: pinning an invented
				// subset here would change what the agent can do.
				ref.InheritsBindingTools = true
			} else {
				ref.AllowedTools = normalizeToolNames(binding.AllowedTools)
			}
			instancePlan.MCPBindings = append(instancePlan.MCPBindings, ref)
		}

		instancePlan.Skills, instancePlan.MCPBindings = NormalizeToolboxContent(instancePlan.Skills, instancePlan.MCPBindings)
		if collisions := DetectToolboxSourceCollisions(instancePlan.Skills); len(collisions) > 0 {
			plan.Diagnostics = append(plan.Diagnostics, ToolboxMigrationDiagnostic{
				Severity:        ToolboxMigrationError,
				AgentInstanceID: instance.ID,
				AgentName:       instance.Name,
				Message:         fmt.Sprintf("skill %q resolves to more than one source and needs manual resolution", collisions[0].CapabilityID),
			})
		}

		instancePlan.SkillSpacesUsed = countToolboxSkillSpaces(instancePlan.Skills)
		if capacitySource != nil {
			if capacity, expert, ok := capacitySource.ResolveAgentCapacity(instance.Name); ok {
				instancePlan.StageCapacity = capacity
				instancePlan.ExpertMode = expert
				instancePlan.OverCapacity = !expert && instancePlan.SkillSpacesUsed > capacity
			}
		}
		if instancePlan.OverCapacity {
			plan.Diagnostics = append(plan.Diagnostics, ToolboxMigrationDiagnostic{
				Severity:        ToolboxMigrationWarning,
				AgentInstanceID: instance.ID,
				AgentName:       instance.Name,
				Message: fmt.Sprintf("%s keeps %d active skills above its %d-skill capacity; the toolbox is preserved and marked full",
					instance.Name, instancePlan.SkillSpacesUsed, instancePlan.StageCapacity),
			})
		}

		plan.Instances = append(plan.Instances, instancePlan)
	}

	return plan
}

// legacyAllowedSkillBindingIDs / legacyAllowedMCPBindingIDs read a legacy
// access entry into the set of binding IDs it permits, plus whether the entry
// existed at all.
//
// The second return value is the whole point: a MISSING entry meant "everything
// enabled", while a PRESENT entry with an empty list meant "nothing". Collapsing
// those two into one empty set is the bug this feature removes, so migration
// has to keep them distinct while reading the legacy state.
func legacyAllowedSkillBindingIDs(entry *AgentSkillAccess, exists bool) (map[string]bool, bool) {
	if !exists || entry == nil {
		return nil, false
	}
	return normalizeValueSet(entry.EnabledBindingIDs), true
}

func legacyAllowedMCPBindingIDs(entry *AgentMCPAccess, exists bool) (map[string]bool, bool) {
	if !exists || entry == nil {
		return nil, false
	}
	return normalizeValueSet(entry.EnabledBindingIDs), true
}

// resourceKey is the composite key used to look a workspace resource up in the
// installed-capability ownership index.
func resourceKey(kind, id string) string {
	return normalizeResourceKind(kind) + "\x00" + strings.TrimSpace(id)
}

// ownedResourceCapabilityIndex maps each workspace resource to the installed
// Workspace Capability that owns it.
//
// This is provenance only. A Toolbox entry recording "this binding came with
// File Janitor" lets the Workshop group it and lets readiness recompute when
// the capability's resources change (FR-32) — it never causes the parent
// capability to install, uninstall, or activate.
func ownedResourceCapabilityIndex(ws *Workspace) map[string]string {
	if ws == nil || len(ws.InstalledCapabilities) == 0 {
		return nil
	}
	index := make(map[string]string)
	for _, capability := range ws.InstalledCapabilities {
		if !capability.Active() {
			continue
		}
		for _, resource := range capability.OwnedResources {
			if !resource.Valid() {
				continue
			}
			index[resourceKey(resource.Kind, resource.ID)] = NormalizeCapabilityID(capability.ID)
		}
	}
	if len(index) == 0 {
		return nil
	}
	return index
}

// ApplyToolboxMigrationPlan writes a plan into the workspace: one explicit
// Toolbox and one pinned assignment per agent instance.
//
// It is all-or-nothing on the in-memory workspace — the caller runs it inside
// Store.Update, so a returned error means nothing is saved and the workspace
// keeps its pre-migration behavior (FR-35). Re-running it after success is a
// no-op (FR-34).
func ApplyToolboxMigrationPlan(ws *Workspace, plan ToolboxMigrationPlan, actor string) error {
	if ws == nil {
		return fmt.Errorf("workspace is required")
	}
	if plan.Blocked() {
		for _, diagnostic := range plan.Diagnostics {
			if diagnostic.Severity == ToolboxMigrationError {
				return fmt.Errorf("toolbox migration blocked for workspace %s: %s", ws.ID, diagnostic.Message)
			}
		}
	}

	// Build every Toolbox and assignment against COPIES first, then commit, so
	// a validation failure partway through cannot leave half the instances
	// migrated (FR-35).
	type pending struct {
		definition ToolboxDefinition
		assignment AgentToolboxAssignment
	}
	pendingWrites := make([]pending, 0, len(plan.Instances))
	reservedNames := make([]string, 0, len(plan.Instances))
	now := time.Now()

	for _, instancePlan := range plan.Instances {
		if strings.TrimSpace(instancePlan.AgentInstanceID) == "" {
			return fmt.Errorf("toolbox migration plan contains an instance with no ID")
		}
		// Skip instances that already have an explicit assignment. This is what
		// makes the operation an ENSURE rather than a one-shot: an agent
		// attached to a workspace long after its migration still gets its
		// capabilities written down, and re-running over an already-migrated
		// workspace stays a no-op (FR-34).
		if _, assigned := ws.GetToolboxAssignment(instancePlan.AgentInstanceID); assigned {
			continue
		}
		name, err := uniqueMigratedToolboxName(ws, instancePlan, reservedNames)
		if err != nil {
			return err
		}
		reservedNames = append(reservedNames, name)
		skills, bindings := NormalizeToolboxContent(instancePlan.Skills, instancePlan.MCPBindings)
		if err := ValidateToolboxContent(skills, bindings); err != nil {
			return fmt.Errorf("toolbox migration for agent instance %s: %w", instancePlan.AgentInstanceID, err)
		}

		definition := ToolboxDefinition{
			ID:          "tbx-" + uuid.New().String(),
			WorkspaceID: ws.ID,
			Name:        name,
			Description: fmt.Sprintf("The capabilities %s already had when toolboxes were introduced.", displayInstanceName(instancePlan)),
			Version:     1,
			Status:      ToolboxStatusActive,
			Skills:      skills,
			MCPBindings: bindings,
			CreatedAt:   now,
			UpdatedAt:   now,
			Provenance:  ToolboxProvenanceMigration,
			Actor:       strings.TrimSpace(actor),
		}
		assignment := AgentToolboxAssignment{
			AgentInstanceID: instancePlan.AgentInstanceID,
			ToolboxID:       definition.ID,
			ToolboxVersion:  definition.Version,
			AppliedAt:       now,
			Provenance:      ToolboxProvenanceMigration,
			Actor:           strings.TrimSpace(actor),
		}
		pendingWrites = append(pendingWrites, pending{definition: definition, assignment: assignment})
	}

	// Nothing to do: already-explicit workspace, and no new instance to cover.
	// Returning without touching the record is what keeps the ensure free to
	// run often (see MigrateWorkspaceToolboxes's read-first guard).
	if len(pendingWrites) == 0 && ws.ToolboxMigration.Migrated() {
		return nil
	}

	for _, write := range pendingWrites {
		ws.Toolboxes = append(ws.Toolboxes, write.definition.Clone())
		ws.ToolboxAssignments = append(ws.ToolboxAssignments, write.assignment.Clone())
	}

	previous := ws.ToolboxMigration
	state := &ToolboxMigrationState{
		Version:     ToolboxMigrationVersion,
		CompletedAt: now,
		Diagnostics: plan.Diagnostics,
	}
	if previous != nil {
		// Counts are cumulative across ensures, so they still answer "how much
		// of this workspace was made explicit by migration" after a later agent
		// was covered by the same mechanism.
		state.ToolboxCount = previous.ToolboxCount
		state.AssignmentCount = previous.AssignmentCount
		if previous.CompletedAt.After(now) || !previous.CompletedAt.IsZero() && len(pendingWrites) == 0 {
			state.CompletedAt = previous.CompletedAt
		}
	}
	state.ToolboxCount += len(pendingWrites)
	state.AssignmentCount += len(pendingWrites)

	ws.ToolboxMigration = state
	ws.UpdatedAt = now
	return nil
}

// uniqueMigratedToolboxName gives each instance a distinct `Workspace Default`
// name, disambiguated by agent when a workspace has several instances.
//
// Toolbox names are case-insensitively unique per workspace (FR-41), and a
// workspace with three agents needs three migrated Toolboxes — so the plain
// name can only go to the first one.
func uniqueMigratedToolboxName(ws *Workspace, instancePlan ToolboxMigrationInstancePlan, reserved []string) (string, error) {
	taken := make(map[string]struct{}, len(ws.Toolboxes)+len(reserved))
	for i := range ws.Toolboxes {
		taken[toolboxNameKey(ws.Toolboxes[i].Name)] = struct{}{}
	}
	for _, name := range reserved {
		taken[toolboxNameKey(name)] = struct{}{}
	}

	candidates := []string{
		MigratedToolboxName,
		fmt.Sprintf("%s — %s", MigratedToolboxName, displayInstanceName(instancePlan)),
	}
	for _, candidate := range candidates {
		if _, exists := taken[toolboxNameKey(candidate)]; !exists {
			return NormalizeToolboxName(candidate), nil
		}
	}
	for suffix := 2; suffix < 1000; suffix++ {
		candidate := fmt.Sprintf("%s — %s %d", MigratedToolboxName, displayInstanceName(instancePlan), suffix)
		if _, exists := taken[toolboxNameKey(candidate)]; !exists {
			return NormalizeToolboxName(candidate), nil
		}
	}
	return "", fmt.Errorf("could not derive a unique toolbox name for agent instance %s", instancePlan.AgentInstanceID)
}

func displayInstanceName(instancePlan ToolboxMigrationInstancePlan) string {
	name := strings.TrimSpace(instancePlan.AgentName)
	if name == "" {
		name = strings.TrimSpace(instancePlan.AgentInstanceID)
	}
	if name == "" {
		return "this agent"
	}
	return name
}

// MigrateWorkspaceToolboxes plans and applies migration for one workspace
// inside a single store update, so the read that produces the plan and the
// write that applies it cannot straddle a concurrent workspace change.
//
// It returns the state that resulted, or nil when the workspace was already
// migrated.
func MigrateWorkspaceToolboxes(store Store, workspaceID string, skillSource ToolboxMigrationSkillSource, capacitySource ToolboxMigrationCapacitySource, actor string) (*ToolboxMigrationState, error) {
	if store == nil {
		return nil, fmt.Errorf("workspace store is required")
	}

	// Check under a plain read first. Store.Update always saves, so routing a
	// workspace with nothing to do through it would rewrite workspace.json and
	// bump Version on every call — churn that other sessions would see as a
	// concurrent change.
	if existing, err := store.Get(workspaceID); err == nil && !toolboxMigrationNeeded(existing) {
		return existing.ToolboxMigration, nil
	}

	var result *ToolboxMigrationState
	err := store.Update(workspaceID, func(ws *Workspace) error {
		if !toolboxMigrationNeeded(ws) {
			result = ws.ToolboxMigration
			return nil
		}
		plan := PlanToolboxMigration(ws, skillSource, capacitySource)
		if err := ApplyToolboxMigrationPlan(ws, plan, actor); err != nil {
			return err
		}
		result = ws.ToolboxMigration
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// toolboxMigrationNeeded reports whether this workspace has any instance whose
// capabilities are still implicit.
//
// The check is per INSTANCE, not per workspace, because an agent attached after
// migration would otherwise keep resolving through the legacy merge forever —
// silently inheriting every binding the workspace gains, which is the exact
// behavior this feature removes. There are ~18 code paths that attach an agent,
// so covering them by hooking each one would be a maintenance trap; making the
// ensure cheap and idempotent, and calling it from the few places that matter,
// is the durable version.
func toolboxMigrationNeeded(ws *Workspace) bool {
	if ws == nil {
		return false
	}
	if !ws.ToolboxMigration.Migrated() {
		return true
	}
	for _, instance := range ws.GetAgentInstances() {
		if _, assigned := ws.GetToolboxAssignment(instance.ID); !assigned {
			return true
		}
	}
	return false
}

// EnsureToolboxAssignments makes every agent instance in one workspace
// explicit, and is safe to call on any read path that is about to present or
// resolve Toolbox state. It writes only when something is genuinely missing.
func EnsureToolboxAssignments(store Store, workspaceID string, skillSource ToolboxMigrationSkillSource, capacitySource ToolboxMigrationCapacitySource) error {
	_, err := MigrateWorkspaceToolboxes(store, workspaceID, skillSource, capacitySource, "ensure")
	return err
}
