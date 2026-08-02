package workspace

import (
	"sort"
	"strings"
)

// The Workshop inventory: everything a user may put into a Toolbox, grouped by
// where it comes from (PRD FR-5, FR-43, FR-47–FR-50).
//
// The grouping is the point. Ori knows about far more capabilities than any one
// workspace has approved, and the pre-Toolbox editor showed those two sets in
// one undifferentiated list — so "add this skill" sometimes meant "select an
// approved thing" and sometimes meant "go install something". Separating
// `workspace_provided` from `global_library` makes the difference visible
// before the click rather than after it (FR-43).
//
// Nothing in this file installs, connects, enables, trusts, or classifies
// anything. It is a read model. Adding an unavailable capability to a Toolbox
// records a REQUIREMENT and leaves the draft inert; the user completes setup
// through the existing capability catalog, which is a separate, explicit flow
// (FR-45, FR-46).

// Workshop item kinds.
const (
	WorkshopKindSkill = "skill"
	WorkshopKindMCP   = "mcp"
)

// WorkshopSourceGlobalLibrary marks a capability Ori knows about that this
// workspace has NOT approved. It is deliberately not one of the ToolboxSource*
// constants: a Toolbox entry can never carry it, because a saved entry always
// names an approved source or an explicit unmet requirement (FR-43, FR-46).
const WorkshopSourceGlobalLibrary = "global_library"

// WorkshopItem is one selectable (or locked, or unavailable) capability.
type WorkshopItem struct {
	Kind         string `json:"kind"`
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name"`
	Source       string `json:"source"`
	BindingID    string `json:"binding_id,omitempty"`
	// OwnerCapabilityID names the installed Workspace Capability that supplied
	// this item, when one did — provenance for grouping and for recomputing
	// readiness when that capability's resources change. It never implies the
	// capability should be installed or activated (FR-32).
	OwnerCapabilityID string `json:"owner_capability_id,omitempty"`

	// Available reports whether this item can be put to work right now.
	Available bool `json:"available"`
	// Locked marks a core capability: always present, never deselectable, and
	// consuming no skill space (FR-47, FR-48).
	Locked bool `json:"locked,omitempty"`
	// Selected reports whether the Toolbox being edited currently includes it.
	Selected bool `json:"selected,omitempty"`
	// Required reports the requirement level of a selected entry (FR-14).
	Required bool `json:"required,omitempty"`
	// UnavailableReason explains, in plain language, what is missing. Empty
	// when Available.
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	// ConsumesSkillSpace states whether selecting this uses one of the agent's
	// active-skill spaces. False for core capabilities and every MCP item
	// (FR-58, FR-59).
	ConsumesSkillSpace bool `json:"consumes_skill_space"`

	// --- Skill detail (FR-49) ---

	// Summary is the plain-language description of what this skill adds.
	Summary string `json:"summary,omitempty"`
	// Prompt is the skill's underlying prompt text, shown behind an expander so
	// the user can check what it actually instructs rather than trusting the
	// summary (FR-49).
	Prompt string `json:"prompt,omitempty"`
	// Config is the workspace binding's configuration for this skill.
	Config map[string]any `json:"config,omitempty"`
	// SkillAllowedTools / SkillDisallowedTools are the skill's own tool policy.
	SkillAllowedTools    []string `json:"skill_allowed_tools,omitempty"`
	SkillDisallowedTools []string `json:"skill_disallowed_tools,omitempty"`
	// Trusted reflects the workspace binding's trust decision. Displayed, never
	// changed from here (FR-45).
	Trusted bool `json:"trusted,omitempty"`

	// --- MCP detail (FR-50) ---

	ServerName string `json:"server_name,omitempty"`
	Alias      string `json:"alias,omitempty"`
	// Connected reflects the binding's enabled state — the connection half of
	// readiness.
	Connected bool `json:"connected,omitempty"`
	// Scope is the binding's scope (filesystem roots, account, etc.).
	Scope map[string]any `json:"scope,omitempty"`
	// ExposedTools lists the operations the binding permits, or nil when it
	// permits everything — in which case ExposesAllTools is true and the user
	// must pin an explicit subset before the Toolbox is complete (FR-13).
	ExposedTools    []string `json:"exposed_tools,omitempty"`
	ExposesAllTools bool     `json:"exposes_all_tools,omitempty"`
	// SelectedTools is the subset this Toolbox currently exposes.
	SelectedTools []string `json:"selected_tools,omitempty"`
	// DefaultSideEffect and ToolRisks classify what the operations DO —
	// read, write, or external (FR-50).
	DefaultSideEffect string            `json:"default_side_effect,omitempty"`
	ToolRisks         map[string]string `json:"tool_risks,omitempty"`
	// UnclassifiedTools lists operations with no side-effect classification.
	// They fail closed under a Goal's autonomy gate until classified, so they
	// must be visible rather than silently absent (FR-50, FR-159).
	UnclassifiedTools []string `json:"unclassified_tools,omitempty"`
	// NativeCapability marks a binding realized by one of Ori's own
	// capabilities (a mailbox) rather than an MCP server. It has no server to
	// launch and no discoverable tool list.
	NativeCapability bool `json:"native_capability,omitempty"`
}

