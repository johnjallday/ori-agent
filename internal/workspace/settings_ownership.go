package workspace

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/promptvars"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Settings ownership is a read-only explanation of which saved definition
// applies to an agent in one context (its library profile, or one workspace),
// where that definition lives, and how it compares with the library. It never
// resolves runtime tools, never migrates or assigns anything, and never
// serializes a raw agent: only whitelisted fields leave this file.
//
// The contract is recorded in tasks/findings-agent-settings-ownership.md.

// ErrSettingsOwnershipAgentNotFound means no library record, workspace
// membership or workspace copy knows the name.
var ErrSettingsOwnershipAgentNotFound = errors.New("agent not found")

// Context kinds and states.
const (
	OwnershipContextLibrary   = "library"
	OwnershipContextWorkspace = "workspace"

	OwnershipStateOK          = "ok"
	OwnershipStateNotFound    = "not_found"
	OwnershipStateNotMember   = "not_member"
	OwnershipStateUnavailable = "unavailable"
	OwnershipStateNoLibrary   = "no_library"
	OwnershipStateDegraded    = "degraded"
	OwnershipStateAbsent      = "absent"
)

// Definition sources.
const (
	OwnershipSourceLibrary         = "library"
	OwnershipSourceSystem          = "system"
	OwnershipSourceWorkspaceCopy   = "workspace_copy"
	OwnershipSourceLibraryFallback = "library_fallback"
	OwnershipSourceUnknown         = "unknown"
)

// Field comparisons.
const (
	OwnershipSame        = "same"
	OwnershipDifferent   = "different"
	OwnershipNotCompared = "not_compared"
)

// Instance selection states.
const (
	OwnershipInstanceSelected      = "selected"
	OwnershipInstanceAmbiguous     = "ambiguous"
	OwnershipInstanceNone          = "none"
	OwnershipInstanceNotFound      = "not_found"
	OwnershipInstanceNotApplicable = "not_applicable"
	OwnershipInstanceUnidentified  = "unidentified"
)

// Copy tracking states that need no tracking reader.
const (
	OwnershipTrackingNotApplicable = "not_applicable"
	OwnershipTrackingUnknown       = "unknown"
)

// OwnershipExcluded names what the projection deliberately never shows.
const OwnershipExcluded = "API keys, web search, native tool and cloud fallback permissions are not shown here."

// SettingsOwnershipSources are the read-only owners the projection consults.
type SettingsOwnershipSources struct {
	Workspaces Store
	// Library reads the user's own definition (store.LibraryDefinition).
	Library func(name string) store.LibraryRecord
	// Tracking optionally reports how a workspace copy relates to a consent
	// tracking record (projectstaffing.Service.CopyTracking). It must not write.
	Tracking func(agentName, workspaceID string, library, copyAgent *agent.Agent) (string, error)
	// SkillInventory optionally lists the skill names in an agent's skill
	// collection (skills.Manager.ListSkills). It must not write. Without it, or
	// when it fails, skill availability is reported as unknown.
	SkillInventory func(agentName string) ([]string, error)
}

// SettingsOwnershipRequest names the agent and the viewing context.
type SettingsOwnershipRequest struct {
	Agent       string
	WorkspaceID string
	InstanceID  string
}

// SettingsOwnership is the whitelisted projection.
type SettingsOwnership struct {
	Agent      string                     `json:"agent"`
	Library    OwnershipLibrary           `json:"library"`
	Contexts   OwnershipContexts          `json:"contexts"`
	Context    OwnershipContext           `json:"context"`
	Definition OwnershipDefinition        `json:"definition"`
	Tracking   OwnershipTracking          `json:"tracking"`
	Instances  OwnershipInstanceSelection `json:"instances"`
	Prompt     OwnershipPrompt            `json:"prompt"`
	// DirectToolbox is the Default Toolbox: skills for direct chat outside
	// workspaces. WorkspaceToolbox is the selected instance's pinned Toolbox.
	// They are separate selections and never merged.
	DirectToolbox    OwnershipDirectToolbox    `json:"direct_toolbox"`
	WorkspaceToolbox OwnershipWorkspaceToolbox `json:"workspace_toolbox"`
	Permissions      OwnershipPermissions      `json:"permissions"`
	// Impact is advisory: what a library edit would reach, from current
	// tracking evidence. It authorizes nothing; save-time checks decide.
	Impact   OwnershipImpact `json:"impact"`
	Excluded string          `json:"excluded"`
}

// Saved-prompt preview states.
const (
	OwnershipPromptOK            = "ok"
	OwnershipPromptNeedsInstance = "needs_instance"
	OwnershipPromptUnavailable   = "unavailable"
)

