package workspace

import "strings"

const (
	// AnchorDirectoryReferenceIDKey identifies the external work folder. Only
	// its reference ID is portable here; the path lives on that reference.
	AnchorDirectoryReferenceIDKey = "anchor_directory_reference_id"
	PrimaryDirectoryIDKey         = "primary_directory_id"
	AnchorFolderMCPAlias          = "anchor-folder"

	ShapeManaged  = "managed"
	ShapeAnchored = "anchored"
	ShapeImported = "imported"
)

// AnchorDirectoryReferenceID returns the declared anchor, even when its folder
// is missing on this machine. Never infer an anchor from paths or bindings.
func (w *Workspace) AnchorDirectoryReferenceID() string {
	if w == nil {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return AnchorIDFromSharedData(w.SharedData)
}

func (w *Workspace) IsAnchored() bool { return w.AnchorDirectoryReferenceID() != "" }

func AnchorIDFromSharedData(data map[string]any) string {
	id, _ := data[AnchorDirectoryReferenceIDKey].(string)
	return strings.TrimSpace(id)
}

// Shape is derived, not another persisted flag that could drift from the
// portable anchor or existing Import Folder metadata.
func (w *Workspace) Shape() string {
	if w == nil {
		return ShapeManaged
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return ShapeFromSharedData(w.SharedData)
}

func ShapeFromSharedData(data map[string]any) string {
	if AnchorIDFromSharedData(data) != "" {
		return ShapeAnchored
	}
	if meta, ok := data["folder_import"].(map[string]any); ok {
		enabled, _ := meta["enabled"].(bool)
		text, _ := meta["enabled"].(string)
		path, _ := meta["path"].(string)
		if enabled || strings.EqualFold(strings.TrimSpace(text), "true") || strings.TrimSpace(path) != "" {
			return ShapeImported
		}
	}
	return ShapeManaged
}
