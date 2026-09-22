package blueprintintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type FolderResult struct {
	Folder  FileResult   `json:"folder"`
	Files   []FileResult `json:"files"`
	Omitted int          `json:"omitted"`
}

func (s *SourceService) AddFolder(ctx context.Context, workspaceID, intakeKey, selectionToken string) (FolderResult, error) {
	requirement, err := s.Requirement(workspaceID, intakeKey)
	if err != nil {
		return FolderResult{}, err
	}
	if requirement.Sources.DirectoryKey == "" {
		return FolderResult{}, ErrFolderDisabled
	}
	consent, err := s.ConsentStatus(ctx, workspaceID, requirement.Key)
	if err != nil || !consent.Accepted {
		return FolderResult{}, ErrConsentNeeded
	}
	if s.selections == nil {
		return FolderResult{}, fmt.Errorf("trusted folder selection is unavailable")
	}
	selected, err := s.selections.ResolveFor(strings.TrimSpace(selectionToken), workspaceID)
	if err != nil {
		return FolderResult{}, fmt.Errorf("trusted folder selection is unavailable")
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(selected))
	if err != nil {
		return FolderResult{}, fmt.Errorf("read selected folder: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return FolderResult{}, fmt.Errorf("the selected path is not a folder")
	}
	entries, err := os.ReadDir(root) // #nosec G304 -- root comes from a scoped trusted native-picker token
	if err != nil {
		return FolderResult{}, fmt.Errorf("read selected folder: %w", err)
	}

	lock := s.lockFor(workspaceID, requirement.Key)
	lock.Lock()
	defer lock.Unlock()
	state, stateDir, _, err := s.loadState(workspaceID)
	if err != nil {
		return FolderResult{}, err
	}
	kept := state.Sources[:0]
	for _, source := range state.Sources {
		if source.IntakeKey == requirement.Key && (source.Kind == "folder" || source.Kind == "folder_file") {
			continue
		}
		kept = append(kept, source)
	}
	state.Sources = kept

	folderID, err := newSourceID()
	if err != nil {
		return FolderResult{}, err
	}
	remaining := MaxFilesPerIntake - countFiles(state.Sources, requirement.Key)
	if remaining < 0 {
		remaining = 0
	}
	folderRecord := SourceRecord{ID: folderID, IntakeKey: requirement.Key, Kind: "folder", Name: filepath.Base(root), FolderPath: root, Status: SourceStatusSelected, AddedAt: s.now().UTC()}
	results := make([]FileResult, 0, min(len(entries), remaining))
	omitted := 0
	seenFiles := 0
	for _, entry := range entries {
		candidate := filepath.Join(root, entry.Name())
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr != nil {
			continue
		}
		entryInfo, statErr := os.Stat(resolved)
		if statErr != nil || !entryInfo.Mode().IsRegular() {
			continue
		}
		if seenFiles >= remaining {
			omitted++
			continue
		}
		seenFiles++
		record, message, readErr := s.readFolderFile(ctx, stateDir, root, resolved, entry.Name(), folderID, requirement)
		if readErr != nil {
			return FolderResult{}, readErr
		}
		record.Message = message
		state.Sources = append(state.Sources, record)
		results = append(results, fileResult(record, message))
	}
	folderRecord.Omitted = omitted
	if omitted > 0 {
		folderRecord.Message = fmt.Sprintf("%d immediate file(s) were left out by the 50-file limit.", omitted)
	}
	state.Sources = append(state.Sources, folderRecord)
	if err := saveSourceState(stateDir, state); err != nil {
		return FolderResult{}, err
	}
	folderResult := fileResult(folderRecord, "")

	return FolderResult{Folder: folderResult, Files: results, Omitted: omitted}, nil
}

func (s *SourceService) readFolderFile(ctx context.Context, stateDir, root, resolved, name, folderID string, requirement workspace.IntakeRequirement) (SourceRecord, string, error) {
	return s.readFolderFileWithID(ctx, stateDir, root, resolved, name, folderID, "", requirement)
}

func (s *SourceService) readFolderFileWithID(ctx context.Context, stateDir, root, resolved, name, folderID, sourceID string, requirement workspace.IntakeRequirement) (SourceRecord, string, error) {
	if err := ctx.Err(); err != nil {
		return SourceRecord{}, "", err
	}
	record := SourceRecord{IntakeKey: requirement.Key, Kind: "folder_file", Name: name, FolderPath: root, ParentID: folderID, Status: SourceStatusSkipped, AddedAt: s.now().UTC()}
	id := strings.TrimSpace(sourceID)
	if id == "" {
		var err error
		id, err = newSourceID()
		if err != nil {
			return SourceRecord{}, "", err
		}
	}
	record.ID = id
	if !pathContained(root, resolved) {
		return record, "This symlink points outside the selected folder and was not read.", nil
	}
	file, err := os.Open(resolved) // #nosec G304 -- resolved path is contained in the trusted selected folder above
	if err != nil {
		record.Status = SourceStatusUnreadable
		return record, "Ori could not read this file.", nil
	}
	data, readErr := io.ReadAll(io.LimitReader(file, fileparser.MaxFileSize+1))
	_ = file.Close()
	record.Size = int64(len(data))
	if readErr != nil {
		record.Status = SourceStatusUnreadable
		return record, "Ori could not read this file.", nil
	}
	if err := fileparser.ValidateFileSize(record.Size); err != nil {
		record.Status = SourceStatusTooLarge
		return record, "The file is larger than 10 MB.", nil
	}
	digest := sha256.Sum256(data)
	record.ContentHash = hex.EncodeToString(digest[:])
	if !extensionAllowed(name, requirement.AcceptedExtensions) {
		return record, "This file type is not supported by this intake.", nil
	}
	parsed, err := fileparser.ParseFile(name, data)
	if err != nil {
		record.Status = SourceStatusUnreadable
		return record, "Ori could not read this file.", nil
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "text"), 0o750); err != nil {
		return SourceRecord{}, "", err
	}
	textName := id + ".txt"
	if err := os.WriteFile(filepath.Join(stateDir, "text", textName), []byte(parsed), 0o600); err != nil {
		return SourceRecord{}, "", err
	}
	record.Status = SourceStatusParsed
	record.ParsedTextFile = filepath.ToSlash(filepath.Join("text", textName))
	return record, "", nil
}

func pathContained(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
