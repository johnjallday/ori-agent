package workspace

import (
	"regexp"
	"strings"
	"time"
)

// RuntimeRequirementsSchemaVersion is the only blueprint runtime-requirements
// schema understood by this build. A newer schema must be migrated explicitly;
// it must never be interpreted as an empty or already-configured contract.
const RuntimeRequirementsSchemaVersion = 1

// Public contract bounds keep hand-authored manifests, portable workspace
// snapshots, and creation/setup UI finite. Text lengths are measured in encoded
// bytes, matching JSON's storage cost and the Setup Wizard contract.
const (
	MaxRuntimeOperatingModes    = 8
	MaxRuntimeRequirements      = 24
	MaxRuntimeIdentifierLength  = 64
	MaxRuntimeLabelLength       = 120
	MaxRuntimeDescriptionLength = 1000
	MaxRuntimeDisclosureLength  = 2000
)

var runtimeIdentifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// NormalizeRuntimeIdentifier canonicalizes a persisted mode, requirement, or
// adapter lookup key. Invalid identifiers normalize to empty, never to a
// near-match that could activate a different requirement.
func NormalizeRuntimeIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > MaxRuntimeIdentifierLength || !runtimeIdentifierPattern.MatchString(value) {
		return ""
	}
	return value
}

// RuntimeRequirementsContract is the normalized, portable runtime contract a
// workspace snapshots from its originating blueprint. It is declarative data
// only. In particular, it cannot carry a path, endpoint, command, script,
// request, module, or custom renderer. Adapter is only a lookup key into a
// compiled server registry.
type RuntimeRequirementsContract struct {
	SchemaVersion  int                    `json:"schema_version"`
	OperatingModes []RuntimeOperatingMode `json:"operating_modes"`
	Requirements   []RuntimeRequirement   `json:"requirements"`
}

// RuntimeOperatingMode is one supported way to use a workspace. Requires names
// requirement keys from the same RuntimeRequirementsContract; it cannot name a
// provider, executable, path, or endpoint. A contract with one mode selects it
// implicitly, while a contract with multiple modes records the user's choice in
// WorkspaceRuntimeState.
type RuntimeOperatingMode struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Requires    []string `json:"requires,omitempty"`
}

// RuntimeRequirement declares one abstract capability needed by one or more
// operating modes. Label, Description, and Disclosure are untrusted
// user-facing text. Adapter is an allowlisted compiled lookup key, never code
// or a dynamic module selector.
type RuntimeRequirement struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Disclosure  string `json:"disclosure,omitempty"`
	Adapter     string `json:"adapter"`
}

// CloneRuntimeRequirementsContract returns a deep copy of a contract. Every
// boundary that stores or returns a blueprint snapshot uses this helper so a
// later template edit cannot rewrite an existing workspace's requirements.
func CloneRuntimeRequirementsContract(contract *RuntimeRequirementsContract) *RuntimeRequirementsContract {
	if contract == nil {
		return nil
	}
	clone := *contract
	if len(contract.OperatingModes) > 0 {
		clone.OperatingModes = make([]RuntimeOperatingMode, len(contract.OperatingModes))
		for i, mode := range contract.OperatingModes {
			clone.OperatingModes[i] = mode
			clone.OperatingModes[i].Requires = append([]string(nil), mode.Requires...)
		}
	} else {
		clone.OperatingModes = nil
	}
	clone.Requirements = append([]RuntimeRequirement(nil), contract.Requirements...)
	return &clone
}

// StructurallyValid reports whether the portable part of a contract is safe to
// resolve. The project-template authoring layer adds the compiled-adapter
// allowlist and detailed diagnostics; this check protects hand-edited or
// older workspace snapshots. Any malformed identifier/reference invalidates
// the entire contract rather than silently turning an assisted mode into a
// requirement-free one.
func (c *RuntimeRequirementsContract) StructurallyValid() bool {
	if c == nil || c.SchemaVersion != RuntimeRequirementsSchemaVersion {
		return false
	}
	if len(c.OperatingModes) == 0 || len(c.OperatingModes) > MaxRuntimeOperatingModes || len(c.Requirements) > MaxRuntimeRequirements {
		return false
	}

	requirementKeys := make(map[string]struct{}, len(c.Requirements))
	for _, requirement := range c.Requirements {
		key := NormalizeRuntimeIdentifier(requirement.Key)
		adapter := NormalizeRuntimeIdentifier(requirement.Adapter)
		if key == "" || adapter == "" || !runtimeTextValid(requirement.Label, MaxRuntimeLabelLength, true) ||
			!runtimeTextValid(requirement.Description, MaxRuntimeDescriptionLength, true) ||
			!runtimeTextValid(requirement.Disclosure, MaxRuntimeDisclosureLength, false) {
			return false
		}
		if _, duplicate := requirementKeys[key]; duplicate {
			return false
		}
		requirementKeys[key] = struct{}{}
	}

	modeIDs := make(map[string]struct{}, len(c.OperatingModes))
	for _, mode := range c.OperatingModes {
		id := NormalizeRuntimeIdentifier(mode.ID)
		if id == "" || !runtimeTextValid(mode.Label, MaxRuntimeLabelLength, true) || !runtimeTextValid(mode.Description, MaxRuntimeDescriptionLength, true) {
			return false
		}
		if _, duplicate := modeIDs[id]; duplicate {
			return false
		}
		modeIDs[id] = struct{}{}

		seenRequirements := make(map[string]struct{}, len(mode.Requires))
		for _, rawKey := range mode.Requires {
			key := NormalizeRuntimeIdentifier(rawKey)
			if key == "" {
				return false
			}
			if _, duplicate := seenRequirements[key]; duplicate {
				return false
			}
			if _, declared := requirementKeys[key]; !declared {
				return false
			}
			seenRequirements[key] = struct{}{}
		}
	}
	return true
}

