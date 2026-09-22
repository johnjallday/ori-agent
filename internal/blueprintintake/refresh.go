package blueprintintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// RefreshMode identifies which unattended source checks should run.
type RefreshMode struct {
	Folders bool
	Links   bool
}

// SourceRefreshFailure is one independently failed source refresh.
type SourceRefreshFailure struct {
	SourceID string `json:"source_id"`
	Message  string `json:"message"`
}

// SourceRefresh reports only snapshots whose parsed content changed, plus
// folder files that disappeared. Unchanged snapshots never reach the model.
type SourceRefresh struct {
	Changed  []SourceRecord         `json:"changed,omitempty"`
	Removed  []SourceRecord         `json:"removed,omitempty"`
	Failures []SourceRefreshFailure `json:"failures,omitempty"`
}

// RefreshSources re-reads consented external sources in place. It never copies
// a folder file and never runs the intake skill.
func (s *SourceService) RefreshSources(ctx context.Context, workspaceID, intakeKey string, mode RefreshMode) (SourceRefresh, error) {
	requirement, err := s.Requirement(workspaceID, intakeKey)
	if err != nil {
		return SourceRefresh{}, err
	}
	consent, err := s.ConsentStatus(ctx, workspaceID, requirement.Key)
	if err != nil || !consent.Accepted {
		return SourceRefresh{}, ErrConsentNeeded
	}

	lock := s.lockFor(workspaceID, requirement.Key)
	lock.Lock()
	defer lock.Unlock()
	state, stateDir, _, err := s.loadState(workspaceID)
	if err != nil {
		return SourceRefresh{}, err
	}
	result := SourceRefresh{}
	dirty := false
	if mode.Links && requirement.Sources.URL {
		if s.refreshLinks(ctx, &state, stateDir, requirement, &result) {
			dirty = true
		}
	}
	if mode.Folders && requirement.Sources.DirectoryKey != "" {
		changed, refreshErr := s.refreshFolders(ctx, &state, stateDir, requirement, &result)
		if refreshErr != nil {
			return SourceRefresh{}, refreshErr
		}
		dirty = dirty || changed
	}
	if dirty {
		if err := saveSourceState(stateDir, state); err != nil {
			return SourceRefresh{}, err
		}
	}
	return result, nil
}

func (s *SourceService) refreshLinks(ctx context.Context, state *sourceState, stateDir string, requirement workspace.IntakeRequirement, result *SourceRefresh) bool {
	dirty := false
	for index := range state.Sources {
		source := &state.Sources[index]
		if source.IntakeKey != requirement.Key || source.Kind != "link" {
			continue
		}
		snapshot, err := s.fetcher.Fetch(ctx, source.URL)
		if err != nil {
			result.Failures = append(result.Failures, SourceRefreshFailure{SourceID: source.ID, Message: "Ori could not refresh this page."})
			continue
		}
		text := strings.TrimSpace(snapshot.Content)
		digest := sha256.Sum256([]byte(text))
		hash := hex.EncodeToString(digest[:])
		now := s.now().UTC()
		source.FetchedAt = &now
		dirty = true
		if hash == source.ContentHash {
			continue
		}
		if err := os.WriteFile(filepath.Join(stateDir, filepath.FromSlash(source.ParsedTextFile)), []byte(text), 0o600); err != nil { // #nosec G304 -- path was generated and persisted by this package
			result.Failures = append(result.Failures, SourceRefreshFailure{SourceID: source.ID, Message: "Ori could not store the refreshed page."})
			continue
		}
		if err := os.WriteFile(filepath.Join(stateDir, filepath.FromSlash(source.SnapshotFile)), snapshot.Body, 0o600); err != nil { // #nosec G304 -- path was generated and persisted by this package
			result.Failures = append(result.Failures, SourceRefreshFailure{SourceID: source.ID, Message: "Ori could not store the refreshed page."})
			continue
		}
		source.URL = snapshot.URL
		source.Title = snapshot.Title
		if strings.TrimSpace(snapshot.Title) != "" {
			source.Name = strings.TrimSpace(snapshot.Title)
		}
		source.ContentType = snapshot.ContentType
		source.ContentHash = hash
		source.Size = int64(len(snapshot.Body))
		source.Status = SourceStatusParsed
		result.Changed = append(result.Changed, *source)
	}
	return dirty
}

