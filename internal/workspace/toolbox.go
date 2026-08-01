package workspace

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Named Toolboxes: the explicit, versioned recipe of exactly which capabilities
// one workspace agent instance may use (PRD FR-1–FR-23).
//
// The type that matters here is the SEPARATION. Ori already had four different
// notions of "the agent has this capability", and they were only distinguishable
// by which code path you happened to be reading:
//
//   - installed        — InstalledCapability, "this workspace has File Janitor"
//   - workshop-bound   — SkillBinding / MCPBinding, "this workspace approved it"
//   - learned          — the global agent's enabled skills, "this agent knows it"
//   - active           — what the runtime actually handed the model
//
// Before Toolboxes, `active` was *derived* from the other three by a merge, so
// binding something to the workspace silently changed what every agent in it
// could do. A ToolboxDefinition makes `active` a stored, named, versioned fact
// that only changes when a user changes it (FR-2, FR-32).
//
// This file owns the domain: identities, versions, validation, normalization,
// and source-collision detection. Persistence accessors live in
// workspace_toolbox.go; migration from legacy implicit state lives in
// toolbox_migration.go.

// Skill sources a Toolbox entry may declare (FR-5). Every entry names exactly
// one — a skill that could come from two places must pin which one it came from
// rather than letting a precedence rule pick at runtime (FR-6).
const (
	// ToolboxSourceAgentLearned is a skill carried by the reusable global agent.
	ToolboxSourceAgentLearned = "agent_learned"
	// ToolboxSourceWorkspaceProvided is a skill supplied by a workspace skill
	// binding. It names that binding's ID so a rename cannot re-point it.
	ToolboxSourceWorkspaceProvided = "workspace_provided"
	// ToolboxSourceCore is a mandatory Ori runtime ability or synthesized
	// binding. Core entries are always present, cannot be deselected, and do not
	// consume a skill space — but they remain visible in Focus and risk
	// summaries so the surface is never understated (FR-59, FR-64).
	ToolboxSourceCore = "core"
)

// Toolbox lifecycle status (FR-9, FR-20).
const (
	// ToolboxStatusActive is a Toolbox that may be selected and used.
	ToolboxStatusActive = "active"
	// ToolboxStatusDraft is a Toolbox created but never yet used. It is
	// selectable; the distinction exists so the UI can tell a never-used
	// recipe from a retired one.
	ToolboxStatusDraft = "draft"
	// ToolboxStatusArchived is a retired Toolbox. It stays resolvable from run
	// snapshots and audits but can no longer be newly selected (FR-19, FR-20).
	ToolboxStatusArchived = "archived"
)

// Provenance values recording how a Toolbox version or assignment came to
// exist (FR-9, FR-15, FR-160). The set is open — any normalized non-empty
// string is accepted — so a later flow can name itself without a change here.
const (
	// ToolboxProvenanceMigration is the one-time migration of legacy implicit
	// capability state into an explicit `Workspace Default` (FR-29).
	ToolboxProvenanceMigration = "migration"
	// ToolboxProvenanceUser is a deliberate user action in the Workshop.
	ToolboxProvenanceUser = "user"
	// ToolboxProvenanceLegacyAccessAPI is a mutation arriving through the
	// pre-Toolbox per-instance access endpoints, which remain available during
	// the compatibility window (FR-36).
	ToolboxProvenanceLegacyAccessAPI = "legacy_access_api"
	// ToolboxProvenanceWrapUp is a variant drafted from a run Wrap-up (FR-118).
	ToolboxProvenanceWrapUp = "wrap_up"
)

// MigratedToolboxName is the name given to the explicit Toolbox created for
// every pre-existing agent instance during migration (FR-29). It is a
// user-visible name, so it uses the cozy vocabulary rather than an identifier.
const MigratedToolboxName = "Workspace Default"

