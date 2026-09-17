package projectconnection

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The attach functions below are run-independent: they know nothing about a
// setup journey run, its deterministic IDs, or its Home. The guided journey
// and the ordinary Create Workspace modal both call them, so a workspace
// attached from either path is recorded identically.

var (
	// ErrNoProjectEntry means the folder is readable but holds no direct-child
	// file with one of the blueprint's declared project-file extensions.
	ErrNoProjectEntry = errors.New("folder has no project file with a declared extension")
	// ErrFolderUnavailable means the selected folder is missing, is not a plain
	// directory, cannot be read, or holds more candidates than Ori reviews.
	ErrFolderUnavailable = errors.New("selected project folder is unavailable")
	// ErrEntryNotFound means a requested project file is not one of the scanned
	// candidates (or is not a bare file name).
	ErrEntryNotFound = errors.New("requested project file is not in the selected folder")
)

// ExistingProjectScan is one bounded, no-follow read of a folder chosen with
// Ori's native picker.
type ExistingProjectScan struct {
	// Root is the absolute, cleaned folder path.
	Root string
	// Candidates are the direct-child regular files whose extension the
	// blueprint declares, sorted by name.
	Candidates []string
	// Digest fingerprints the root and every candidate's name, mode, size, and
	// modification time, so a later re-scan can tell whether anything changed.
	Digest string
}

// ScanExistingProject lists the project files a blueprint could attach from
// selected. It never follows a symlinked root or candidate and never reads
// file contents.
func ScanExistingProject(selected string, declaration *projecttemplates.AttachExistingDeclaration) (ExistingProjectScan, error) {
	if declaration == nil || len(declaration.EntryExtensions) == 0 {
		return ExistingProjectScan{}, ErrFolderUnavailable
	}
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(selected)))
	if err != nil || !filepath.IsAbs(root) {
		return ExistingProjectScan{}, ErrFolderUnavailable
	}
	info, err := os.Lstat(root) // #nosec G304 -- root came only from Ori's trusted native picker token
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ExistingProjectScan{}, ErrFolderUnavailable
	}
	entries, err := os.ReadDir(root) // #nosec G304 -- exact no-follow picker root checked above
	if err != nil {
		return ExistingProjectScan{}, ErrFolderUnavailable
	}
	allowed := make(map[string]struct{}, len(declaration.EntryExtensions))
	for _, extension := range declaration.EntryExtensions {
		allowed[strings.ToLower(extension)] = struct{}{}
	}
	candidates := make([]string, 0)
	facts := []string{root, info.ModTime().UTC().Format(time.RFC3339Nano)}
	for _, entry := range entries {
		if len(candidates) >= maxEntryCandidates {
			return ExistingProjectScan{}, ErrFolderUnavailable
		}
		if _, ok := allowed[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			continue
		}
		entryInfo, statErr := os.Lstat(filepath.Join(root, entry.Name())) // #nosec G304 -- one direct child of the checked root
		if statErr != nil || entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, entry.Name())
		facts = append(facts, entry.Name(), entryInfo.Mode().String(), entryInfo.ModTime().UTC().Format(time.RFC3339Nano), strconv.FormatInt(entryInfo.Size(), 10))
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return ExistingProjectScan{}, ErrNoProjectEntry
	}
	return ExistingProjectScan{Root: root, Candidates: candidates, Digest: digestStrings(facts...)}, nil
}

// SelectProjectEntry chooses the project file to attach. An empty request
// selects the only candidate; with several candidates it returns "" so the
// caller can ask the user, and a commit must still refuse an empty selection.
func SelectProjectEntry(requested string, candidates []string) (string, error) {
	if requested == "" {
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		return "", nil
	}
	if filepath.Base(requested) != requested || strings.ContainsAny(requested, `/\\`) {
		return "", ErrEntryNotFound
	}
	for _, candidate := range candidates {
		if candidate == requested {
			return candidate, nil
		}
	}
	return "", ErrEntryNotFound
}

