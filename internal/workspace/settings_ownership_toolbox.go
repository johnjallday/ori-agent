package workspace

import (
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/store"
)

// Toolbox ownership: which skills and connections a context selects, kept as
// two separate facts — the Default Toolbox for direct chat outside workspaces,
// and one workspace instance's pinned Toolbox. Selection is not permission and
// not readiness; every state below that cannot be verified says so.
//
// Nothing here creates an assignment, migrates a legacy selection, registers a
// server, discovers tools or connects anything: it reads the workspace record
// already loaded by the projection (ResolveAssignedToolbox is a read-locked
// selector) and, optionally, the agent's skill collection.

// Toolbox states.
const (
	OwnershipToolboxSelected      = "selected"       // direct: explicit selection with skills
	OwnershipToolboxEmpty         = "empty"          // explicit selection with nothing in it
	OwnershipToolboxLegacy        = "legacy"         // direct: not migrated; every enabled skill applies
	OwnershipToolboxPinned        = "pinned"         // workspace: assignment resolved to a version
	OwnershipToolboxUnassigned    = "unassigned"     // workspace: no assignment yet (legacy merge)
	OwnershipToolboxBroken        = "broken"         // workspace: assignment does not resolve
	OwnershipToolboxNeedsInstance = "needs_instance" // workspace: no single instance selected
	OwnershipToolboxUnavailable   = "unavailable"
	OwnershipToolboxNotApplicable = "not_applicable"
)

// Availability of one selected skill or connection.
const (
	OwnershipAvailable   = "available"
	OwnershipSwitchedOff = "switched_off"
	OwnershipMissing     = "missing"
	OwnershipUnknown     = "unknown"
	OwnershipDisabled    = "disabled" // in the agent's skills but turned off, so it does not run
)

// OwnershipToolboxSkill is one selected skill and whether its source currently
// offers it.
type OwnershipToolboxSkill struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Source       string `json:"source"`
	Required     bool   `json:"required"`
	Availability string `json:"availability"`
}

// OwnershipToolboxConnection is one selected connection with the operations the
// pinned version selects and any the connection no longer allows.
type OwnershipToolboxConnection struct {
	BindingID string   `json:"binding_id"`
	Name      string   `json:"name"`
	Required  bool     `json:"required"`
	State     string   `json:"state"`
	Tools     []string `json:"tools"`
	// AllTools: the entry defers to a connection that allows every operation,
	// so the real operation count is not pinned.
	AllTools bool `json:"all_tools"`
	// NoLongerAllowed lists selected operations the connection's current
	// policy no longer permits; they are not exposed.
	NoLongerAllowed []string `json:"no_longer_allowed"`
	RepoScope       string   `json:"repo_scope,omitempty"`
}

// OwnershipDirectToolbox is the Default Toolbox (direct chat outside
// workspaces).
type OwnershipDirectToolbox struct {
	State     string                  `json:"state"`
	Version   int64                   `json:"version,omitempty"`
	Skills    []OwnershipToolboxSkill `json:"skills"`
	Inventory string                  `json:"inventory,omitempty"`
	Note      string                  `json:"note,omitempty"`
}