// Bounds on user-authored Toolbox metadata (FR-41, FR-42). Names are bounded so
// a pasted document cannot become a Toolbox name; descriptions are bounded for
// the same reason with more room.
const (
	MaxToolboxNameLength        = 60
	MaxToolboxDescriptionLength = 500
)

var (
	// ErrToolboxNotFound means no Toolbox with the requested ID exists in the
	// workspace. Callers translate this to 404.
	ErrToolboxNotFound = errors.New("workspace: toolbox not found")
	// ErrToolboxVersionNotFound means the Toolbox exists but the requested
	// version is neither its current version nor a retained historical one.
	ErrToolboxVersionNotFound = errors.New("workspace: toolbox version not found")
	// ErrToolboxNameTaken means another Toolbox in the same workspace already
	// uses that name, compared case-insensitively (FR-41).
	ErrToolboxNameTaken = errors.New("workspace: toolbox name already in use")
	// ErrToolboxArchived means the Toolbox may no longer be newly selected
	// (FR-20). It remains readable for audit.
	ErrToolboxArchived = errors.New("workspace: toolbox is archived")
	// ErrToolboxSourceCollision means one Toolbox names the same normalized
	// skill identity twice from different sources. Resolution is the user's
	// call, never a silent precedence rule (FR-6).
	ErrToolboxSourceCollision = errors.New("workspace: toolbox skill resolves to two different sources")
	// ErrToolboxWidensAllowedTools means a Toolbox MCP entry lists a tool its
	// workspace binding does not permit. A Toolbox may narrow a binding's
	// AllowedTools policy and never widen it (FR-12).
	ErrToolboxWidensAllowedTools = errors.New("workspace: toolbox tool selection widens the binding's allowed tools")
	// ErrToolboxAllToolsSemantics means a Toolbox MCP entry carries no explicit
	// tool list. New Toolboxes must name a concrete subset even when the
	// binding itself permits everything (FR-13).
	ErrToolboxAllToolsSemantics = errors.New("workspace: toolbox MCP entry must list explicit tools")
	// ErrToolboxFull means the edit would add a skill beyond the agent's
	// stage-based active-skill capacity (FR-33, FR-56).
	ErrToolboxFull = errors.New("workspace: toolbox is full")
)

// ToolboxSkillRef is one skill a Toolbox activates (FR-10).
type ToolboxSkillRef struct {
	// CapabilityID is the stable normalized skill identity — the lower-cased
	// skill name. It is what deduplication and capacity counting key on.
	CapabilityID string `json:"capability_id"`
	// DisplayName preserves the exact-case name as saved, for UI and for
	// resolving the skill by name against the skill manager.
	DisplayName string `json:"display_name,omitempty"`
	// Source is one of the ToolboxSource* constants: exactly where this skill
	// comes from. The runtime resolves this source and no other (FR-6).
	Source string `json:"source"`
	// BindingID names the workspace skill binding when Source is
	// workspace_provided. Required in that case and empty otherwise: a
	// binding ID on an agent-learned entry would be an unresolvable mixture.
	BindingID string `json:"binding_id,omitempty"`
	// OwnerCapabilityID records the installed Workspace Capability that owns
	// the referenced binding, when one does (FR-32). It is provenance for the
	// Workshop — "this came with File Janitor" — and drives readiness
	// recomputation when a capability's owned resources change. It never makes
	// the parent capability install, uninstall, or activate.
	OwnerCapabilityID string `json:"owner_capability_id,omitempty"`
	// Required marks a capability the Toolbox cannot work without. A missing
	// required capability makes the Toolbox not ready; a missing optional one
	// warns and is omitted from the effective Toolbox (FR-14).
	Required bool `json:"required,omitempty"`
}

