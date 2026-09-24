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
	// FolderOfferIDKey records which "show me a folder" offer a workspace
	// was created for, so the offer's resolution can attach the folder to
	// that workspace and no other.
	FolderOfferIDKey = "folder_digest_offer_id"
)

// AttachLinkedDirectory records an outside folder (an absolute path that
// stays where it is; nothing is copied) as a directory reference on folderWS
// and marks it the workspace's primary project directory. A reference for
// the same path is reused rather than duplicated. It returns the reference
// ID.
func AttachLinkedDirectory(folderWS *workspace.Workspace, name, absPath string) (string, error) {
	if folderWS == nil {
		return "", fmt.Errorf("workspace metadata is unavailable")
	}
	path := filepath.Clean(strings.TrimSpace(absPath))
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("a linked directory needs an absolute path")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(path)
	}
	id := ""
	for _, ref := range folderWS.DirectoryReferences {
		if filepath.Clean(ref.Path) == path {
			id = ref.ID
			break
		}
	}
	if id == "" {
		if err := folderWS.AddDirectoryReference(workspace.DirectoryReference{Name: name, Path: path}); err != nil {
			return "", err
		}
		for _, ref := range folderWS.DirectoryReferences {
			if filepath.Clean(ref.Path) == path {
				id = ref.ID
				break
			}
		}
	}
	if id == "" {
		return "", fmt.Errorf("linked directory reference was not recorded")
	}
	if folderWS.SharedData == nil {
		folderWS.SharedData = make(map[string]any)
	}
	SetPrimaryDirectoryID(folderWS.SharedData, id)
	return id, nil
}

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