// OwnershipPromptNote says what the saved-prompt preview is not.
const OwnershipPromptNote = "Saved text only. Notes, memory, tools and other context Ori adds when the agent runs are not shown, so this is not the complete prompt of any past or future run."

// OwnershipPrompt is the saved base prompt and, in a workspace, the selected
// instance's saved guidance layered on it with the runtime's own renderer.
type OwnershipPrompt struct {
	State      string                 `json:"state"`
	BaseSource string                 `json:"base_source"`
	Base       string                 `json:"base"`
	Layers     []OwnershipPromptLayer `json:"layers"`
	Refinement string                 `json:"refinement"`
	Preview    string                 `json:"preview"`
	// HasVariables: the base contains {{…}} placeholders Ori fills at run time.
	HasVariables bool   `json:"has_variables"`
	Note         string `json:"note"`
}

// OwnershipPromptLayer is one saved instance refinement field.
type OwnershipPromptLayer struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// OwnershipLibrary says whether the agent has a library profile.
type OwnershipLibrary struct {
	State string `json:"state"`
	Owner string `json:"owner,omitempty"`
}

// OwnershipContexts lists the workspaces the agent can be inspected in.
type OwnershipContexts struct {
	State      string                    `json:"state"`
	Note       string                    `json:"note,omitempty"`
	Workspaces []OwnershipWorkspaceEntry `json:"workspaces"`
}

// OwnershipWorkspaceEntry is one selectable workspace context.
type OwnershipWorkspaceEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FolderSlug string `json:"folder_slug"`
	Attached   bool   `json:"attached"`
	EntryPoint bool   `json:"entry_point"`
}

// OwnershipWorkspaceRef names the selected workspace.
type OwnershipWorkspaceRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FolderSlug string `json:"folder_slug"`
}

// OwnershipContext is the selected viewing context.
type OwnershipContext struct {
	Kind      string                 `json:"kind"`
	State     string                 `json:"state"`
	Workspace *OwnershipWorkspaceRef `json:"workspace"`
	Message   string                 `json:"message,omitempty"`
}

// OwnershipDefinition is the saved definition that applies in the context.
type OwnershipDefinition struct {
	State    string           `json:"state"`
	Source   string           `json:"source"`
	Note     string           `json:"note,omitempty"`
	Disabled bool             `json:"disabled"`
	Fields   []OwnershipField `json:"fields"`
}

// OwnershipField is one whitelisted saved setting.
type OwnershipField struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Value      any    `json:"value"`
	Empty      bool   `json:"empty"`
	Comparison string `json:"comparison"`
}

// OwnershipTracking is the copy's tracking relationship to the library.
type OwnershipTracking struct {
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
}

// OwnershipInstanceSelection lists the agent's instances in the workspace.
type OwnershipInstanceSelection struct {
	State      string              `json:"state"`
	Items      []OwnershipInstance `json:"items"`
	SelectedID string              `json:"selected_id,omitempty"`
}

// OwnershipInstance is one attachment's saved refinement.
type OwnershipInstance struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	InstanceNumber     int    `json:"instance_number"`
	Role               string `json:"role"`
	Description        string `json:"description"`
	CustomInstructions string `json:"custom_instructions"`
	RoleID             string `json:"role_id,omitempty"`
	EntryPoint         bool   `json:"entry_point"`
}