// OwnershipWorkspaceToolbox is the selected instance's pinned Toolbox.
type OwnershipWorkspaceToolbox struct {
	State          string                       `json:"state"`
	ToolboxID      string                       `json:"toolbox_id,omitempty"`
	ToolboxName    string                       `json:"toolbox_name,omitempty"`
	PinnedVersion  int64                        `json:"pinned_version,omitempty"`
	CurrentVersion int64                        `json:"current_version,omitempty"`
	Archived       bool                         `json:"archived"`
	Skills         []OwnershipToolboxSkill      `json:"skills"`
	Connections    []OwnershipToolboxConnection `json:"connections"`
	// Core lists capabilities the workspace provides regardless of Toolbox
	// selection, when the workspace record says so.
	Core      []string `json:"core"`
	Inventory string   `json:"inventory,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// OwnershipPermissions explains who owns each restriction. Values are shown
// only where the workspace record holds them; agent-level grants are not
// shown at all (see OwnershipExcluded).
type OwnershipPermissions struct {
	WorkspaceNativeTools string   `json:"workspace_native_tools"`
	Notes                []string `json:"notes"`
}

// skillInventory reads the agent's skill collection once. ok=false means the
// collection could not be read: availability is then unknown, never assumed.
func skillInventory(src SettingsOwnershipSources, agentName string) (map[string]bool, bool) {
	if src.SkillInventory == nil {
		return nil, false
	}
	skills, err := src.SkillInventory(agentName)
	if err != nil {
		return nil, false
	}
	out := make(map[string]bool, len(skills))
	for name, enabled := range skills {
		if id := NormalizeToolboxCapabilityID(name); id != "" {
			out[id] = out[id] || enabled
		}
	}
	return out, true
}

// learnedAvailability: only an enabled skill runs (direct chat and workspace
// Toolboxes both resolve enabled skills), so present-but-disabled is not
// available.
func learnedAvailability(inventory map[string]bool, capabilityID string) string {
	enabled, present := inventory[NormalizeToolboxCapabilityID(capabilityID)]
	switch {
	case !present:
		return OwnershipMissing
	case !enabled:
		return OwnershipDisabled
	default:
		return OwnershipAvailable
	}
}

func inventoryLabel(ok bool) string {
	if ok {
		return "checked"
	}
	return "unavailable"
}

func projectDirectToolbox(src SettingsOwnershipSources, out SettingsOwnership, library store.LibraryRecord) OwnershipDirectToolbox {
	direct := OwnershipDirectToolbox{State: OwnershipToolboxUnavailable, Skills: []OwnershipToolboxSkill{}}
	switch {
	case out.Context.State == OwnershipStateNoLibrary:
		direct.State = OwnershipToolboxNotApplicable
		direct.Note = "This agent belongs to a workspace, so it has no Default Toolbox for direct chat."
		return direct
	case library.State != store.LibraryPresent || library.Definition == nil:
		direct.Note = "The library profile could not be read, so its Default Toolbox is unknown."
		return direct
	}
	toolbox := library.Definition.DefaultToolbox
	if toolbox == nil {
		direct.State = OwnershipToolboxLegacy
		direct.Note = "No Default Toolbox has been chosen yet, so direct chat uses every skill enabled for this agent."
		return direct
	}
	direct.Version = toolbox.Version
	if len(toolbox.Skills) == 0 {
		direct.State = OwnershipToolboxEmpty
		direct.Note = "The Default Toolbox is empty: direct chat uses no skills."
		return direct
	}
	direct.State = OwnershipToolboxSelected
	inventory, ok := skillInventory(src, out.Agent)
	direct.Inventory = inventoryLabel(ok)
	for _, ref := range toolbox.Skills {
		skill := OwnershipToolboxSkill{
			ID: ref.CapabilityID, Name: firstNonEmpty(ref.DisplayName, ref.CapabilityID),
			Source: ToolboxSourceAgentLearned, Availability: OwnershipUnknown,
		}
		if ok {
			skill.Availability = learnedAvailability(inventory, ref.CapabilityID)
		}
		direct.Skills = append(direct.Skills, skill)
	}
	return direct
}

func projectWorkspaceToolbox(src SettingsOwnershipSources, out SettingsOwnership, ws *Workspace, agentName string) OwnershipWorkspaceToolbox {
	view := OwnershipWorkspaceToolbox{
		State: OwnershipToolboxNotApplicable, Skills: []OwnershipToolboxSkill{},
		Connections: []OwnershipToolboxConnection{}, Core: []string{},
	}
	if ws == nil || out.Context.State != OwnershipStateOK {
		return view
	}
	view.Core = coreCapabilityLabels(ws, agentName)
	if out.Instances.State != OwnershipInstanceSelected || out.Instances.SelectedID == "" {
		view.State = OwnershipToolboxNeedsInstance
		view.Note = "Each instance pins its own workspace Toolbox. Choose an instance to see it."
		if out.Instances.State == OwnershipInstanceNone {
			view.State = OwnershipToolboxNotApplicable
			view.Note = "This agent has no instance in this workspace, so no workspace Toolbox applies to it."
		}
		return view
	}
	definition, recipe, assigned, err := ws.ResolveAssignedToolbox(out.Instances.SelectedID)
	switch {
	case err != nil:
		// Never fall forward to the latest version or to all bindings.
		view.State = OwnershipToolboxBroken
		if assignment, ok := ws.GetToolboxAssignment(out.Instances.SelectedID); ok && assignment != nil {
			view.ToolboxID = assignment.ToolboxID
			view.PinnedVersion = assignment.ToolboxVersion
		}
		view.Note = "This instance's Toolbox assignment does not resolve (its Toolbox or pinned version is missing). Ori will not run it with a substitute; it needs repair in the workspace."
		return view
	case !assigned:
		view.State = OwnershipToolboxUnassigned
		view.Note = "This instance has no Toolbox assignment yet. Until the workspace gives it one, it uses the workspace's enabled connections and skills under its connection access settings."
		return view
	}
	view.ToolboxID = definition.ID
	view.ToolboxName = definition.Name
	view.PinnedVersion = recipe.Version
	view.CurrentVersion = definition.Version
	view.Archived = definition.Archived()
	if len(recipe.Skills) == 0 && len(recipe.MCPBindings) == 0 {
		view.State = OwnershipToolboxEmpty
		view.Note = "The pinned Toolbox version selects no skills or connections."
		return view
	}
	view.State = OwnershipToolboxPinned

	skillBindings := make(map[string]SkillBinding)
	for _, binding := range ws.GetSkillBindings() {
		skillBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}
	var inventory map[string]bool
	inventoryOK, inventoryRead := false, false
	for _, ref := range recipe.Skills {
		skill := OwnershipToolboxSkill{
			ID: ref.CapabilityID, Name: firstNonEmpty(ref.DisplayName, ref.CapabilityID),
			Source: NormalizeToolboxSource(ref.Source), Required: ref.Required, Availability: OwnershipUnknown,
		}
		switch skill.Source {
		case ToolboxSourceWorkspaceProvided:
			binding, exists := skillBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))]
			switch {
			case !exists:
				skill.Availability = OwnershipMissing
			case !binding.Enabled:
				skill.Availability = OwnershipSwitchedOff
			default:
				skill.Availability = OwnershipAvailable
			}
		case ToolboxSourceAgentLearned:
			if !inventoryRead {
				inventory, inventoryOK = skillInventory(src, agentName)
				inventoryRead = true
			}
			if inventoryOK {
				skill.Availability = learnedAvailability(inventory, ref.CapabilityID)
			}
		}
		view.Skills = append(view.Skills, skill)
	}
	if inventoryRead {
		view.Inventory = inventoryLabel(inventoryOK)
	}

	mcpBindings := make(map[string]MCPBinding)
	for _, binding := range ws.GetMCPBindings() {
		mcpBindings[strings.ToLower(strings.TrimSpace(binding.ID))] = binding
	}
	for _, ref := range recipe.MCPBindings {
		conn := OwnershipToolboxConnection{
			BindingID: ref.BindingID, Name: ref.BindingID, Required: ref.Required,
			State: OwnershipMissing, Tools: []string{}, NoLongerAllowed: []string{},
		}
		binding, exists := mcpBindings[strings.ToLower(strings.TrimSpace(ref.BindingID))]
		if exists {
			conn.Name = firstNonEmpty(binding.Alias, binding.ServerName, binding.ID)
			conn.State = OwnershipSwitchedOff
			if binding.Enabled {
				conn.State = OwnershipAvailable
			}
			conn.RepoScope = BindingRepoScope(binding)
			switch {
			case ref.InheritsBindingTools && binding.AllowsAllTools():
				conn.AllTools = true
			case ref.InheritsBindingTools:
				conn.Tools = append(conn.Tools, binding.AllowedTools...)
			default:
				// Same rule as the runtime: a binding narrowed after this
				// version was pinned wins.
				for _, tool := range ref.AllowedTools {
					if binding.ToolAllowed(tool) {
						conn.Tools = append(conn.Tools, tool)
					} else {
						conn.NoLongerAllowed = append(conn.NoLongerAllowed, tool)
					}
				}
			}
		} else {
			conn.Tools = append(conn.Tools, ref.AllowedTools...)
		}
		sort.Strings(conn.Tools)
		sort.Strings(conn.NoLongerAllowed)
		view.Connections = append(view.Connections, conn)
	}
	return view
}

// coreCapabilityLabels lists what the workspace provides outside any Toolbox,
// exactly as the runtime derives it.
func coreCapabilityLabels(ws *Workspace, agentName string) []string {
	labels := []string{}
	if synthesizeFilesystemBinding(ws.GetMCPBindings(), ws) != nil {
		labels = append(labels, "Workspace files (provided by Ori for this workspace's folders)")
	}
	for _, binding := range settingsManagedSkillBindings(ws, agentName) {
		labels = append(labels, binding.SkillName+" (workspace setting for its entry agent)")
	}
	return labels
}

func projectPermissions(ws *Workspace) OwnershipPermissions {
	perms := OwnershipPermissions{
		WorkspaceNativeTools: OwnershipToolboxNotApplicable,
		Notes: []string{
			"A Toolbox only narrows which skills and operations an agent may use. It does not grant workspace access, approve actions, or check that a connection works.",
			"In a workspace, each connection's own settings still limit its operations and repositories, even when a Toolbox selects them.",
			"Actions that change things outside Ori still ask for approval each time, unless trusted native tools are turned on for both the workspace and the agent.",
		},
	}
	if ws != nil {
		perms.WorkspaceNativeTools = "not_allowed"
		if ws.AllowNativeMCPCLI {
			perms.WorkspaceNativeTools = "allowed"
		}
	}
	return perms
}
