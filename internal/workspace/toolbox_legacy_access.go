package workspace

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Compatibility bridge from the pre-Toolbox per-instance access endpoints to
// explicit Toolbox state (PRD FR-36).
//
// The old `agent-skill-access` / `agent-mcp-access` endpoints stay available so
// existing clients and scripts keep working. What they must NOT do is keep
// working as an independent source of truth: after migration the runtime
// resolves the pinned Toolbox and never reads an access entry, so a mutation
// that only wrote the access entry would appear to succeed and change nothing.
// That silent no-op is worse than a removed endpoint.
//
// So a legacy access mutation is translated into the equivalent explicit
// Toolbox edit — a new version of the instance's assigned Toolbox, re-pinned to
// that instance. The access entry is still written (callers read it back), but
// it is now a projection of the Toolbox rather than a rival to it.

// LegacyAccessKind selects which half of a Toolbox a legacy access mutation
// rewrites.
type LegacyAccessKind string

const (
	// LegacyAccessSkills rewrites the workspace-provided skill entries,
	// leaving agent-learned entries untouched — the legacy endpoint only ever
	// controlled workspace skill bindings.
	LegacyAccessSkills LegacyAccessKind = "skills"
	// LegacyAccessMCP rewrites the MCP binding entries.
	LegacyAccessMCP LegacyAccessKind = "mcp"
)