// ProjectSettingsOwnership builds the inspection for req. It returns
// ErrSettingsOwnershipAgentNotFound only when nothing knows the name; every
// other problem is a recoverable state inside the projection.
func ProjectSettingsOwnership(src SettingsOwnershipSources, req SettingsOwnershipRequest) (SettingsOwnership, error) {
	name := strings.TrimSpace(req.Agent)
	out := SettingsOwnership{
		Agent:     name,
		Excluded:  OwnershipExcluded,
		Tracking:  OwnershipTracking{State: OwnershipTrackingNotApplicable},
		Instances: OwnershipInstanceSelection{State: OwnershipInstanceNotApplicable, Items: []OwnershipInstance{}},
		Definition: OwnershipDefinition{
			State: OwnershipStateAbsent, Source: OwnershipSourceUnknown, Fields: []OwnershipField{},
		},
	}
	if name == "" {
		return out, ErrSettingsOwnershipAgentNotFound
	}

	library := store.LibraryRecord{State: store.LibraryAbsent}
	if src.Library != nil {
		library = src.Library(name)
	}
	out.Library = OwnershipLibrary{State: library.State, Owner: library.Owner}
	out.Contexts = listOwnershipContexts(src, name)
	if library.Owner == store.LibraryOwnerSystem {
		out.Contexts.Note = "Workspaces that still refer to the assistant by an earlier name are not listed."
	}

	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		if library.State == store.LibraryAbsent && len(out.Contexts.Workspaces) == 0 {
			return out, ErrSettingsOwnershipAgentNotFound
		}
		projectLibraryContext(&out, library)
		out.Prompt = projectPrompt(out)
		out.DirectToolbox = projectDirectToolbox(src, out, library)
		out.WorkspaceToolbox = OwnershipWorkspaceToolbox{State: OwnershipToolboxNotApplicable, Skills: []OwnershipToolboxSkill{}, Connections: []OwnershipToolboxConnection{}, Core: []string{}}
		out.Permissions = projectPermissions(nil)
		out.Impact = projectLibraryImpact(src, out, library, name)
		return out, nil
	}
	ws := projectWorkspaceContext(src, &out, library, name, workspaceID, strings.TrimSpace(req.InstanceID))
	out.Prompt = projectPrompt(out)
	out.DirectToolbox = OwnershipDirectToolbox{
		State: OwnershipToolboxNotApplicable, Skills: []OwnershipToolboxSkill{},
		Note: "The Default Toolbox applies to direct chat outside workspaces. In a workspace, the instance's pinned workspace Toolbox decides its tools instead.",
	}
	out.WorkspaceToolbox = projectWorkspaceToolbox(src, out, ws, name)
	out.Permissions = projectPermissions(ws)
	out.Impact = OwnershipImpact{State: OwnershipToolboxNotApplicable, Copies: []OwnershipImpactCopy{}}
	if library.State == store.LibraryAbsent && len(out.Contexts.Workspaces) == 0 && out.Context.State != OwnershipStateOK {
		return out, ErrSettingsOwnershipAgentNotFound
	}
	return out, nil
}

func listOwnershipContexts(src SettingsOwnershipSources, name string) OwnershipContexts {
	contexts := OwnershipContexts{State: OwnershipStateOK, Workspaces: []OwnershipWorkspaceEntry{}}
	if src.Workspaces == nil {
		contexts.State = OwnershipStateUnavailable
		return contexts
	}
	// Only workspaces that reference the name are contexts. A copy file alone is
	// not membership: copies are stored by slug, so "Dev Ops" and "dev-ops"
	// would share one file.
	for _, ref := range WorkspaceMembershipFor(src.Workspaces, name).Workspaces {
		contexts.Workspaces = append(contexts.Workspaces, OwnershipWorkspaceEntry{
			ID: ref.ID, Name: ref.Name, FolderSlug: ref.FolderSlug, Attached: true, EntryPoint: ref.EntryPoint,
		})
	}
	return contexts
}

func projectLibraryContext(out *SettingsOwnership, library store.LibraryRecord) {
	out.Context = OwnershipContext{Kind: OwnershipContextLibrary, State: OwnershipStateOK}
	switch library.State {
	case store.LibraryPresent:
		source := OwnershipSourceLibrary
		if library.Owner == store.LibraryOwnerSystem {
			source = OwnershipSourceSystem
		}
		out.Definition = definitionFrom(library.Definition, nil, source, OwnershipStateOK, "")
	case store.LibraryUnreadable:
		out.Context.State = OwnershipStateUnavailable
		out.Context.Message = "This agent's library profile could not be read."
		out.Definition.State = OwnershipStateUnavailable
		out.Definition.Note = "The saved definition file could not be read, so its settings are unknown."
	default:
		out.Context.State = OwnershipStateNoLibrary
		out.Context.Message = "This agent belongs to a workspace and has no library profile. Choose a workspace to see its settings."
	}
}