// ToolboxMCPRef is one workspace MCP binding a Toolbox activates, narrowed to
// an explicit set of operations (FR-11).
type ToolboxMCPRef struct {
	// BindingID is the exact workspace MCP binding. Never a server name: a
	// workspace may bind the same server template twice with different scopes,
	// and only the binding ID distinguishes them (FR-7).
	BindingID string `json:"binding_id"`
	// AllowedTools is the explicit, concrete tool subset this Toolbox exposes.
	// Non-nil and explicit even when the binding permits everything (FR-13); an
	// empty list means the Toolbox exposes no operations from this binding.
	AllowedTools []string `json:"allowed_tools"`
	// InheritsBindingTools preserves the pre-Toolbox "all tools" semantics for a
	// MIGRATED entry only, and is the single exception to FR-13.
	//
	// Migration cannot honestly pin a concrete tool subset for a binding that
	// permitted everything: discovering the real tool list needs a live
	// connection to the MCP server, and inventing one would either narrow the
	// agent's capabilities (breaking FR-31's "preserve existing behavior") or
	// list operations that do not exist. So a migrated all-tools binding keeps
	// deferring to the binding's own policy and is surfaced to the user as a
	// repair to make explicit.
	//
	// Nothing a user creates may set this: create/version validation rejects a
	// nil tool list unless this flag came from migration.
	InheritsBindingTools bool `json:"inherits_binding_tools,omitempty"`
	// OwnerCapabilityID records the installed Workspace Capability that owns
	// this binding, when one does. See ToolboxSkillRef.OwnerCapabilityID.
	OwnerCapabilityID string `json:"owner_capability_id,omitempty"`
	// Required marks a binding the Toolbox cannot work without (FR-14).
	Required bool `json:"required,omitempty"`
}

// ToolboxRecipe is the immutable versioned CONTENT of a Toolbox: exactly which
// capabilities that version grants (FR-18).
//
// Content is versioned separately from identity so editing a Toolbox produces a
// new version rather than rewriting the meaning of a version a run snapshot
// already referenced. A recipe, once recorded in History, is never mutated.
type ToolboxRecipe struct {
	Version     int64             `json:"version"`
	Skills      []ToolboxSkillRef `json:"skills,omitempty"`
	MCPBindings []ToolboxMCPRef   `json:"mcp_bindings,omitempty"`
	// CreatedAt is when this version was saved.
	CreatedAt time.Time `json:"created_at"`
	// Provenance and Actor record who produced this version and through which
	// flow (FR-160).
	Provenance string `json:"provenance,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

// ToolboxDefinition is a named, workspace-owned, versioned Toolbox (FR-8, FR-9).
//
// The current version's content is carried inline (Skills/MCPBindings/Version)
// so the overwhelmingly common read — "what does this Toolbox grant now?" —
// needs no lookup. History holds the PRIOR versions only; ResolveVersion
// bridges the two.
type ToolboxDefinition struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	// Version is the current version number, monotonically increasing and never
	// reused (FR-18).
	Version int64 `json:"version"`
	// Status is one of the ToolboxStatus* constants (FR-9, FR-20).
	Status string `json:"status"`

	// Current version content.
	Skills      []ToolboxSkillRef `json:"skills,omitempty"`
	MCPBindings []ToolboxMCPRef   `json:"mcp_bindings,omitempty"`

	// History holds every prior version in ascending order, retained
	// indefinitely in V1 so a run snapshot's version stays resolvable for audit
	// (FR-19). It never contains the current version.
	History []ToolboxRecipe `json:"history,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Provenance records how this Toolbox was first created (FR-9).
	Provenance string `json:"provenance,omitempty"`
	// Actor records who created it (FR-160).
	Actor string `json:"actor,omitempty"`
}

// PriorToolboxAssignment is the assignment that was in force before the current
// one, retained so Undo can offer to restore it (FR-88).
type PriorToolboxAssignment struct {
	ToolboxID      string    `json:"toolbox_id"`
	ToolboxVersion int64     `json:"toolbox_version"`
	AppliedAt      time.Time `json:"applied_at"`
	Provenance     string    `json:"provenance,omitempty"`
	Actor          string    `json:"actor,omitempty"`
}