func runtimeTextValid(value string, max int, required bool) bool {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return false
	}
	if len(value) > max {
		return false
	}
	for _, r := range value {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Mode resolves a mode by normalized stable ID and returns a defensive copy.
// A malformed contract resolves nothing.
func (c *RuntimeRequirementsContract) Mode(id string) (RuntimeOperatingMode, bool) {
	id = NormalizeRuntimeIdentifier(id)
	if id == "" || !c.StructurallyValid() {
		return RuntimeOperatingMode{}, false
	}
	for _, mode := range c.OperatingModes {
		if NormalizeRuntimeIdentifier(mode.ID) != id {
			continue
		}
		mode.Requires = append([]string(nil), mode.Requires...)
		return mode, true
	}
	return RuntimeOperatingMode{}, false
}

// ImplicitMode returns the only mode in a valid one-mode contract. Multiple
// modes require a persisted user selection and therefore have no implicit
// answer.
func (c *RuntimeRequirementsContract) ImplicitMode() (RuntimeOperatingMode, bool) {
	if !c.StructurallyValid() || len(c.OperatingModes) != 1 {
		return RuntimeOperatingMode{}, false
	}
	return c.Mode(c.OperatingModes[0].ID)
}

// Requirement resolves a requirement by normalized stable key. A malformed
// contract resolves nothing, preventing blank/duplicate declarations from
// becoming active through a hand-edited snapshot.
func (c *RuntimeRequirementsContract) Requirement(key string) (RuntimeRequirement, bool) {
	key = NormalizeRuntimeIdentifier(key)
	if key == "" || !c.StructurallyValid() {
		return RuntimeRequirement{}, false
	}
	for _, requirement := range c.Requirements {
		if NormalizeRuntimeIdentifier(requirement.Key) == key {
			return requirement, true
		}
	}
	return RuntimeRequirement{}, false
}

// RequirementsForMode resolves a mode's ordered requirements. Both the slice
// and its values are detached from the contract.
func (c *RuntimeRequirementsContract) RequirementsForMode(modeID string) ([]RuntimeRequirement, bool) {
	mode, ok := c.Mode(modeID)
	if !ok {
		return nil, false
	}
	if len(mode.Requires) == 0 {
		return nil, true
	}
	out := make([]RuntimeRequirement, 0, len(mode.Requires))
	for _, key := range mode.Requires {
		requirement, found := c.Requirement(key)
		if !found {
			return nil, false
		}
		out = append(out, requirement)
	}
	return out, true
}

// Durable runtime-configuration states. These describe whether durable setup
// remains valid and are deliberately independent of whether an external
// application happens to be online right now.
const (
	RuntimeConfigurationNotStarted     = "not_started"
	RuntimeConfigurationInProgress     = "in_progress"
	RuntimeConfigurationConfigured     = "configured"
	RuntimeConfigurationNeedsAttention = "needs_attention"
)

// NormalizeRuntimeConfigurationState canonicalizes persisted durable state. A
// blank value has not started; every unknown value is unfinished, never
// configured.
func NormalizeRuntimeConfigurationState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "":
		return RuntimeConfigurationNotStarted
	case RuntimeConfigurationNotStarted:
		return RuntimeConfigurationNotStarted
	case RuntimeConfigurationConfigured:
		return RuntimeConfigurationConfigured
	case RuntimeConfigurationNeedsAttention:
		return RuntimeConfigurationNeedsAttention
	default:
		return RuntimeConfigurationInProgress
	}
}

// RuntimeRequirementState is the small amount of durable state Ori persists
// for one requirement. Current connectivity is never stored here. Historical
// verification timestamps survive an offline or wrong-target live result.
type RuntimeRequirementState struct {
	RequirementKey     string     `json:"requirement_key"`
	ConfigurationState string     `json:"configuration_state"`
	FirstVerifiedAt    *time.Time `json:"first_verified_at,omitempty"`
	LastVerifiedAt     *time.Time `json:"last_verified_at,omitempty"`
}