func projectWorkspaceContext(src SettingsOwnershipSources, out *SettingsOwnership, library store.LibraryRecord, name, workspaceID, instanceID string) *Workspace {
	out.Context = OwnershipContext{Kind: OwnershipContextWorkspace, State: OwnershipStateOK}
	out.Instances.State = OwnershipInstanceNone
	if src.Workspaces == nil {
		out.Context.State = OwnershipStateUnavailable
		out.Context.Message = "Workspaces could not be read."
		return nil
	}
	ws, err := src.Workspaces.Get(workspaceID)
	if err != nil || ws == nil || ws.Status == StatusTrashed || ws.Status == StatusMissing {
		out.Context.State = OwnershipStateNotFound
		out.Context.Message = "That workspace is not available. It may have been removed or moved to the trash."
		out.Instances.State = OwnershipInstanceNotApplicable
		return nil
	}
	out.Context.Workspace = &OwnershipWorkspaceRef{ID: ws.ID, Name: ws.Name, FolderSlug: ws.FolderSlug}

	attached, sharedWith := false, ""
	for _, ref := range referencedAgentNames(ws) {
		switch {
		case strings.EqualFold(ref, name):
			attached = true
		case Slugify(ref) == Slugify(name):
			sharedWith = ref
		}
	}
	if !attached {
		out.Context.State = OwnershipStateNotMember
		out.Context.Message = "This agent is not in that workspace."
		out.Instances.State = OwnershipInstanceNotApplicable
		return nil
	}
	out.Instances = selectOwnershipInstance(ws, name, instanceID)
	if sharedWith != "" {
		// Both names resolve to one copy file; the file cannot say whose it is.
		out.Definition.State = OwnershipStateDegraded
		out.Definition.Note = "This workspace also has an agent named \"" + sharedWith +
			"\" whose saved copy uses the same file, so which settings belong to this agent could not be verified."
		out.Tracking.State = OwnershipTrackingUnknown
		return ws
	}
	copyAgent, hasCopy, copyErr := src.Workspaces.GetWorkspaceAgent(ws.ID, name)

	var base *agent.Agent
	if library.State == store.LibraryPresent {
		base = library.Definition
	}
	switch {
	case copyErr != nil:
		out.Definition.State = OwnershipStateDegraded
		out.Definition.Source = OwnershipSourceUnknown
		out.Definition.Note = "This workspace's own copy of the agent could not be read. Ori's runner would fall back to the library profile, but which settings apply here could not be verified."
		out.Tracking.State = OwnershipTrackingUnknown
	case hasCopy && copyAgent != nil:
		out.Definition = definitionFrom(copyAgent, base, OwnershipSourceWorkspaceCopy, OwnershipStateOK,
			"This workspace keeps its own saved copy of the agent's settings.")
		out.Tracking = trackingFor(src, name, ws.ID, base, copyAgent)
	case base != nil:
		source := OwnershipSourceLibraryFallback
		out.Definition = definitionFrom(base, nil, source, OwnershipStateOK,
			"This workspace has no saved copy, so it reads the library profile directly.")
	case library.State == store.LibraryUnreadable:
		out.Definition.State = OwnershipStateUnavailable
		out.Definition.Note = "This workspace has no saved copy and the library profile could not be read."
	default:
		// Never borrow another workspace's namesake.
		out.Definition.State = OwnershipStateUnavailable
		out.Definition.Note = "This workspace has no saved copy of the agent and there is no library profile to read instead."
	}
	return ws
}

// projectPrompt composes the saved-prompt preview from what the projection
// already selected: the verified saved base (the workspace copy, or the library
// when the workspace reads it directly) and, in a workspace, the selected
// instance's saved guidance, layered exactly as AppendAgentRefinement does.
// Nothing else is read; run-time layers are excluded by design.
func projectPrompt(out SettingsOwnership) OwnershipPrompt {
	prompt := OwnershipPrompt{State: OwnershipPromptUnavailable, Layers: []OwnershipPromptLayer{}, Note: OwnershipPromptNote}
	if out.Context.State != OwnershipStateOK || out.Definition.State != OwnershipStateOK {
		return prompt
	}
	for _, f := range out.Definition.Fields {
		if f.Key == "system_prompt" {
			prompt.Base, _ = f.Value.(string)
		}
	}
	prompt.BaseSource = out.Definition.Source
	prompt.HasVariables = promptvars.HasVariables(prompt.Base)
	prompt.State = OwnershipPromptOK
	prompt.Preview = prompt.Base
	if out.Context.Kind != OwnershipContextWorkspace {
		return prompt
	}
	switch out.Instances.State {
	case OwnershipInstanceAmbiguous, OwnershipInstanceUnidentified, OwnershipInstanceNotFound:
		// Guidance belongs to one instance; never pick one for the reader.
		prompt.State = OwnershipPromptNeedsInstance
		prompt.Preview = ""
		return prompt
	case OwnershipInstanceSelected:
	default:
		return prompt
	}
	var inst OwnershipInstance
	for _, item := range out.Instances.Items {
		if item.ID == out.Instances.SelectedID {
			inst = item
		}
	}
	for _, layer := range []OwnershipPromptLayer{
		{Key: "role", Label: "Role here", Value: inst.Role},
		{Key: "description", Label: "Description here", Value: inst.Description},
		{Key: "custom_instructions", Label: "Custom instructions", Value: inst.CustomInstructions},
	} {
		if strings.TrimSpace(layer.Value) != "" {
			prompt.Layers = append(prompt.Layers, layer)
		}
	}
	prompt.Refinement = RenderAgentRefinement(AgentInstance{
		Role: inst.Role, Description: inst.Description, CustomInstructions: inst.CustomInstructions,
	})
	switch {
	case prompt.Refinement == "":
	case strings.TrimSpace(prompt.Base) == "":
		prompt.Preview = prompt.Refinement
	default:
		prompt.Preview = prompt.Base + "\n\n---\n" + prompt.Refinement
	}
	return prompt
}