// FolderOwner names the workspace a folder already belongs to.
type FolderOwner struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
}

// FolderOwnerStore is what FindFolderOwner reads: every workspace, and where
// each one's own folder is.
type FolderOwnerStore interface {
	List() ([]string, error)
	Get(string) (*workspace.Workspace, error)
	GetFolderPath(string) (string, error)
}

// folderImportKey is the shared_data record Import Folder writes on a
// workspace it adopted from an existing folder.
const folderImportKey = "folder_import"

// FindFolderOwner reports the workspace, other than ignoreWorkspaceID, that
// selected already belongs to: a folder attached as a workspace's project
// (through the guided journey or the create modal), or a workspace's own
// folder, which is what Import Folder adopts. It returns nil when no workspace
// owns it, and an error when ownership cannot be verified, which callers must
// treat as a refusal rather than as "free".
func FindFolderOwner(store FolderOwnerStore, selected, ignoreWorkspaceID string) (*FolderOwner, error) {
	if store == nil {
		return nil, ErrUnavailable
	}
	selectedInfo, err := os.Stat(selected)
	if err != nil {
		return nil, ErrFolderUnavailable
	}
	ids, err := store.List()
	if err != nil {
		return nil, ErrUnavailable
	}
	sameFolder := func(path string) bool {
		if strings.TrimSpace(path) == "" {
			return false
		}
		info, statErr := os.Stat(path) // #nosec G304 -- a path Ori itself recorded for a workspace; only compared, never read
		return statErr == nil && os.SameFile(selectedInfo, info)
	}
	for _, id := range ids {
		if id == ignoreWorkspaceID {
			continue
		}
		candidate, getErr := store.Get(id)
		if getErr != nil || candidate == nil || candidate.ID == ignoreWorkspaceID {
			continue
		}
		owner := &FolderOwner{WorkspaceID: candidate.ID, Name: candidate.Name, Slug: candidate.FolderSlug}
		if locator, locatorErr := workspace.GetProjectEntryLocator(candidate.SharedData); locatorErr == nil &&
			locator != nil && locator.Kind == workspace.ProjectEntryDirectoryReference {
			if reference, referenceErr := candidate.GetDirectoryReference(locator.DirectoryReferenceID); referenceErr == nil &&
				reference != nil && sameFolder(reference.Path) {
				return owner, nil
			}
		}
		if imported, ok := candidate.SharedData[folderImportKey].(map[string]any); ok {
			if path, _ := imported["path"].(string); sameFolder(path) {
				return owner, nil
			}
		}
		if folder, folderErr := store.GetFolderPath(candidate.ID); folderErr == nil && sameFolder(folder) {
			return owner, nil
		}
	}
	return nil, nil
}

// LockAttachCommit serializes every attach that records a folder, from its
// final ownership check to its durable write, across the guided journey and
// the create modal. Without it two requests could both find a folder free and
// both attach it.
func LockAttachCommit() (unlock func()) {
	connectionCommitMu.Lock()
	return connectionCommitMu.Unlock
}

// RecordAttachedProject references root in place on ws and points the typed
// project-entry locator at entry inside it. Nothing in root is created,
// copied, moved, or modified. ws.ID must already be final: the reference is
// owned by that workspace and the resolver rejects a mismatch.
func RecordAttachedProject(ws *workspace.Workspace, name, root, entry, referenceID string) error {
	if ws == nil || strings.TrimSpace(ws.ID) == "" {
		return ErrInvalid
	}
	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{ID: referenceID, Name: name, Path: root}); err != nil {
		return ErrUnavailable
	}
	if err := workspace.SetProjectEntryLocator(ws.SharedData, workspace.ProjectEntryLocator{
		SchemaVersion: workspace.ProjectEntryLocatorSchemaVersion,
		Kind:          workspace.ProjectEntryDirectoryReference, DirectoryReferenceID: referenceID,
		RelativePath: entry,
	}); err != nil {
		return ErrInvalid
	}
	return nil
}