// WorkshopInventory is the complete grouped read model for one editing session.
type WorkshopInventory struct {
	WorkspaceID     string `json:"workspace_id"`
	AgentInstanceID string `json:"agent_instance_id,omitempty"`
	AgentName       string `json:"agent_name,omitempty"`
	ToolboxID       string `json:"toolbox_id,omitempty"`
	ToolboxVersion  int64  `json:"toolbox_version,omitempty"`

	// Core is locked and always present (FR-47).
	Core []WorkshopItem `json:"core"`
	// AgentLearned is the agent's own Skill Collection: selectable, and
	// available in every workspace this agent is attached to (FR-3).
	AgentLearned []WorkshopItem `json:"agent_learned"`
	// WorkspaceProvided is what this workspace's Workshop has approved (FR-43).
	WorkspaceProvided []WorkshopItem `json:"workspace_provided"`
	// GlobalLibrary is what Ori knows about but this workspace has not
	// approved. Selectable only through the explicit **Add requirement** action
	// (FR-43, FR-46).
	GlobalLibrary []WorkshopItem `json:"global_library"`

	// Requirements are entries the Toolbox names that resolve to nothing right
	// now — an unmet **Add requirement**, or a binding that has since been
	// removed. They keep the saved recipe intact and drive `Missing capability`
	// (FR-14, FR-46).
	Requirements []WorkshopItem `json:"requirements,omitempty"`

	// Collisions are skill identities the Toolbox draws from two sources. The
	// user resolves them; Ori does not pick (FR-6, FR-44).
	Collisions []ToolboxSourceCollision `json:"collisions,omitempty"`

	Capacity ToolboxCapacity `json:"capacity"`
}

// ToolboxLibraryItem is one capability Ori knows about globally.
type ToolboxLibraryItem struct {
	// Name is the skill name or MCP server template name.
	Name string
	// Summary is a short plain-language description, when the source has one.
	Summary string
	// SetupHint explains what adding it would require, e.g. "needs an MCP
	// server binding in this workspace".
	SetupHint string
}

// ToolboxLibrarySource supplies the globally known capabilities that this
// workspace has not approved.
//
// It is an interface rather than a direct dependency because the skill manager
// and the MCP config manager live outside this package, and a workspace must
// remain readable when neither is wired (tests, and the workspace store used
// standalone).
type ToolboxLibrarySource interface {
	ListLibrarySkills() []ToolboxLibraryItem
	ListLibraryMCPServers() []ToolboxLibraryItem
}