func trackingFor(src SettingsOwnershipSources, name, workspaceID string, library, copyAgent *agent.Agent) OwnershipTracking {
	if src.Tracking == nil {
		return OwnershipTracking{State: OwnershipTrackingUnknown, Note: "Copy tracking could not be checked."}
	}
	state, err := src.Tracking(name, workspaceID, library, copyAgent)
	tracking := OwnershipTracking{State: state}
	if state == "" {
		tracking.State = OwnershipTrackingUnknown
	}
	if err != nil {
		tracking.Note = err.Error()
	}
	return tracking
}

func selectOwnershipInstance(ws *Workspace, name, instanceID string) OwnershipInstanceSelection {
	sel := OwnershipInstanceSelection{Items: []OwnershipInstance{}}
	for _, inst := range ws.GetAgentInstances() {
		if !strings.EqualFold(strings.TrimSpace(inst.Name), name) {
			continue
		}
		sel.Items = append(sel.Items, OwnershipInstance{
			ID: inst.ID, Name: inst.Name, InstanceNumber: inst.InstanceNumber, Role: inst.Role,
			Description: inst.Description, CustomInstructions: inst.CustomInstructions,
			RoleID: inst.RoleID, EntryPoint: inst.EntryPoint,
		})
	}
	switch {
	case instanceID != "":
		for _, item := range sel.Items {
			if item.ID != "" && item.ID == instanceID {
				sel.State, sel.SelectedID = OwnershipInstanceSelected, item.ID
				return sel
			}
		}
		sel.State = OwnershipInstanceNotFound
	case len(sel.Items) == 1 && sel.Items[0].ID != "":
		sel.State, sel.SelectedID = OwnershipInstanceSelected, sel.Items[0].ID
	case len(sel.Items) == 1:
		// A legacy attachment without a stable ID cannot be addressed.
		sel.State = OwnershipInstanceUnidentified
	case len(sel.Items) > 1:
		sel.State = OwnershipInstanceAmbiguous
	default:
		sel.State = OwnershipInstanceNone
	}
	return sel
}

// definitionFrom projects the whitelisted fields of def, comparing each with
// against when it is non-nil.
func definitionFrom(def, against *agent.Agent, source, state, note string) OwnershipDefinition {
	out := OwnershipDefinition{State: state, Source: source, Note: note, Fields: []OwnershipField{}}
	if def == nil {
		out.State = OwnershipStateUnavailable
		return out
	}
	out.Disabled = def.Status == types.AgentStatusDisabled
	for _, f := range ownershipFieldsOf(def) {
		f.Comparison = OwnershipNotCompared
		if against != nil {
			f.Comparison = OwnershipDifferent
			for _, other := range ownershipFieldsOf(against) {
				if other.Key == f.Key && other.Value == f.Value {
					f.Comparison = OwnershipSame
				}
			}
		}
		out.Fields = append(out.Fields, f)
	}
	return out
}

// ownershipFieldsOf is the whitelist. Credentials, permission grants and
// fallbacks are never read from the definition here.
func ownershipFieldsOf(def *agent.Agent) []OwnershipField {
	s := def.Settings
	description := ""
	if def.Metadata != nil {
		description = def.Metadata.Description
	}
	text := func(key, label, value string) OwnershipField {
		return OwnershipField{Key: key, Label: label, Value: value, Empty: strings.TrimSpace(value) == ""}
	}
	return []OwnershipField{
		text("provider", "Provider", s.Provider),
		text("model", "Model", s.Model),
		text("reasoning_effort", "Reasoning level", s.ReasoningEffort),
		{Key: "temperature", Label: "Temperature", Value: s.Temperature},
		{Key: "max_output_tokens", Label: "Output limit", Value: s.MaxOutputTokens, Empty: s.MaxOutputTokens <= 0},
		text("role", "Definition role", string(def.Role)),
		text("description", "Description", description),
		text("system_prompt", "Base prompt", s.SystemPrompt),
	}
}