// ApplyLegacyAccessToToolbox translates a legacy per-instance access mutation
// into an explicit Toolbox edit, returning the resulting assignment.
//
// It returns ok=false when the instance has no assignment yet: an unmigrated
// workspace still resolves through the legacy path, where writing the access
// entry alone is exactly right.
//
// Must be called with the workspace already loaded for update — it mutates ws
// in place and the caller saves.
func ApplyLegacyAccessToToolbox(ws *Workspace, agentInstanceID string, kind LegacyAccessKind, enabledBindingIDs []string, actor string) (*AgentToolboxAssignment, bool, error) {
	if ws == nil {
		return nil, false, fmt.Errorf("workspace is required")
	}
	if _, exists := ws.GetToolboxAssignment(agentInstanceID); !exists {
		return nil, false, nil
	}

	definition, recipe, ok, err := ws.ResolveAssignedToolbox(agentInstanceID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	skills, bindings := recipe.Skills, recipe.MCPBindings
	switch kind {
	case LegacyAccessSkills:
		skills = rewriteWorkspaceSkillEntries(ws, skills, enabledBindingIDs)
	case LegacyAccessMCP:
		bindings = rewriteMCPEntries(ws, enabledBindingIDs)
	default:
		return nil, false, fmt.Errorf("unknown legacy access kind %q", kind)
	}

	targetID := definition.ID
	// A Toolbox shared by several instances must not be edited through one
	// instance's access call: that would silently change the others, which is
	// precisely the cross-instance mutation FR-16/FR-17 forbid. Fork instead.
	if len(instancesAssignedToToolbox(ws, definition.ID)) > 1 {
		forked, forkErr := forkToolboxForInstance(ws, definition, recipe, agentInstanceID, actor)
		if forkErr != nil {
			return nil, false, forkErr
		}
		targetID = forked.ID
	}

	updated, err := ws.SaveToolboxVersion(targetID, skills, bindings, ToolboxProvenanceLegacyAccessAPI, actor)
	if err != nil {
		return nil, false, err
	}

	saved, err := ws.SetToolboxAssignment(AgentToolboxAssignment{
		AgentInstanceID: agentInstanceID,
		ToolboxID:       updated.ID,
		ToolboxVersion:  updated.Version,
		Provenance:      ToolboxProvenanceLegacyAccessAPI,
		Actor:           actor,
	})
	if err != nil {
		return nil, false, err
	}
	return saved, true, nil
}

// rewriteWorkspaceSkillEntries replaces the workspace-provided skill entries
// with the ones the access list names, preserving agent-learned entries.
func rewriteWorkspaceSkillEntries(ws *Workspace, existing []ToolboxSkillRef, enabledBindingIDs []string) []ToolboxSkillRef {
	preserved := make([]ToolboxSkillRef, 0, len(existing))
	preservedIdentities := make(map[string]struct{}, len(existing))
	for _, ref := range existing {
		if NormalizeToolboxSource(ref.Source) != ToolboxSourceWorkspaceProvided {
			preserved = append(preserved, ref)
			if identity := NormalizeToolboxCapabilityID(ref.CapabilityID); identity != "" {
				preservedIdentities[identity] = struct{}{}
			}
		}
	}

	wanted := normalizeValueSet(enabledBindingIDs)
	ownerByResource := ownedResourceCapabilityIndex(ws)
	for _, binding := range ws.GetSkillBindings() {
		if !wanted[strings.ToLower(strings.TrimSpace(binding.ID))] {
			continue
		}
		name := strings.TrimSpace(binding.SkillName)
		identity := NormalizeToolboxCapabilityID(name)
		if identity == "" {
			continue
		}
		// Migration deliberately omits a workspace binding shadowed by an
		// agent-learned/core skill: the legacy access model controlled whether
		// the capability was available, not which source supplied it. Preserve
		// that precedence when translating later access writes, or changing an
		// unrelated switch can recreate a source collision and block removal.
		if _, shadowed := preservedIdentities[identity]; shadowed {
			continue
		}
		preserved = append(preserved, ToolboxSkillRef{
			CapabilityID:      identity,
			DisplayName:       name,
			Source:            ToolboxSourceWorkspaceProvided,
			BindingID:         binding.ID,
			OwnerCapabilityID: ownerByResource[resourceKey(ResourceMCPBinding, binding.ID)],
			Required:          true,
		})
	}
	return preserved
}

// rewriteMCPEntries builds the MCP entries an access list names.
//
// A binding whose policy permits every tool becomes an inherited entry rather
// than an invented tool subset, for the same reason migration does it: naming
// operations that may not exist would be a lie, and omitting them would narrow
// what the caller asked for (see ToolboxMCPRef.InheritsBindingTools).
func rewriteMCPEntries(ws *Workspace, enabledBindingIDs []string) []ToolboxMCPRef {
	wanted := normalizeValueSet(enabledBindingIDs)
	ownerByResource := ownedResourceCapabilityIndex(ws)

	refs := make([]ToolboxMCPRef, 0, len(wanted))
	for _, binding := range ws.GetMCPBindings() {
		if !wanted[strings.ToLower(strings.TrimSpace(binding.ID))] {
			continue
		}
		ref := ToolboxMCPRef{
			BindingID:         binding.ID,
			OwnerCapabilityID: ownerByResource[resourceKey(ResourceMCPBinding, binding.ID)],
			Required:          true,
		}
		if binding.AllowsAllTools() {
			ref.InheritsBindingTools = true
		} else {
			ref.AllowedTools = normalizeToolNames(binding.AllowedTools)
		}
		refs = append(refs, ref)
	}
	return refs
}

// instancesAssignedToToolbox lists the agent instances currently pinned to a
// Toolbox.
func instancesAssignedToToolbox(ws *Workspace, toolboxID string) []string {
	normalized := strings.TrimSpace(toolboxID)
	var instances []string
	for _, assignment := range ws.ListToolboxAssignments() {
		if strings.EqualFold(strings.TrimSpace(assignment.ToolboxID), normalized) {
			instances = append(instances, assignment.AgentInstanceID)
		}
	}
	return instances
}

// forkToolboxForInstance copies a shared Toolbox's current content into a new
// per-instance Toolbox, so an edit made on behalf of one instance cannot reach
// the others.
func forkToolboxForInstance(ws *Workspace, source ToolboxDefinition, recipe ToolboxRecipe, agentInstanceID, actor string) (*ToolboxDefinition, error) {
	name := source.Name
	for _, instance := range ws.GetAgentInstances() {
		if strings.EqualFold(strings.TrimSpace(instance.ID), strings.TrimSpace(agentInstanceID)) {
			name = fmt.Sprintf("%s — %s", source.Name, strings.TrimSpace(instance.Name))
			break
		}
	}
	if _, taken := ws.FindToolboxByName(name); taken {
		name = fmt.Sprintf("%s (%s)", source.Name, strings.TrimSpace(agentInstanceID))
	}
	if len([]rune(name)) > MaxToolboxNameLength {
		name = string([]rune(name)[:MaxToolboxNameLength])
	}

	return ws.CreateToolbox(ToolboxDefinition{
		ID:          "tbx-" + uuid.New().String(),
		WorkspaceID: ws.ID,
		Name:        NormalizeToolboxName(name),
		Description: source.Description,
		Icon:        source.Icon,
		Color:       source.Color,
		Status:      ToolboxStatusActive,
		Skills:      recipe.Skills,
		MCPBindings: recipe.MCPBindings,
		Provenance:  ToolboxProvenanceLegacyAccessAPI,
		Actor:       actor,
	})
}
