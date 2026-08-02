package workspace

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Workspace-owned persistence for named Toolboxes and their per-instance
// assignments (FR-22, FR-23).
//
// The canonical workspace record is the source of truth. Toolboxes live on the
// Workspace struct rather than in a side store precisely so they inherit the
// workspace's monotonic Version and lost-write protection for free: a Toolbox
// switch and a concurrent binding edit contend on the same record, which is the
// behavior FR-23 asks for and the behavior a separate store would have to
// re-implement badly.
//
// Every exported method here takes the workspace lock and works on clones, in
// the same style as workspace_skills.go / workspace_mcp.go. Unexported helpers
// suffixed `Locked` assume the caller already holds the lock.

// GetToolboxes returns a copy of every Toolbox defined in this workspace,
// ordered by name for stable presentation.
func (w *Workspace) GetToolboxes() []ToolboxDefinition {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.Toolboxes) == 0 {
		return nil
	}
	out := make([]ToolboxDefinition, len(w.Toolboxes))
	for i := range w.Toolboxes {
		out[i] = w.Toolboxes[i].Clone()
	}
	sort.SliceStable(out, func(i, j int) bool {
		return toolboxNameKey(out[i].Name) < toolboxNameKey(out[j].Name)
	})
	return out
}

// GetToolbox returns a copy of one Toolbox by ID.
func (w *Workspace) GetToolbox(toolboxID string) (*ToolboxDefinition, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	idx := w.findToolboxIndexLocked(toolboxID)
	if idx < 0 {
		return nil, false
	}
	cp := w.Toolboxes[idx].Clone()
	return &cp, true
}

// FindToolboxByName returns a copy of the Toolbox with the given name, compared
// case-insensitively (FR-41).
func (w *Workspace) FindToolboxByName(name string) (*ToolboxDefinition, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	key := toolboxNameKey(name)
	if key == "" {
		return nil, false
	}
	for i := range w.Toolboxes {
		if toolboxNameKey(w.Toolboxes[i].Name) == key {
			cp := w.Toolboxes[i].Clone()
			return &cp, true
		}
	}
	return nil, false
}

func (w *Workspace) findToolboxIndexLocked(toolboxID string) int {
	normalized := strings.TrimSpace(toolboxID)
	if normalized == "" {
		return -1
	}
	for i := range w.Toolboxes {
		if strings.EqualFold(strings.TrimSpace(w.Toolboxes[i].ID), normalized) {
			return i
		}
	}
	return -1
}

// CreateToolbox saves a new Toolbox as version 1.
//
// It is deliberately not an upsert: creating and versioning are different
// operations with different meanings for run snapshots, and one function that
// silently did either would make "did this edit produce a new version?"
// unanswerable from the call site.
func (w *Workspace) CreateToolbox(def ToolboxDefinition) (*ToolboxDefinition, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	def.ID = strings.TrimSpace(def.ID)
	if def.ID == "" {
		return nil, fmt.Errorf("toolbox ID is required")
	}
	if w.findToolboxIndexLocked(def.ID) >= 0 {
		return nil, fmt.Errorf("toolbox %s already exists", def.ID)
	}

	def.Name = NormalizeToolboxName(def.Name)
	def.Description = strings.TrimSpace(def.Description)
	if err := ValidateToolboxMetadata(def.Name, def.Description); err != nil {
		return nil, err
	}
	if err := w.requireUnusedToolboxNameLocked(def.Name, ""); err != nil {
		return nil, err
	}

	def.WorkspaceID = w.ID
	def.Skills, def.MCPBindings = NormalizeToolboxContent(def.Skills, def.MCPBindings)
	if err := ValidateToolboxContent(def.Skills, def.MCPBindings); err != nil {
		return nil, err
	}
	if err := w.validateToolboxAgainstWorkshopLocked(def.Skills, def.MCPBindings); err != nil {
		return nil, err
	}

	now := time.Now()
	def.Version = 1
	def.History = nil
	def.Status = NormalizeToolboxStatus(def.Status)
	def.Icon = strings.TrimSpace(def.Icon)
	def.Color = strings.TrimSpace(def.Color)
	def.Provenance = strings.TrimSpace(def.Provenance)
	def.Actor = strings.TrimSpace(def.Actor)
	if def.CreatedAt.IsZero() {
		def.CreatedAt = now
	}
	def.UpdatedAt = now

	w.Toolboxes = append(w.Toolboxes, def.Clone())
	w.UpdatedAt = now

	cp := def.Clone()
	return &cp, nil
}