// AgentToolboxAssignment is the Toolbox version currently selected for one
// stable workspace agent instance (FR-15).
//
// Keyed by AgentInstance.ID and never by agent name: two instances of the same
// reusable agent must be able to carry different Toolboxes, and the pre-Toolbox
// name-keyed toggles could not express that (FR-16, FR-17).
type AgentToolboxAssignment struct {
	AgentInstanceID string `json:"agent_instance_id"`
	ToolboxID       string `json:"toolbox_id"`
	// ToolboxVersion pins the exact version in force. It does not follow later
	// edits of the Toolbox — a new version must be used deliberately.
	ToolboxVersion int64     `json:"toolbox_version"`
	AppliedAt      time.Time `json:"applied_at"`
	Provenance     string    `json:"provenance,omitempty"`
	Actor          string    `json:"actor,omitempty"`
	// Previous retains exactly one step of history for Undo (FR-88–FR-90).
	Previous *PriorToolboxAssignment `json:"previous,omitempty"`
}

// NormalizeToolboxCapabilityID returns the stable identity used to compare,
// deduplicate, and capacity-count a skill. Skill identities arrive from the
// skill manager, workspace bindings, hand-edited workspace.json, and the API,
// with inconsistent case and stray whitespace; every comparison in this feature
// routes both sides through here so a lookup cannot miss on spelling (FR-30).
func NormalizeToolboxCapabilityID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// NormalizeToolboxSource maps a source string onto one of the ToolboxSource*
// constants, returning "" for anything unrecognized. Unrecognized sources are
// rejected rather than defaulted: guessing would re-introduce exactly the
// silent precedence FR-6 forbids.
func NormalizeToolboxSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case ToolboxSourceAgentLearned:
		return ToolboxSourceAgentLearned
	case ToolboxSourceWorkspaceProvided:
		return ToolboxSourceWorkspaceProvided
	case ToolboxSourceCore:
		return ToolboxSourceCore
	default:
		return ""
	}
}

// NormalizeToolboxStatus maps a lifecycle status onto one of the
// ToolboxStatus* constants. An empty or unrecognized status resolves to
// active — the safe reading for a record written by an older build, since the
// alternative would hide a Toolbox the user can see referenced elsewhere.
func NormalizeToolboxStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case ToolboxStatusArchived:
		return ToolboxStatusArchived
	case ToolboxStatusDraft:
		return ToolboxStatusDraft
	default:
		return ToolboxStatusActive
	}
}

// NormalizeToolboxName trims and collapses internal whitespace in a
// user-visible Toolbox name. Collapsing matters because "Research  Kit" and
// "Research Kit" are the same name to a reader, and uniqueness is checked
// against what the reader sees.
func NormalizeToolboxName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// toolboxNameKey is the case-insensitive uniqueness key for a Toolbox name
// (FR-41).
func toolboxNameKey(name string) string {
	return strings.ToLower(NormalizeToolboxName(name))
}

// ConsumesSkillSpace reports whether this entry counts against the agent's
// stage-based active-skill capacity. Core capabilities are always present and
// never consume a space (FR-59).
func (r ToolboxSkillRef) ConsumesSkillSpace() bool {
	return NormalizeToolboxSource(r.Source) != ToolboxSourceCore
}

// Normalize returns the entry with its identity, source, and binding reference
// canonicalized. It does not validate — Validate reports what is still wrong
// after normalization.
func (r ToolboxSkillRef) Normalize() ToolboxSkillRef {
	out := r
	out.DisplayName = strings.TrimSpace(r.DisplayName)
	out.CapabilityID = NormalizeToolboxCapabilityID(r.CapabilityID)
	if out.CapabilityID == "" {
		out.CapabilityID = NormalizeToolboxCapabilityID(out.DisplayName)
	}
	if out.DisplayName == "" {
		out.DisplayName = out.CapabilityID
	}
	out.Source = NormalizeToolboxSource(r.Source)
	out.BindingID = strings.TrimSpace(r.BindingID)
	out.OwnerCapabilityID = NormalizeCapabilityID(r.OwnerCapabilityID)
	// A binding ID only means something for a workspace-provided skill.
	// Carrying one on an agent-learned or core entry would leave two
	// disagreeing answers to "where does this skill come from".
	if out.Source != ToolboxSourceWorkspaceProvided {
		out.BindingID = ""
	}
	return out
}