func (s *SourceService) refreshFolders(ctx context.Context, state *sourceState, stateDir string, requirement workspace.IntakeRequirement, result *SourceRefresh) (bool, error) {
	dirty := false
	folders := make([]SourceRecord, 0, 1)
	for _, source := range state.Sources {
		if source.IntakeKey == requirement.Key && source.Kind == "folder" {
			folders = append(folders, source)
		}
	}
	for _, folder := range folders {
		root, err := filepath.EvalSymlinks(filepath.Clean(folder.FolderPath))
		if err != nil {
			result.Failures = append(result.Failures, SourceRefreshFailure{SourceID: folder.ID, Message: "Ori could not refresh this folder."})
			continue
		}
		entries, err := os.ReadDir(root) // #nosec G304 -- path was issued by the scoped native picker and persisted by this package
		if err != nil {
			result.Failures = append(result.Failures, SourceRefreshFailure{SourceID: folder.ID, Message: "Ori could not refresh this folder."})
			continue
		}
		old := make(map[string]SourceRecord)
		baseFiles := 0
		for _, source := range state.Sources {
			switch {
			case source.IntakeKey == requirement.Key && source.Kind == "folder_file" && source.ParentID == folder.ID:
				old[source.Name] = source
			case source.IntakeKey == requirement.Key && source.Kind == "file":
				baseFiles++
			}
		}
		remaining := MaxFilesPerIntake - baseFiles
		if remaining < 0 {
			remaining = 0
		}
		seen := make(map[string]bool)
		children := make([]SourceRecord, 0, min(len(entries), remaining))
		omitted := 0
		for _, entry := range entries {
			resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(root, entry.Name()))
			if resolveErr != nil {
				continue
			}
			info, statErr := os.Stat(resolved)
			if statErr != nil || !info.Mode().IsRegular() {
				continue
			}
			if len(children) >= remaining {
				omitted++
				continue
			}
			previous := old[entry.Name()]
			record, message, readErr := s.readFolderFileWithID(ctx, stateDir, root, resolved, entry.Name(), folder.ID, previous.ID, requirement)
			if readErr != nil {
				return dirty, readErr
			}
			if previous.ID != "" {
				record.AddedAt = previous.AddedAt
			}
			record.Message = message
			children = append(children, record)
			seen[entry.Name()] = true
			if record.Status == SourceStatusParsed && (previous.ID == "" || previous.ContentHash != record.ContentHash || previous.Status != record.Status) {
				result.Changed = append(result.Changed, record)
			} else if previous.Status == SourceStatusParsed && record.Status != SourceStatusParsed {
				result.Removed = append(result.Removed, previous)
			}
		}
		for name, previous := range old {
			if !seen[name] && previous.Status == SourceStatusParsed {
				result.Removed = append(result.Removed, previous)
			}
		}

		folder.Omitted = omitted
		folder.Message = ""
		if omitted > 0 {
			folder.Message = fmt.Sprintf("%d immediate file(s) were left out by the 50-file limit.", omitted)
		}
		next := make([]SourceRecord, 0, len(state.Sources)-len(old)+len(children))
		for _, source := range state.Sources {
			if source.IntakeKey == requirement.Key && source.Kind == "folder_file" && source.ParentID == folder.ID {
				continue
			}
			if source.ID == folder.ID {
				next = append(next, folder)
			} else {
				next = append(next, source)
			}
		}
		next = append(next, children...)
		state.Sources = next
		dirty = true
	}
	return dirty, nil
}