// SaveToolboxVersion records an edit as a NEW version, moving the previous
// current version into immutable history (FR-18, FR-19).
//
// The previous version is never rewritten, because a run snapshot may already
// name it. That is the whole reason edits version rather than mutate.
func (w *Workspace) SaveToolboxVersion(toolboxID string, skills []ToolboxSkillRef, bindings []ToolboxMCPRef, provenance, actor string) (*ToolboxDefinition, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	idx := w.findToolboxIndexLocked(toolboxID)
	if idx < 0 {
		return nil, fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
	}
	current := w.Toolboxes[idx]
	if current.Archived() {
		return nil, fmt.Errorf("%w: %s", ErrToolboxArchived, current.ID)
	}

	normalizedSkills, normalizedBindings := NormalizeToolboxContent(skills, bindings)
	if err := ValidateToolboxContent(normalizedSkills, normalizedBindings); err != nil {
		return nil, err
	}
	if err := w.validateToolboxAgainstWorkshopLocked(normalizedSkills, normalizedBindings); err != nil {
		return nil, err
	}

	now := time.Now()
	updated := current.Clone()
	updated.History = append(updated.History, current.CurrentRecipe())
	updated.Version = current.Version + 1
	updated.Skills = normalizedSkills
	updated.MCPBindings = normalizedBindings
	updated.UpdatedAt = now
	if trimmed := strings.TrimSpace(provenance); trimmed != "" {
		updated.Provenance = trimmed
	}
	if trimmed := strings.TrimSpace(actor); trimmed != "" {
		updated.Actor = trimmed
	}
	// A Toolbox that has been edited is no longer an untouched draft.
	if NormalizeToolboxStatus(updated.Status) == ToolboxStatusDraft {
		updated.Status = ToolboxStatusActive
	}

	w.Toolboxes[idx] = updated.Clone()
	w.UpdatedAt = now

	cp := updated.Clone()
	return &cp, nil
}

// UpdateToolboxMetadata renames or re-decorates a Toolbox without producing a
// new version.
//
// Metadata is intentionally NOT versioned: a run snapshot reproduces
// capabilities, and renaming a Toolbox changes no capability. Versioning a
// rename would fill the audit history with entries that grant nothing (FR-41).
func (w *Workspace) UpdateToolboxMetadata(toolboxID, name, description, icon, color string) (*ToolboxDefinition, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	idx := w.findToolboxIndexLocked(toolboxID)
	if idx < 0 {
		return nil, fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
	}

	normalizedName := NormalizeToolboxName(name)
	trimmedDescription := strings.TrimSpace(description)
	if err := ValidateToolboxMetadata(normalizedName, trimmedDescription); err != nil {
		return nil, err
	}
	if err := w.requireUnusedToolboxNameLocked(normalizedName, w.Toolboxes[idx].ID); err != nil {
		return nil, err
	}

	updated := w.Toolboxes[idx].Clone()
	updated.Name = normalizedName
	updated.Description = trimmedDescription
	updated.Icon = strings.TrimSpace(icon)
	updated.Color = strings.TrimSpace(color)
	updated.UpdatedAt = time.Now()

	w.Toolboxes[idx] = updated.Clone()
	w.UpdatedAt = updated.UpdatedAt

	cp := updated.Clone()
	return &cp, nil
}

// SetToolboxStatus archives or reactivates a Toolbox (FR-20).
func (w *Workspace) SetToolboxStatus(toolboxID, status string) (*ToolboxDefinition, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	idx := w.findToolboxIndexLocked(toolboxID)
	if idx < 0 {
		return nil, fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
	}

	updated := w.Toolboxes[idx].Clone()
	updated.Status = NormalizeToolboxStatus(status)
	updated.UpdatedAt = time.Now()

	w.Toolboxes[idx] = updated.Clone()
	w.UpdatedAt = updated.UpdatedAt

	cp := updated.Clone()
	return &cp, nil
}

