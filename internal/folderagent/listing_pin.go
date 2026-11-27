package folderagent

import (
	"slices"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const FirstLookProvenance = "folder_agent"

func ListingTools() []string {
	return []string{"list_directory", "list_directory_with_sizes", "directory_tree", "search_files", "get_file_info", "list_allowed_directories"}
}

// ReleasedFirstLookRecipe recognizes the exact obsolete setup transition:
// a host-created listing recipe widened once by Ori and no longer assigned.
// It is evidence only for displaying a receipt, never for pinning or reading.
func ReleasedFirstLookRecipe(ws *workspace.Workspace, instanceID, bindingID string) bool {
	wide := append(ListingTools(), "read_file", "read_text_file", "read_media_file", "read_multiple_files")
	if a, assigned := ws.GetToolboxAssignment(instanceID); assigned {
		// Startup may already have projected the released seat as the
		// workspace default. Never substitute a user-selected toolbox.
		const actor = "startup-migration"
		m, ok := ws.GetToolbox(a.ToolboxID)
		if !ok || a.Provenance != workspace.ToolboxProvenanceMigration || a.Actor != actor || a.ToolboxVersion != 1 || m.Version != 1 || m.Status != workspace.ToolboxStatusActive || !adviceRecipeMatches(m.CurrentRecipe(), bindingID, a.Provenance, actor, wide) {
			return false
		}
	}
	d, exists := ws.GetToolbox("folder-agent-" + instanceID)
	if !exists || d.Version != 2 || len(d.History) != 1 || d.Status != workspace.ToolboxStatusActive {
		return false
	}
	return d.History[0].Version == 1 && adviceRecipeMatches(d.History[0], bindingID, FirstLookProvenance, "Ori", ListingTools()) && adviceRecipeMatches(d.CurrentRecipe(), bindingID, FirstLookProvenance, "Ori", wide)
}

func adviceRecipeMatches(recipe workspace.ToolboxRecipe, binding, provenance, actor string, want []string) bool {
	if recipe.Actor != actor || recipe.Provenance != provenance || len(recipe.Skills) != 0 || len(recipe.MCPBindings) != 1 {
		return false
	}
	ref := recipe.MCPBindings[0]
	if !ref.Required || ref.BindingID != binding || ref.NeedsExplicitTools() || ref.OwnerCapabilityID != "" || len(ref.AllowedTools) != len(want) {
		return false
	}
	return !slices.ContainsFunc(want, func(name string) bool { return !slices.Contains(ref.AllowedTools, name) })
}

// ListingPinAllowed accepts an empty seat or our exact listing-only recipe.
// An unrelated assignment (or a conflicting definition under our ID) is not
// consent to replace it. Check both when offering and under the pin write lock.
func ListingPinAllowed(ws *workspace.Workspace, instanceID, bindingID string) bool {
	id := "folder-agent-" + instanceID
	a, assigned := ws.GetToolboxAssignment(instanceID)
	d, exists := ws.GetToolbox(id)
	if assigned && (!exists || a.ToolboxID != id || a.Provenance != FirstLookProvenance || a.ToolboxVersion != d.Version) {
		return false
	}
	if !exists {
		return true
	}
	if d.Status != workspace.ToolboxStatusActive || d.Provenance != FirstLookProvenance || len(d.Skills) != 0 || len(d.MCPBindings) != 1 {
		return false
	}
	ref := d.MCPBindings[0]
	if ref.BindingID != bindingID || ref.NeedsExplicitTools() || !slices.Contains(ref.AllowedTools, "list_directory") {
		return false
	}
	for _, name := range ref.AllowedTools {
		if !slices.Contains(ListingTools(), name) {
			return false
		}
	}
	return true
}