// Validate reports why this skill entry cannot be saved, or nil.
func (r ToolboxSkillRef) Validate() error {
	if r.CapabilityID == "" {
		return fmt.Errorf("toolbox skill entry requires a capability identity")
	}
	if r.Source == "" {
		return fmt.Errorf("toolbox skill %q requires a source of %q, %q, or %q",
			r.CapabilityID, ToolboxSourceAgentLearned, ToolboxSourceWorkspaceProvided, ToolboxSourceCore)
	}
	if r.Source == ToolboxSourceWorkspaceProvided && r.BindingID == "" {
		return fmt.Errorf("workspace-provided toolbox skill %q requires a workspace skill binding ID", r.CapabilityID)
	}
	return nil
}

// Normalize returns the MCP entry with its binding reference and tool list
// canonicalized: trimmed, deduplicated case-insensitively, and sorted so two
// selections of the same operations compare and hash identically.
func (r ToolboxMCPRef) Normalize() ToolboxMCPRef {
	out := r
	out.BindingID = strings.TrimSpace(r.BindingID)
	out.OwnerCapabilityID = NormalizeCapabilityID(r.OwnerCapabilityID)
	// A nil list is preserved as nil so Validate can reject it as legacy
	// all-tools semantics (FR-13); an explicit empty list stays a real,
	// meaningful "no operations" selection.
	if out.InheritsBindingTools {
		out.AllowedTools = nil
	} else if r.AllowedTools != nil {
		out.AllowedTools = normalizeToolNames(r.AllowedTools)
	}
	return out
}

// Validate reports why this MCP entry cannot be saved, or nil.
func (r ToolboxMCPRef) Validate() error {
	if r.BindingID == "" {
		return fmt.Errorf("toolbox MCP entry requires a workspace binding ID")
	}
	if r.AllowedTools == nil && !r.InheritsBindingTools {
		return fmt.Errorf("%w: binding %s", ErrToolboxAllToolsSemantics, r.BindingID)
	}
	return nil
}

// NeedsExplicitTools reports whether this entry still defers to its binding's
// tool policy instead of naming a concrete subset. Such entries are legal
// (migration produced them) but incomplete, and the Workshop surfaces them as
// a repair (FR-13, FR-47).
func (r ToolboxMCPRef) NeedsExplicitTools() bool {
	return r.InheritsBindingTools
}

// normalizeToolNames trims, case-insensitively deduplicates, and sorts tool
// names, always returning a non-nil slice so the result can be distinguished
// from the nil "all tools" sentinel.
func normalizeToolNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

// Clone returns a deep copy of the recipe.
func (r ToolboxRecipe) Clone() ToolboxRecipe {
	cp := r
	cp.Skills = cloneToolboxSkillRefs(r.Skills)
	cp.MCPBindings = cloneToolboxMCPRefs(r.MCPBindings)
	return cp
}

func cloneToolboxSkillRefs(refs []ToolboxSkillRef) []ToolboxSkillRef {
	if len(refs) == 0 {
		return nil
	}
	return append([]ToolboxSkillRef(nil), refs...)
}

func cloneToolboxMCPRefs(refs []ToolboxMCPRef) []ToolboxMCPRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]ToolboxMCPRef, len(refs))
	for i, ref := range refs {
		out[i] = ref
		if ref.AllowedTools != nil {
			out[i].AllowedTools = append([]string(nil), ref.AllowedTools...)
		}
	}
	return out
}