// DeleteToolbox removes a Toolbox entirely.
//
// The reference guard is the CALLER's responsibility via ToolboxReferences: a
// delete is refused while an assignment, a recurring Goal, or a future Team
// Setup still names the Toolbox (FR-21), and the caller is the layer that can
// see Goals and Team Setups. This method enforces only the part it can see —
// an active assignment in this workspace.
func (w *Workspace) DeleteToolbox(toolboxID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	idx := w.findToolboxIndexLocked(toolboxID)
	if idx < 0 {
		return fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
	}

	id := w.Toolboxes[idx].ID
	for _, assignment := range w.ToolboxAssignments {
		if strings.EqualFold(strings.TrimSpace(assignment.ToolboxID), strings.TrimSpace(id)) {
			return fmt.Errorf("toolbox %s is assigned to agent instance %s and cannot be deleted", id, assignment.AgentInstanceID)
		}
	}

	w.Toolboxes = append(w.Toolboxes[:idx], w.Toolboxes[idx+1:]...)
	w.UpdatedAt = time.Now()
	return nil
}

// ToolboxReference is one thing that depends on a Toolbox and therefore blocks
// its deletion (FR-21).
type ToolboxReference struct {
	// Kind is "assignment", "recurring_goal", or "team_setup". The last is
	// recognized but never produced in V1 — Team Setups are Phase 3, and the
	// delete guard is written to see them the day they exist rather than being
	// retrofitted then (FR-144–FR-155 boundary).
	Kind string `json:"kind"`
	// ID identifies the referencing record — an agent instance ID for an
	// assignment, a goal ID for a recurring Goal.
	ID string `json:"id"`
	// Label is a human-readable description of the reference for the UI.
	Label string `json:"label,omitempty"`
	// ToolboxVersion is the pinned version, when the reference pins one.
	ToolboxVersion int64 `json:"toolbox_version,omitempty"`
}

// ToolboxReferences lists everything in this workspace that currently depends
// on a Toolbox. An empty result means deletion is safe as far as workspace
// state is concerned.
func (w *Workspace) ToolboxReferences(toolboxID string) []ToolboxReference {
	w.mu.RLock()
	defer w.mu.RUnlock()

	normalized := strings.TrimSpace(toolboxID)
	if normalized == "" {
		return nil
	}

	var refs []ToolboxReference
	for _, assignment := range w.ToolboxAssignments {
		if !strings.EqualFold(strings.TrimSpace(assignment.ToolboxID), normalized) {
			continue
		}
		label := assignment.AgentInstanceID
		for _, instance := range w.AgentInstances {
			if strings.EqualFold(strings.TrimSpace(instance.ID), strings.TrimSpace(assignment.AgentInstanceID)) {
				label = instance.Name
				if instance.InstanceNumber > 1 {
					label = fmt.Sprintf("%s #%d", instance.Name, instance.InstanceNumber)
				}
				break
			}
		}
		refs = append(refs, ToolboxReference{
			Kind:           "assignment",
			ID:             assignment.AgentInstanceID,
			Label:          label,
			ToolboxVersion: assignment.ToolboxVersion,
		})
	}
	return refs
}

func (w *Workspace) requireUnusedToolboxNameLocked(name, exceptToolboxID string) error {
	key := toolboxNameKey(name)
	if key == "" {
		return nil
	}
	except := strings.TrimSpace(exceptToolboxID)
	for i := range w.Toolboxes {
		if except != "" && strings.EqualFold(strings.TrimSpace(w.Toolboxes[i].ID), except) {
			continue
		}
		if toolboxNameKey(w.Toolboxes[i].Name) == key {
			return fmt.Errorf("%w: %q", ErrToolboxNameTaken, NormalizeToolboxName(name))
		}
	}
	return nil
}

