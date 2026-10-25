package sessionhttp

import (
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Snapshot only the canonical grant's identity. No content is read while a task
// is seeded, and a missing grant produces a restricted but unavailable task.
func folderSummaryScope(ws *workspace.Workspace) (workspace.FolderSummaryScope, bool) {
	id, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
	entry := ""
	locator, err := workspace.GetProjectEntryLocator(ws.SharedData)
	if err != nil {
		return workspace.FolderSummaryScope{}, false
	}
	if locator != nil {
		if locator.Kind != workspace.ProjectEntryDirectoryReference {
			return workspace.FolderSummaryScope{}, false
		}
		id, entry = locator.DirectoryReferenceID, locator.RelativePath
	}
	ref, err := ws.GetDirectoryReference(strings.TrimSpace(id))
	if err != nil || ref == nil || ref.WorkspaceID != ws.ID || ref.Purpose != "" {
		return workspace.FolderSummaryScope{}, false
	}
	info, err := os.Lstat(ref.Path) // #nosec G304 G703 -- canonical reference by ID, metadata only
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return workspace.FolderSummaryScope{}, false
	}
	identity, err := projectlibrary.DirectoryIdentity(info)
	if err != nil {
		return workspace.FolderSummaryScope{}, false
	}
	return workspace.FolderSummaryScope{DirectoryID: ref.ID, Identity: identity, Entry: entry}, true
}