// Clone returns a deep copy of the definition, including its version history.
func (d ToolboxDefinition) Clone() ToolboxDefinition {
	cp := d
	cp.Skills = cloneToolboxSkillRefs(d.Skills)
	cp.MCPBindings = cloneToolboxMCPRefs(d.MCPBindings)
	if len(d.History) > 0 {
		cp.History = make([]ToolboxRecipe, len(d.History))
		for i, recipe := range d.History {
			cp.History[i] = recipe.Clone()
		}
	}
	return cp
}

// Clone returns a deep copy of the assignment.
func (a AgentToolboxAssignment) Clone() AgentToolboxAssignment {
	cp := a
	if a.Previous != nil {
		prev := *a.Previous
		cp.Previous = &prev
	}
	return cp
}

// CurrentRecipe returns the definition's current version as a recipe, so
// callers can treat current and historical versions uniformly.
func (d ToolboxDefinition) CurrentRecipe() ToolboxRecipe {
	return ToolboxRecipe{
		Version:     d.Version,
		Skills:      cloneToolboxSkillRefs(d.Skills),
		MCPBindings: cloneToolboxMCPRefs(d.MCPBindings),
		CreatedAt:   d.UpdatedAt,
		Provenance:  d.Provenance,
		Actor:       d.Actor,
	}
}

// ResolveVersion returns the exact content of one version, current or
// historical. A run snapshot, comparison, or audit that names a version must
// still get that version's meaning after the Toolbox has been edited or
// archived (FR-19).
func (d ToolboxDefinition) ResolveVersion(version int64) (ToolboxRecipe, error) {
	if version == d.Version {
		return d.CurrentRecipe(), nil
	}
	for _, recipe := range d.History {
		if recipe.Version == version {
			return recipe.Clone(), nil
		}
	}
	return ToolboxRecipe{}, fmt.Errorf("%w: toolbox %s version %d", ErrToolboxVersionNotFound, d.ID, version)
}

// Archived reports whether this Toolbox may no longer be newly selected.
func (d ToolboxDefinition) Archived() bool {
	return NormalizeToolboxStatus(d.Status) == ToolboxStatusArchived
}

// SkillSpacesUsed counts the entries that consume the agent's stage-based
// active-skill capacity: the deduplicated non-core skills. MCP operations never
// consume a skill space — capacity is about prompt/skill surface, and MCP
// exposure is assessed separately by Focus (FR-58, FR-59).
func (d ToolboxDefinition) SkillSpacesUsed() int {
	return countToolboxSkillSpaces(d.Skills)
}

// SkillSpacesUsed counts a recipe's space-consuming skills. See
// ToolboxDefinition.SkillSpacesUsed.
func (r ToolboxRecipe) SkillSpacesUsed() int {
	return countToolboxSkillSpaces(r.Skills)
}

func countToolboxSkillSpaces(refs []ToolboxSkillRef) int {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if !ref.ConsumesSkillSpace() {
			continue
		}
		key := NormalizeToolboxCapabilityID(ref.CapabilityID)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	return len(seen)
}