// BuildWorkshopInventory assembles the grouped read model for one agent
// instance, optionally against a specific Toolbox version being edited.
//
// learned may be nil when the skill source is unavailable; the agent-learned
// group is then empty rather than wrong. library may be nil, in which case the
// global-library group is omitted — showing nothing is honest, whereas showing
// a partial library would misrepresent what Ori can offer.
func BuildWorkshopInventory(
	ws *Workspace,
	instance *AgentInstance,
	recipe *ToolboxRecipe,
	learned []ResolvedSkill,
	library ToolboxLibrarySource,
	capacity int,
	expertMode bool,
) WorkshopInventory {
	inventory := WorkshopInventory{WorkspaceID: ws.ID}
	if instance != nil {
		inventory.AgentInstanceID = instance.ID
		inventory.AgentName = instance.Name
	}

	selectedSkills := make(map[string]ToolboxSkillRef)
	selectedBindings := make(map[string]ToolboxMCPRef)
	if recipe != nil {
		inventory.ToolboxVersion = recipe.Version
		for _, ref := range recipe.Skills {
			selectedSkills[ref.CapabilityID] = ref
		}
		for _, ref := range recipe.MCPBindings {
			selectedBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))] = ref
		}
		inventory.Collisions = DetectToolboxSourceCollisions(recipe.Skills)
		inventory.Capacity = EvaluateToolboxCapacity(recipe.Skills, capacity, expertMode)
	} else {
		inventory.Capacity = EvaluateToolboxCapacity(nil, capacity, expertMode)
	}

	ownerByResource := ownedResourceCapabilityIndex(ws)

	// --- Core: always present, never deselectable, no skill space (FR-47). ---
	if synthesized := synthesizeFilesystemBinding(ws.GetMCPBindings(), ws); synthesized != nil {
		inventory.Core = append(inventory.Core, mcpWorkshopItem(*synthesized, ToolboxSourceCore, nil, ownerByResource))
	}
	for _, binding := range settingsManagedSkillBindings(ws, instanceAgentName(instance)) {
		item := skillWorkshopItem(binding.SkillName, ToolboxSourceCore, binding.ID, nil, nil, ownerByResource)
		item.Summary = "Always available in this workspace."
		inventory.Core = append(inventory.Core, item)
	}
	for i := range inventory.Core {
		inventory.Core[i].Locked = true
		inventory.Core[i].Available = true
		inventory.Core[i].Selected = true
		inventory.Core[i].ConsumesSkillSpace = false
	}

	// --- Agent-learned: the agent's own collection (FR-3, FR-5). ---
	for _, skill := range learned {
		identity := NormalizeToolboxCapabilityID(skill.Name)
		if identity == "" {
			continue
		}
		item := skillWorkshopItem(skill.Name, ToolboxSourceAgentLearned, "", &skill, nil, ownerByResource)
		if selected, ok := selectedSkills[identity]; ok && NormalizeToolboxSource(selected.Source) == ToolboxSourceAgentLearned {
			item.Selected = true
			item.Required = selected.Required
		}
		inventory.AgentLearned = append(inventory.AgentLearned, item)
	}

	// --- Workspace-provided: what this Workshop has approved (FR-43). ---
	for _, binding := range ws.GetSkillBindings() {
		if strings.TrimSpace(binding.SkillName) == "" {
			continue
		}
		bindingCopy := binding
		item := skillWorkshopItem(binding.SkillName, ToolboxSourceWorkspaceProvided, binding.ID, nil, &bindingCopy, ownerByResource)
		if !binding.Enabled {
			item.Available = false
			item.UnavailableReason = "This skill is bound to the workspace but switched off."
		}
		if selected, ok := selectedSkills[item.CapabilityID]; ok &&
			NormalizeToolboxSource(selected.Source) == ToolboxSourceWorkspaceProvided &&
			strings.EqualFold(selected.BindingID, binding.ID) {
			item.Selected = true
			item.Required = selected.Required
		}
		inventory.WorkspaceProvided = append(inventory.WorkspaceProvided, item)
	}
	for _, binding := range ws.GetMCPBindings() {
		selected := selectedBindings[strings.ToLower(strings.TrimSpace(binding.ID))]
		item := mcpWorkshopItem(binding, ToolboxSourceWorkspaceProvided, &selected, ownerByResource)
		if _, ok := selectedBindings[strings.ToLower(strings.TrimSpace(binding.ID))]; ok {
			item.Selected = true
			item.Required = selected.Required
			item.SelectedTools = selected.AllowedTools
		}
		inventory.WorkspaceProvided = append(inventory.WorkspaceProvided, item)
	}

	// --- Requirements: named by the Toolbox but resolving to nothing (FR-14). ---
	knownSkillBindings := make(map[string]struct{})
	for _, binding := range ws.GetSkillBindings() {
		knownSkillBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = struct{}{}
	}
	knownMCPBindings := make(map[string]struct{})
	for _, binding := range ws.GetMCPBindings() {
		knownMCPBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = struct{}{}
	}
	learnedIdentities := make(map[string]struct{}, len(learned))
	for _, skill := range learned {
		learnedIdentities[NormalizeToolboxCapabilityID(skill.Name)] = struct{}{}
	}

	if recipe != nil {
		for _, ref := range recipe.Skills {
			missing := false
			switch NormalizeToolboxSource(ref.Source) {
			case ToolboxSourceWorkspaceProvided:
				_, missing = knownSkillBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))]
				missing = !missing
			case ToolboxSourceAgentLearned:
				_, present := learnedIdentities[ref.CapabilityID]
				// With no skill source wired there is nothing to check against,
				// so an entry is not reported missing on no evidence.
				missing = !present && len(learned) > 0
			}
			if !missing {
				continue
			}
			inventory.Requirements = append(inventory.Requirements, WorkshopItem{
				Kind:               WorkshopKindSkill,
				CapabilityID:       ref.CapabilityID,
				DisplayName:        firstNonEmpty(ref.DisplayName, ref.CapabilityID),
				Source:             NormalizeToolboxSource(ref.Source),
				BindingID:          ref.BindingID,
				OwnerCapabilityID:  ref.OwnerCapabilityID,
				Required:           ref.Required,
				Available:          false,
				ConsumesSkillSpace: ref.ConsumesSkillSpace(),
				UnavailableReason:  "This skill is not available in this workspace yet.",
			})
		}
		for _, ref := range recipe.MCPBindings {
			if _, present := knownMCPBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))]; present {
				continue
			}
			inventory.Requirements = append(inventory.Requirements, WorkshopItem{
				Kind:              WorkshopKindMCP,
				CapabilityID:      ref.BindingID,
				DisplayName:       ref.BindingID,
				Source:            ToolboxSourceWorkspaceProvided,
				BindingID:         ref.BindingID,
				OwnerCapabilityID: ref.OwnerCapabilityID,
				Required:          ref.Required,
				Available:         false,
				SelectedTools:     ref.AllowedTools,
				UnavailableReason: "This connection is not set up in this workspace yet.",
			})
		}
	}

	// --- Global library: known to Ori, not approved here (FR-43, FR-45). ---
	if library != nil {
		approvedSkills := make(map[string]struct{})
		for _, binding := range ws.GetSkillBindings() {
			approvedSkills[NormalizeToolboxCapabilityID(binding.SkillName)] = struct{}{}
		}
		for _, skill := range learned {
			approvedSkills[NormalizeToolboxCapabilityID(skill.Name)] = struct{}{}
		}
		for _, entry := range library.ListLibrarySkills() {
			identity := NormalizeToolboxCapabilityID(entry.Name)
			if identity == "" {
				continue
			}
			if _, approved := approvedSkills[identity]; approved {
				continue
			}
			inventory.GlobalLibrary = append(inventory.GlobalLibrary, WorkshopItem{
				Kind:               WorkshopKindSkill,
				CapabilityID:       identity,
				DisplayName:        strings.TrimSpace(entry.Name),
				Source:             WorkshopSourceGlobalLibrary,
				Available:          false,
				ConsumesSkillSpace: true,
				Summary:            entry.Summary,
				UnavailableReason: firstNonEmpty(entry.SetupHint,
					"Ori knows this skill, but this workspace has not added it yet."),
			})
		}

		approvedServers := make(map[string]struct{})
		for _, binding := range ws.GetMCPBindings() {
			approvedServers[strings.ToLower(strings.TrimSpace(binding.ServerName))] = struct{}{}
		}
		for _, entry := range library.ListLibraryMCPServers() {
			name := strings.TrimSpace(entry.Name)
			if name == "" {
				continue
			}
			if _, approved := approvedServers[strings.ToLower(name)]; approved {
				continue
			}
			inventory.GlobalLibrary = append(inventory.GlobalLibrary, WorkshopItem{
				Kind:         WorkshopKindMCP,
				CapabilityID: strings.ToLower(name),
				DisplayName:  name,
				Source:       WorkshopSourceGlobalLibrary,
				ServerName:   name,
				Available:    false,
				Summary:      entry.Summary,
				UnavailableReason: firstNonEmpty(entry.SetupHint,
					"Ori knows this connection, but this workspace has not set it up yet."),
			})
		}
	}

	sortWorkshopItems(inventory.Core)
	sortWorkshopItems(inventory.AgentLearned)
	sortWorkshopItems(inventory.WorkspaceProvided)
	sortWorkshopItems(inventory.GlobalLibrary)
	sortWorkshopItems(inventory.Requirements)
	return inventory
}

