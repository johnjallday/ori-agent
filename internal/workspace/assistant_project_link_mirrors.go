package workspace

import (
	"bytes"
	"encoding/json"
)

// AssistantProjectLinkMirrorsAgree checks the complete saved child link and
// identity across a canonical primary and any distinct folder mirror. A link
// ID/revision match is not enough: a partial write can change project roles,
// bindings or immutable provider pins without changing either. This is a
// read-only refusal guard, not a reconciliation or a choice of store winner.
func AssistantProjectLinkMirrorsAgree(store Store, project *Workspace) bool {
	if store == nil || project == nil || project.GetAssistantProjectLink() == nil {
		return false
	}
	mirror, ok := store.(MirrorWorkspaceProvider)
	if !ok {
		return true // A plain single store has no second authority to compare.
	}
	folder, mirrored, err := mirror.GetMirrorWorkspace(project.ID)
	if err != nil {
		return false
	}
	if !mirrored {
		return true
	}
	if folder == nil || folder.ID != project.ID || folder.OwnerUserID != project.OwnerUserID ||
		folder.ParentID != project.ParentID || folder.Status != project.Status || folder.GetAssistantProjectLink() == nil {
		return false
	}
	primaryLink, primaryErr := json.Marshal(project.GetAssistantProjectLink())
	folderLink, folderErr := json.Marshal(folder.GetAssistantProjectLink())
	return primaryErr == nil && folderErr == nil && bytes.Equal(primaryLink, folderLink)
}