// NormalizeToolboxContent canonicalizes and deterministically orders a
// version's capability entries.
//
// Ordering is deliberate: it makes two independently-built selections of the
// same capabilities compare equal, which is what lets version comparison,
// snapshot hashing, and drift detection be exact rather than approximate.
// Case-insensitive deduplication happens here too, keeping the FIRST entry for
// a given identity so a caller's stated preference wins over list position.
func NormalizeToolboxContent(skills []ToolboxSkillRef, bindings []ToolboxMCPRef) ([]ToolboxSkillRef, []ToolboxMCPRef) {
	normalizedSkills := make([]ToolboxSkillRef, 0, len(skills))
	seenSkills := make(map[string]struct{}, len(skills))
	for _, ref := range skills {
		normalized := ref.Normalize()
		if normalized.CapabilityID == "" {
			continue
		}
		// Deduplicate on identity+source, not identity alone: two entries with
		// the same identity but different sources are a COLLISION the user must
		// resolve, and silently dropping one here would resolve it for them.
		key := normalized.CapabilityID + "\x00" + normalized.Source + "\x00" + strings.ToLower(normalized.BindingID)
		if _, exists := seenSkills[key]; exists {
			continue
		}
		seenSkills[key] = struct{}{}
		normalizedSkills = append(normalizedSkills, normalized)
	}
	sort.SliceStable(normalizedSkills, func(i, j int) bool {
		if normalizedSkills[i].CapabilityID != normalizedSkills[j].CapabilityID {
			return normalizedSkills[i].CapabilityID < normalizedSkills[j].CapabilityID
		}
		return normalizedSkills[i].Source < normalizedSkills[j].Source
	})

	normalizedBindings := make([]ToolboxMCPRef, 0, len(bindings))
	seenBindings := make(map[string]int, len(bindings))
	for _, ref := range bindings {
		normalized := ref.Normalize()
		if normalized.BindingID == "" {
			continue
		}
		key := strings.ToLower(normalized.BindingID)
		if existing, exists := seenBindings[key]; exists {
			// The same binding listed twice is a merge, not a collision: both
			// entries authorize operations from one approved binding, so the
			// union is exactly what the user selected. Required wins because it
			// is the stricter readiness claim; an inherited all-tools entry
			// absorbs the subset because it is the wider of the two.
			merged := &normalizedBindings[existing]
			merged.Required = merged.Required || normalized.Required
			merged.InheritsBindingTools = merged.InheritsBindingTools || normalized.InheritsBindingTools
			if merged.InheritsBindingTools {
				merged.AllowedTools = nil
				continue
			}
			merged.AllowedTools = normalizeToolNames(
				append(append([]string(nil), merged.AllowedTools...), normalized.AllowedTools...),
			)
			continue
		}
		seenBindings[key] = len(normalizedBindings)
		normalizedBindings = append(normalizedBindings, normalized)
	}
	sort.SliceStable(normalizedBindings, func(i, j int) bool {
		return strings.ToLower(normalizedBindings[i].BindingID) < strings.ToLower(normalizedBindings[j].BindingID)
	})

	if len(normalizedSkills) == 0 {
		normalizedSkills = nil
	}
	if len(normalizedBindings) == 0 {
		normalizedBindings = nil
	}
	return normalizedSkills, normalizedBindings
}

// ToolboxSourceCollision reports one skill identity that a Toolbox draws from
// two different sources (FR-6).
type ToolboxSourceCollision struct {
	// CapabilityID is the normalized skill identity in conflict.
	CapabilityID string `json:"capability_id"`
	// Sources lists the conflicting entries in the order they appear, so the
	// UI can present the user a concrete choice rather than a warning.
	Sources []ToolboxSkillRef `json:"sources"`
}

// DetectToolboxSourceCollisions returns every skill identity that appears under
// more than one source or binding.
//
// This is surfaced to the user rather than resolved automatically. The legacy
// runtime silently preferred the agent-learned source, which meant binding a
// same-named skill to a workspace produced no visible effect and no explanation
// (FR-6, FR-44).
func DetectToolboxSourceCollisions(skills []ToolboxSkillRef) []ToolboxSourceCollision {
	byIdentity := make(map[string][]ToolboxSkillRef, len(skills))
	order := make([]string, 0, len(skills))
	for _, ref := range skills {
		key := NormalizeToolboxCapabilityID(ref.CapabilityID)
		if key == "" {
			continue
		}
		if _, exists := byIdentity[key]; !exists {
			order = append(order, key)
		}
		byIdentity[key] = append(byIdentity[key], ref)
	}

	var collisions []ToolboxSourceCollision
	for _, key := range order {
		entries := byIdentity[key]
		if len(entries) < 2 {
			continue
		}
		collisions = append(collisions, ToolboxSourceCollision{CapabilityID: key, Sources: entries})
	}
	return collisions
}