// RuntimeCapabilityGrant records one explicit, revocable grant for one
// capability and one stable workspace-agent instance. The absence of a record,
// a zero GrantedAt, or a non-nil RevokedAt all mean no current authority.
// Canonical filesystem/network scope is resolved by trusted compiled code and
// is intentionally absent from this portable record.
type RuntimeCapabilityGrant struct {
	CapabilityKey   string     `json:"capability_key"`
	AgentInstanceID string     `json:"agent_instance_id"`
	GrantedAt       time.Time  `json:"granted_at,omitzero"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

// Active reports whether this record currently grants its named capability.
// It deliberately says nothing about whether the capability remains declared,
// the mode still requires it, or the agent still exists; callers must establish
// those independent facts before constructing an execution scope.
func (g RuntimeCapabilityGrant) Active() bool {
	return NormalizeRuntimeIdentifier(g.CapabilityKey) != "" && strings.TrimSpace(g.AgentInstanceID) != "" && !g.GrantedAt.IsZero() && g.RevokedAt == nil
}

// WorkspaceRuntimeState contains only authoritative durable choices and
// history. It never persists a connected flag, a local path, an adapter result,
// or browser-supplied readiness. RequirementStates and Grants are bounded by
// the workspace's snapshotted contract and defensively cloned by its accessors.
type WorkspaceRuntimeState struct {
	SelectedModeID    string                    `json:"selected_mode_id,omitempty"`
	RequirementStates []RuntimeRequirementState `json:"requirement_states,omitempty"`
	Grants            []RuntimeCapabilityGrant  `json:"grants,omitempty"`
}

// CloneWorkspaceRuntimeState returns a deep copy of durable runtime state.
func CloneWorkspaceRuntimeState(state *WorkspaceRuntimeState) *WorkspaceRuntimeState {
	if state == nil {
		return nil
	}
	clone := *state
	clone.RequirementStates = make([]RuntimeRequirementState, 0, len(state.RequirementStates))
	for _, requirement := range state.RequirementStates {
		requirement.FirstVerifiedAt = cloneTime(requirement.FirstVerifiedAt)
		requirement.LastVerifiedAt = cloneTime(requirement.LastVerifiedAt)
		clone.RequirementStates = append(clone.RequirementStates, requirement)
	}
	if len(clone.RequirementStates) == 0 {
		clone.RequirementStates = nil
	}
	clone.Grants = make([]RuntimeCapabilityGrant, 0, len(state.Grants))
	for _, grant := range state.Grants {
		grant.RevokedAt = cloneTime(grant.RevokedAt)
		clone.Grants = append(clone.Grants, grant)
	}
	if len(clone.Grants) == 0 {
		clone.Grants = nil
	}
	return &clone
}

// GetRuntimeState returns a defensive copy of the workspace's durable runtime
// state, or nil when it has never selected a mode or configured a requirement.
func (w *Workspace) GetRuntimeState() *WorkspaceRuntimeState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return CloneWorkspaceRuntimeState(w.RuntimeState)
}

// SetRuntimeState records durable runtime state. It normalizes stable keys and
// configuration states, and never turns unknown input into configured. Invalid
// requirement-state keys are dropped; invalid grant records remain
// non-authoritative because Active returns false.
func (w *Workspace) SetRuntimeState(state *WorkspaceRuntimeState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if state == nil {
		w.RuntimeState = nil
		w.UpdatedAt = time.Now()
		return
	}

	clone := CloneWorkspaceRuntimeState(state)
	clone.SelectedModeID = NormalizeRuntimeIdentifier(clone.SelectedModeID)
	states := clone.RequirementStates[:0]
	seenStates := make(map[string]int, len(clone.RequirementStates))
	for _, requirement := range clone.RequirementStates {
		requirement.RequirementKey = NormalizeRuntimeIdentifier(requirement.RequirementKey)
		if requirement.RequirementKey == "" {
			continue
		}
		requirement.ConfigurationState = NormalizeRuntimeConfigurationState(requirement.ConfigurationState)
		if existing, duplicate := seenStates[requirement.RequirementKey]; duplicate {
			// Conflicting duplicate persistence can never provide configured proof.
			states[existing].ConfigurationState = RuntimeConfigurationInProgress
			states[existing].FirstVerifiedAt = nil
			states[existing].LastVerifiedAt = nil
			continue
		}
		seenStates[requirement.RequirementKey] = len(states)
		states = append(states, requirement)
	}
	clone.RequirementStates = states
	for i := range clone.Grants {
		clone.Grants[i].CapabilityKey = NormalizeRuntimeIdentifier(clone.Grants[i].CapabilityKey)
		clone.Grants[i].AgentInstanceID = strings.TrimSpace(clone.Grants[i].AgentInstanceID)
		if clone.Grants[i].CapabilityKey == "" || clone.Grants[i].AgentInstanceID == "" {
			clone.Grants[i].GrantedAt = time.Time{}
		}
	}
	w.RuntimeState = clone
	w.UpdatedAt = time.Now()
}
