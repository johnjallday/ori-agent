package folderagent

import (
	"slices"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const FirstLookProvenance = "folder_agent"

func ListingTools() []string {
	return []string{"list_directory", "list_directory_with_sizes", "directory_tree", "search_files", "get_file_info", "list_allowed_directories"}
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