// ValidateToolboxContent reports why a version's capability entries cannot be
// saved, or nil. It assumes the entries have already been normalized.
func ValidateToolboxContent(skills []ToolboxSkillRef, bindings []ToolboxMCPRef) error {
	for _, ref := range skills {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	for _, ref := range bindings {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	if collisions := DetectToolboxSourceCollisions(skills); len(collisions) > 0 {
		return fmt.Errorf("%w: %s", ErrToolboxSourceCollision, collisions[0].CapabilityID)
	}
	return nil
}

// ToolboxCapacity is an agent's active-skill capacity position for one Toolbox
// (FR-33, FR-55–FR-59).
type ToolboxCapacity struct {
	// Used is the deduplicated count of space-consuming (non-core) skills.
	Used int `json:"used"`
	// Capacity is the agent's stage-based skill-space allowance. Zero means
	// unresolvable, in which case no cap is enforced — a missing agent must
	// never block an edit.
	Capacity int `json:"capacity"`
	// ExpertMode lifts the cap entirely. It is a CAPACITY override and nothing
	// more: it never bypasses scopes, allowlists, trust, classification,
	// credentials, approvals, or Goal autonomy (FR-60–FR-62).
	ExpertMode bool `json:"expert_mode,omitempty"`
	// Full reports that no further space-consuming skill may be added.
	Full bool `json:"full"`
	// Grandfathered marks a Toolbox that is ALREADY over capacity — migrated
	// from a pre-Toolbox agent that had more skills than its stage allows. It
	// keeps every skill it has; only additions are blocked (FR-33).
	Grandfathered bool `json:"grandfathered,omitempty"`
}

// EvaluateToolboxCapacity computes the capacity position of a skill selection.
//
// The rule that matters is the grandfathering one: an over-capacity migrated
// Toolbox is preserved exactly, not trimmed. Trimming would silently remove
// capabilities the agent had yesterday, which is the opposite of what migration
// promises. It may remove skills or switch Toolboxes freely; what it may not do
// is add another one (FR-33).
func EvaluateToolboxCapacity(skills []ToolboxSkillRef, capacity int, expertMode bool) ToolboxCapacity {
	used := countToolboxSkillSpaces(skills)
	position := ToolboxCapacity{Used: used, Capacity: capacity, ExpertMode: expertMode}
	if expertMode || capacity <= 0 {
		return position
	}
	position.Grandfathered = used > capacity
	position.Full = used >= capacity
	return position
}

// EnforceToolboxCapacity rejects an edit that ADDS a space-consuming skill
// beyond the agent's capacity, while always permitting an edit that removes,
// reorders, or leaves the skill count alone.
//
// Comparing against the CURRENT selection rather than against the cap alone is
// what makes a grandfathered over-capacity Toolbox still editable: its owner
// can drop a skill or swap two without first having to get under a limit their
// agent has never been under.
func EnforceToolboxCapacity(current, proposed []ToolboxSkillRef, capacity int, expertMode bool) error {
	if expertMode || capacity <= 0 {
		return nil
	}
	proposedUsed := countToolboxSkillSpaces(proposed)
	if proposedUsed <= capacity || proposedUsed <= countToolboxSkillSpaces(current) {
		return nil
	}
	return fmt.Errorf("%w: this agent has %d active skill spaces and the change needs %d. Remove a skill, or turn on expert mode",
		ErrToolboxFull, capacity, proposedUsed)
}

// ValidateToolboxMetadata reports why a Toolbox's user-authored metadata cannot
// be saved, or nil (FR-9, FR-41, FR-42).
func ValidateToolboxMetadata(name, description string) error {
	normalized := NormalizeToolboxName(name)
	if normalized == "" {
		return fmt.Errorf("toolbox name is required")
	}
	if len([]rune(normalized)) > MaxToolboxNameLength {
		return fmt.Errorf("toolbox name must be %d characters or fewer", MaxToolboxNameLength)
	}
	if len([]rune(strings.TrimSpace(description))) > MaxToolboxDescriptionLength {
		return fmt.Errorf("toolbox description must be %d characters or fewer", MaxToolboxDescriptionLength)
	}
	return nil
}
