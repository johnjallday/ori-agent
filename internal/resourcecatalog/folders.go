package resourcecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Owner folders are folders the owner adds to a category that declares
// owner_folders (contract §4.5). Like grants they belong to this computer:
// the path lives in Ori's data directory, never in the Studio folder, and a
// folder is read only after the owner approves it in a scope review.
const (
	// MaxOwnerFolders bounds the folders one Studio has on one computer.
	MaxOwnerFolders = 8
	// FolderSourcePrefix starts every owner folder's source ID. A provider's
	// own sources never use it.
	FolderSourcePrefix = "folder_"

	foldersFile        = "folders.json"
	maxFoldersBytes    = 64 << 10
	maxFolderPathChars = 1024
)

// ErrFolderLimit means the Studio already has MaxOwnerFolders on this
// computer.
var ErrFolderLimit = errors.New("this catalog already has as many folders as it can hold")

// OwnerFolder is one folder the owner added. Path is the canonical absolute
// path the host checked when it was added.
type OwnerFolder struct {
	ID       string    `json:"id"`
	Category string    `json:"category"`
	Path     string    `json:"path"`
	AddedAt  time.Time `json:"added_at"`
}

type foldersDocument struct {
	SchemaVersion     int           `json:"schema_version"`
	StudioWorkspaceID string        `json:"studio_workspace_id"`
	Folders           []OwnerFolder `json:"folders"`
}

// FolderRef tells the provider which folder an inventory page reads.
type FolderRef struct {
	Category string `json:"category"`
	Path     string `json:"path"`
}

// folderInput is one owner folder in the sources operation's input.
type folderInput struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Path     string `json:"path"`
}

// FolderID is an owner folder's source ID: the same category and path always
// give the same ID, so re-adding a removed folder finds its old entries.
func FolderID(category, path string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + path))
	return FolderSourcePrefix + hex.EncodeToString(sum[:8])
}

// IsFolderSource reports whether a source ID names an owner folder.
func IsFolderSource(id string) bool { return strings.HasPrefix(id, FolderSourcePrefix) }

// validFolderPath accepts the canonical absolute path of a folder: one plain
// line, already clean, with nothing left for the provider to resolve.
func validFolderPath(path string) bool {
	return plainLine(path, maxFolderPathChars) && filepath.IsAbs(path) && filepath.Clean(path) == path
}

func (p paths) folders() string { return filepath.Join(p.dataDir, foldersFile) }

func readFolders(p paths) ([]OwnerFolder, error) {
	var document foldersDocument
	found, err := readDocument(p.folders(), maxFoldersBytes, &document)
	if err != nil || !found {
		return nil, err
	}
	if document.SchemaVersion != DocumentSchemaVersion {
		return nil, fmt.Errorf("%w: folders schema %d", ErrCorrupt, document.SchemaVersion)
	}
	return document.Folders, nil
}

func ownerFolder(folders []OwnerFolder, id string) *OwnerFolder {
	for index := range folders {
		if folders[index].ID == id {
			return &folders[index]
		}
	}
	return nil
}

// AddFolder adds a folder to a category that accepts owner folders. It reads
// nothing: the next scope review discloses the folder and only an approval
// lets the provider read it. path must already be the canonical folder the
// caller checked with the shared typed-path rules. Adding the same folder
// again returns it unchanged.
func (s *Service) AddFolder(owner, studioID, category, path string) (OwnerFolder, error) {
	ref, status, err := s.attachedStudio(owner, studioID)
	if err != nil {
		return OwnerFolder{}, err
	}
	declared, ok := status.Declaration.Category(category)
	if !ok || !declared.OwnerFolders || !validFolderPath(path) {
		return OwnerFolder{}, ErrInvalidRequest
	}
	folder := OwnerFolder{ID: FolderID(category, path), Category: category, Path: path, AddedAt: s.now()}
	err = withLock(ref.paths.dataDir, func() error {
		folders, readErr := readFolders(ref.paths)
		if readErr != nil {
			return readErr
		}
		if existing := ownerFolder(folders, folder.ID); existing != nil {
			folder = *existing
			return nil
		}
		if len(folders) >= MaxOwnerFolders {
			return ErrFolderLimit
		}
		folders = append(folders, folder)
		return writeDocument(ref.paths.folders(), foldersDocument{
			SchemaVersion: DocumentSchemaVersion, StudioWorkspaceID: ref.workspace.ID, Folders: folders,
		})
	})
	if err != nil {
		return OwnerFolder{}, err
	}
	return folder, nil
}

// RemoveFolder forgets an owner folder on this computer. An approved folder
// is withdrawn in the same step, so no page of it is read or published again,
// and what was read from it stays readable marked Access withdrawn. Like a
// withdrawal it works without the provider and on a detached Studio.
func (s *Service) RemoveFolder(owner, studioID, folderID string) (OwnerView, error) {
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return OwnerView{}, err
	}
	if !IsFolderSource(folderID) {
		return OwnerView{}, ErrInvalidRequest
	}
	err = withLock(ref.paths.dataDir, func() error {
		folders, readErr := readFolders(ref.paths)
		if readErr != nil {
			return readErr
		}
		index := slices.IndexFunc(folders, func(folder OwnerFolder) bool { return folder.ID == folderID })
		if index < 0 {
			return ErrInvalidRequest
		}
		// Withdraw first: a stop between the two writes leaves a listed folder
		// that is no longer read, never a forgotten one that still is.
		grants, hasGrants, readErr := readGrants(ref.paths)
		if readErr != nil {
			return readErr
		}
		if granted := grantSource(grants, folderID); hasGrants && granted != nil && granted.Active() {
			now := s.now()
			grants.ApprovalRevision++
			granted.WithdrawnAt = &now
			if writeErr := writeDocument(ref.paths.grants(), grants); writeErr != nil {
				return writeErr
			}
		}
		return writeDocument(ref.paths.folders(), foldersDocument{
			SchemaVersion: DocumentSchemaVersion, StudioWorkspaceID: ref.workspace.ID, Folders: slices.Delete(folders, index, index+1),
		})
	})
	if err != nil {
		return OwnerView{}, err
	}
	return s.Overview(owner, studioID)
}

// sourcesInput is the sources operation's input: the wire schema and this
// computer's folders in categories that still accept them. It returns the
// folders it sent, which the answer must report exactly.
func sourcesInput(declaration *Declaration, folders []OwnerFolder) ([]byte, []OwnerFolder) {
	input := struct {
		SchemaVersion int           `json:"schema_version"`
		Folders       []folderInput `json:"folders,omitempty"`
	}{SchemaVersion: WireSchemaVersion}
	var sent []OwnerFolder
	for _, folder := range folders {
		if category, ok := declaration.Category(folder.Category); ok && category.OwnerFolders {
			input.Folders = append(input.Folders, folderInput{ID: folder.ID, Category: folder.Category, Path: folder.Path})
			sent = append(sent, folder)
		}
	}
	encoded, _ := json.Marshal(input)
	return encoded, sent
}

// checkFolderSources requires a sources answer to report every folder it was
// sent, once and as a file source in the folder's own category, and no owner
// folder it was not sent.
func checkFolderSources(answer SourcesAnswer, sent []OwnerFolder) error {
	reported := 0
	for _, source := range answer.Sources {
		if !IsFolderSource(source.ID) {
			continue
		}
		folder := ownerFolder(sent, source.ID)
		if folder == nil || source.Category != folder.Category || source.Access != AccessFile {
			return errors.New("the answer reports a folder it was not given")
		}
		reported++
	}
	if reported != len(sent) {
		return errors.New("the answer leaves out a folder it was given")
	}
	return nil
}