// ValidateToolboxAgainstWorkshop reports why a capability selection cannot be
// saved against this workspace's current Workshop, or nil.
//
// It is the enforcement point for the one rule a Toolbox can never bend: a
// Toolbox narrows what a workspace binding already permits and never widens it
// (FR-12). Anything else — a binding that is missing, disabled, or
// unconfigured — is a READINESS problem, not a validation error, because a
// user must be able to save a Toolbox that names a capability they are about
// to set up (FR-14, FR-45).
func (w *Workspace) ValidateToolboxAgainstWorkshop(skills []ToolboxSkillRef, bindings []ToolboxMCPRef) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.validateToolboxAgainstWorkshopLocked(skills, bindings)
}

func (w *Workspace) validateToolboxAgainstWorkshopLocked(_ []ToolboxSkillRef, bindings []ToolboxMCPRef) error {
	if len(bindings) == 0 {
		return nil
	}

	byID := make(map[string]MCPBinding, len(w.MCPBindings))
	for _, binding := range w.MCPBindings {
		byID[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}

	for _, ref := range bindings {
		binding, exists := byID[strings.ToLower(strings.TrimSpace(ref.BindingID))]
		if !exists {
			// A Toolbox may name a binding that does not exist yet; that is
			// `Missing capability` readiness, not invalid content (FR-14).
			continue
		}
		if binding.AllowsAllTools() {
			continue
		}
		permitted := make(map[string]struct{}, len(binding.AllowedTools))
		for _, tool := range binding.AllowedTools {
			permitted[strings.ToLower(strings.TrimSpace(tool))] = struct{}{}
		}
		for _, tool := range ref.AllowedTools {
			if _, ok := permitted[strings.ToLower(strings.TrimSpace(tool))]; !ok {
				return fmt.Errorf("%w: binding %s does not permit tool %q", ErrToolboxWidensAllowedTools, ref.BindingID, tool)
			}
		}
	}
	return nil
}

// ListToolboxAssignments returns a copy of every per-instance assignment.
func (w *Workspace) ListToolboxAssignments() []AgentToolboxAssignment {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.ToolboxAssignments) == 0 {
		return nil
	}
	out := make([]AgentToolboxAssignment, len(w.ToolboxAssignments))
	for i := range w.ToolboxAssignments {
		out[i] = w.ToolboxAssignments[i].Clone()
	}
	return out
}

// GetToolboxAssignment returns the Toolbox assignment for one stable agent
// instance. Lookup is by AgentInstance.ID only — never by agent name, so two
// instances of the same reusable agent stay independent (FR-16, FR-17).
func (w *Workspace) GetToolboxAssignment(agentInstanceID string) (*AgentToolboxAssignment, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	idx := w.findToolboxAssignmentIndexLocked(agentInstanceID)
	if idx < 0 {
		return nil, false
	}
	cp := w.ToolboxAssignments[idx].Clone()
	return &cp, true
}

func (w *Workspace) findToolboxAssignmentIndexLocked(agentInstanceID string) int {
	normalized := strings.TrimSpace(agentInstanceID)
	if normalized == "" {
		return -1
	}
	for i := range w.ToolboxAssignments {
		if strings.EqualFold(strings.TrimSpace(w.ToolboxAssignments[i].AgentInstanceID), normalized) {
			return i
		}
	}
	return -1
}

