package projecttemplates

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// PrimaryDirectoryIDKey and ProjectDirectoryIDKey are the SharedData keys used
// to record a workspace's project folder as its primary linked directory.
// They are exported so every persistence path (session store and folder
// store) reads and writes the same key names.
const (
	PrimaryDirectoryIDKey = "primary_directory_id"
	ProjectDirectoryIDKey = "project_directory_id"
)

// EnsureProjectDirectoryReference records the project folder at
// folderPath/relPath as a directory reference on folderWS, returning its ID.
// If a reference for that path already exists it is reused rather than
// duplicated. This is shared by the workspace-creation flow
// (sessionhttp) and the workspace_create_project chat tool (chathttp), which
// must agree on how a project folder is registered.
func EnsureProjectDirectoryReference(folderWS *workspace.Workspace, projectName, folderPath, relPath string) (string, error) {
	if folderWS == nil {
		return "", fmt.Errorf("workspace metadata is unavailable")
	}

	projectPath := filepath.Clean(filepath.Join(folderPath, relPath))
	for _, ref := range folderWS.DirectoryReferences {
		if filepath.Clean(ref.Path) == projectPath {
			return ref.ID, nil
		}
	}

	name := strings.TrimSpace(projectName)
	if name == "" {
		name = strings.TrimSpace(filepath.Base(relPath))
	}
	if name == "" || name == "." {
		name = "Project Folder"
	}

	if err := folderWS.AddDirectoryReference(workspace.DirectoryReference{
		Name: name,
		Path: projectPath,
	}); err != nil {
		return "", err
	}

	for _, ref := range folderWS.DirectoryReferences {
		if filepath.Clean(ref.Path) == projectPath {
			return ref.ID, nil
		}
	}
	return "", fmt.Errorf("project directory reference was not recorded")
}

// SetPrimaryDirectoryID records directoryID as both the workspace's primary
// linked directory and its project directory in sharedData. An empty
// directoryID clears both keys instead. Both keys are kept in sync so a
// project folder is consistently discoverable under either name.
func SetPrimaryDirectoryID(sharedData map[string]any, directoryID string) {
	if sharedData == nil {
		return
	}
	directoryID = strings.TrimSpace(directoryID)
	if directoryID == "" {
		delete(sharedData, PrimaryDirectoryIDKey)
		delete(sharedData, ProjectDirectoryIDKey)
		return
	}
	sharedData[PrimaryDirectoryIDKey] = directoryID
	sharedData[ProjectDirectoryIDKey] = directoryID
}