func instanceAgentName(instance *AgentInstance) string {
	if instance == nil {
		return ""
	}
	return instance.Name
}

// settingsManagedSkillBindings mirrors the resolver's core skill synthesis
// without needing a resolver instance, so the read model and the runtime agree
// on what "core" means.
func settingsManagedSkillBindings(ws *Workspace, agentName string) []SkillBinding {
	resolver := &AgentRuntimeResolver{}
	return resolver.resolveSettingsManagedSkillBindings(ws, agentName)
}

func skillWorkshopItem(
	name, source, bindingID string,
	learned *ResolvedSkill,
	binding *SkillBinding,
	ownerByResource map[string]string,
) WorkshopItem {
	item := WorkshopItem{
		Kind:               WorkshopKindSkill,
		CapabilityID:       NormalizeToolboxCapabilityID(name),
		DisplayName:        strings.TrimSpace(name),
		Source:             source,
		BindingID:          bindingID,
		Available:          true,
		ConsumesSkillSpace: source != ToolboxSourceCore,
	}
	if learned != nil {
		item.Summary = strings.TrimSpace(learned.Description)
		item.Prompt = strings.TrimSpace(learned.Prompt)
		item.SkillAllowedTools = learned.AllowedTools
		item.SkillDisallowedTools = learned.DisallowedTools
		item.Trusted = learned.Trusted
	}
	if binding != nil {
		item.Config = binding.Config
		item.Trusted = binding.Trusted
		item.OwnerCapabilityID = ownerByResource[resourceKey(ResourceMCPBinding, binding.ID)]
	}
	return item
}