// SetToolboxAssignment pins a Toolbox version to one stable agent instance,
// retaining the displaced assignment for Undo (FR-15, FR-88).
func (w *Workspace) SetToolboxAssignment(assignment AgentToolboxAssignment) (*AgentToolboxAssignment, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	assignment.AgentInstanceID = strings.TrimSpace(assignment.AgentInstanceID)
	assignment.ToolboxID = strings.TrimSpace(assignment.ToolboxID)
	if assignment.AgentInstanceID == "" {
		return nil, fmt.Errorf("agent instance ID is required")
	}
	if assignment.ToolboxID == "" {
		return nil, fmt.Errorf("toolbox ID is required")
	}
	if !w.hasAgentInstanceLocked(assignment.AgentInstanceID) {
		return nil, fmt.Errorf("agent instance %s is not attached to this workspace", assignment.AgentInstanceID)
	}

	toolboxIdx := w.findToolboxIndexLocked(assignment.ToolboxID)
	if toolboxIdx < 0 {
		return nil, fmt.Errorf("%w: %s", ErrToolboxNotFound, assignment.ToolboxID)
	}
	definition := w.Toolboxes[toolboxIdx]
	if definition.Archived() {
		return nil, fmt.Errorf("%w: %s", ErrToolboxArchived, definition.ID)
	}
	if assignment.ToolboxVersion == 0 {
		assignment.ToolboxVersion = definition.Version
	}
	if _, err := definition.ResolveVersion(assignment.ToolboxVersion); err != nil {
		return nil, err
	}
	// Store the canonical ID casing so later lookups and reference checks agree.
	assignment.ToolboxID = definition.ID

	if assignment.AppliedAt.IsZero() {
		assignment.AppliedAt = time.Now()
	}
	assignment.Provenance = strings.TrimSpace(assignment.Provenance)
	assignment.Actor = strings.TrimSpace(assignment.Actor)

	if idx := w.findToolboxAssignmentIndexLocked(assignment.AgentInstanceID); idx >= 0 {
		previous := w.ToolboxAssignments[idx]
		// Only record an Undo target when the assignment actually changed;
		// re-pinning the same version must not erase the real prior state.
		if !strings.EqualFold(previous.ToolboxID, assignment.ToolboxID) || previous.ToolboxVersion != assignment.ToolboxVersion {
			assignment.Previous = &PriorToolboxAssignment{
				ToolboxID:      previous.ToolboxID,
				ToolboxVersion: previous.ToolboxVersion,
				AppliedAt:      previous.AppliedAt,
				Provenance:     previous.Provenance,
				Actor:          previous.Actor,
			}
		} else if previous.Previous != nil {
			prev := *previous.Previous
			assignment.Previous = &prev
		}
		w.ToolboxAssignments[idx] = assignment.Clone()
	} else {
		w.ToolboxAssignments = append(w.ToolboxAssignments, assignment.Clone())
	}

	w.UpdatedAt = time.Now()
	cp := assignment.Clone()
	return &cp, nil
}

// DeleteToolboxAssignment removes an instance's assignment. Used when an agent
// instance is detached from the workspace.
func (w *Workspace) DeleteToolboxAssignment(agentInstanceID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	idx := w.findToolboxAssignmentIndexLocked(agentInstanceID)
	if idx < 0 {
		return fmt.Errorf("toolbox assignment for agent instance %s not found", agentInstanceID)
	}
	w.ToolboxAssignments = append(w.ToolboxAssignments[:idx], w.ToolboxAssignments[idx+1:]...)
	w.UpdatedAt = time.Now()
	return nil
}

func (w *Workspace) hasAgentInstanceLocked(agentInstanceID string) bool {
	normalized := strings.TrimSpace(agentInstanceID)
	for i := range w.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(w.AgentInstances[i].ID), normalized) {
			return true
		}
	}
	return false
}

// ResolveAssignedToolbox returns the definition and the exact pinned version
// content in force for one stable agent instance.
//
// A missing assignment is reported as ok=false with no error: after migration
// every instance has one, but a workspace being migrated, or an instance added
// in the same request that resolves it, legitimately has none yet.
func (w *Workspace) ResolveAssignedToolbox(agentInstanceID string) (ToolboxDefinition, ToolboxRecipe, bool, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	idx := w.findToolboxAssignmentIndexLocked(agentInstanceID)
	if idx < 0 {
		return ToolboxDefinition{}, ToolboxRecipe{}, false, nil
	}
	assignment := w.ToolboxAssignments[idx]

	toolboxIdx := w.findToolboxIndexLocked(assignment.ToolboxID)
	if toolboxIdx < 0 {
		return ToolboxDefinition{}, ToolboxRecipe{}, false, fmt.Errorf("%w: %s (assigned to %s)", ErrToolboxNotFound, assignment.ToolboxID, agentInstanceID)
	}
	definition := w.Toolboxes[toolboxIdx].Clone()

	recipe, err := definition.ResolveVersion(assignment.ToolboxVersion)
	if err != nil {
		return ToolboxDefinition{}, ToolboxRecipe{}, false, err
	}
	return definition, recipe, true, nil
}