func mcpWorkshopItem(binding MCPBinding, source string, selected *ToolboxMCPRef, ownerByResource map[string]string) WorkshopItem {
	item := WorkshopItem{
		Kind:               WorkshopKindMCP,
		CapabilityID:       strings.ToLower(strings.TrimSpace(binding.ID)),
		DisplayName:        firstNonEmpty(binding.Alias, binding.ServerName, binding.ID),
		Source:             source,
		BindingID:          binding.ID,
		OwnerCapabilityID:  ownerByResource[resourceKey(ResourceMCPBinding, binding.ID)],
		ServerName:         binding.ServerName,
		Alias:              binding.Alias,
		Connected:          binding.Enabled,
		Available:          binding.Enabled,
		Scope:              binding.Scope,
		DefaultSideEffect:  string(binding.DefaultSideEffect),
		NativeCapability:   binding.IsNativeEmail(),
		ConsumesSkillSpace: false,
	}
	if !binding.Enabled {
		item.UnavailableReason = "This connection is set up but switched off."
	}
	if binding.AllowsAllTools() {
		item.ExposesAllTools = true
	} else {
		item.ExposedTools = append([]string(nil), binding.AllowedTools...)
	}

	if len(binding.ToolOverrides) > 0 {
		item.ToolRisks = make(map[string]string, len(binding.ToolOverrides))
		for tool, effect := range binding.ToolOverrides {
			item.ToolRisks[tool] = string(effect)
		}
	}
	// An operation with no classification fails closed under a Goal's autonomy
	// gate, so it has to be visible here rather than quietly missing (FR-159).
	if binding.DefaultSideEffect == "" {
		for _, tool := range item.ExposedTools {
			if _, classified := binding.ToolOverrides[tool]; !classified {
				item.UnclassifiedTools = append(item.UnclassifiedTools, tool)
			}
		}
		sort.Strings(item.UnclassifiedTools)
	}

	if selected != nil && strings.TrimSpace(selected.BindingID) != "" {
		item.SelectedTools = selected.AllowedTools
	}
	return item
}

func sortWorkshopItems(items []WorkshopItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return strings.ToLower(items[i].DisplayName) < strings.ToLower(items[j].DisplayName)
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
