package backuprecovery

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
)

// maxCoverageFolders bounds one coverage read. Past it the result is marked
// truncated, never reported as a healthy all-clear.
const maxCoverageFolders = 32

const (
	CoverageCovered    = "covered"
	CoverageNotCovered = "not_covered"
	CoverageUnknown    = "unknown"
)

// AdoptedFolder is host-owned input: the stored folder a workspace was created
// from or connected to, or an active Home library root. Path is compared inside
// this package and is never returned in any projection.
type AdoptedFolder struct {
	WorkspaceID string
	Path        string
}

// FolderCoverage is one adopted folder's stored-evidence answer. It has no
// path, volume, repository, snapshot or credential field. Reason is a fixed
// code; Provider is a fixed provider id, never a provider-supplied label.
type FolderCoverage struct {
	WorkspaceID         string     `json:"workspace_id"`
	State               string     `json:"state"`
	Reason              string     `json:"reason"`
	Provider            string     `json:"provider,omitempty"`
	CoveringWorkspaceID string     `json:"covering_workspace_id,omitempty"`
	LastCompletedAt     *time.Time `json:"last_completed_at,omitempty"`
}

// CoverageConsole is one installed Backup & Recovery workspace in scope.
// Configured reports a saved configuration, not a successful read.
type CoverageConsole struct {
	WorkspaceID string `json:"workspace_id"`
	Configured  bool   `json:"configured"`
}

type CoverageRead struct {
	Folders  []FolderCoverage  `json:"folders"`
	Consoles []CoverageConsole `json:"consoles"`
	// Complete is false when an in-scope configuration could not be read or
	// either bound truncated the read. Truncated names the folder cap.
	Complete  bool `json:"complete"`
	Truncated bool `json:"truncated"`
}

// ReadFolderCoverage compares host-supplied adopted folders against the stored
// configuration sources and latest observations of the Backup & Recovery
// workspaces among workspaceIDs, in one read transaction. It performs no
// provider, filesystem or credential IO and returns no path. Folders whose
// workspace is not among workspaceIDs are dropped and make the read incomplete.
func (s *Service) ReadFolderCoverage(ctx context.Context, owner string, workspaceIDs []string, folders []AdoptedFolder) (CoverageRead, error) {
	result := CoverageRead{Folders: []FolderCoverage{}, Consoles: []CoverageConsole{}, Complete: true}
	if owner != "local" {
		return result, ErrWorkspaceUnavailable
	}
	release, err := s.enter()
	if err != nil {
		return result, err
	}
	defer release()
	ids, err := scopeIdentities(workspaceIDs)
	if err != nil {
		return result, err
	}
	inScope := map[string]bool{}
	for _, id := range ids {
		inScope[id] = true
	}
	candidates := make([]AdoptedFolder, 0, len(folders))
	seen := map[AdoptedFolder]bool{}
	for _, folder := range folders {
		if seen[folder] {
			continue
		}
		if !inScope[folder.WorkspaceID] {
			result.Complete = false
			continue
		}
		seen[folder] = true
		candidates = append(candidates, folder)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].WorkspaceID != candidates[j].WorkspaceID {
			return candidates[i].WorkspaceID < candidates[j].WorkspaceID
		}
		return candidates[i].Path < candidates[j].Path
	})
	if len(candidates) > maxCoverageFolders {
		candidates = candidates[:maxCoverageFolders]
		result.Truncated = true
		result.Complete = false
	}

	type configuration struct {
		workspace string
		provider  string
		sources   []string
		state     string
		reason    string
		completed *time.Time
	}
	var configurations []configuration
	unreadable := false
	err = s.store.transaction(ctx, func(tx *sql.Tx) error {
		truncated, err := s.scanScopedBackupRecords(ctx, tx, owner, ids, maxSummaryWorkspaces, func(row scopedBackupRecord) {
			if row.failure != "" {
				unreadable = true
				return
			}
			console := CoverageConsole{WorkspaceID: row.id}
			if row.record.state != nil && row.record.state.Configuration != nil {
				console.Configured = true
				cfg := row.record.state.Configuration
				state, reason, completed := configurationCoverage(s.storedStatus(row.record))
				entry := configuration{workspace: row.id, provider: cfg.Provider, state: state, reason: reason, completed: completed}
				for _, source := range cfg.Sources {
					entry.sources = append(entry.sources, source.Path)
				}
				configurations = append(configurations, entry)
			}
			result.Consoles = append(result.Consoles, console)
		})
		unreadable = unreadable || truncated
		return err
	})
	if err != nil {
		return CoverageRead{Folders: []FolderCoverage{}, Consoles: []CoverageConsole{}, Complete: false}, err
	}
	if unreadable {
		result.Complete = false
	}

	rank := map[string]int{CoverageNotCovered: 1, CoverageUnknown: 2, CoverageCovered: 3}
	for _, folder := range candidates {
		row := FolderCoverage{WorkspaceID: folder.WorkspaceID}
		if !localPath(folder.Path) {
			row.State, row.Reason = CoverageUnknown, "folder_unavailable"
			result.Folders = append(result.Folders, row)
			continue
		}
		for _, cfg := range configurations {
			if !coversFolder(cfg.sources, folder.Path) {
				continue
			}
			better := rank[cfg.state] > rank[row.State]
			if !better && cfg.state == row.State && cfg.state == CoverageCovered && cfg.completed != nil && row.LastCompletedAt != nil {
				better = cfg.completed.After(*row.LastCompletedAt)
			}
			if better {
				row.State, row.Reason, row.Provider, row.CoveringWorkspaceID = cfg.state, cfg.reason, cfg.provider, cfg.workspace
				row.LastCompletedAt = nil
				if cfg.state == CoverageCovered && cfg.completed != nil {
					at := *cfg.completed
					row.LastCompletedAt = &at
				}
			}
		}
		if row.State == "" {
			switch {
			case unreadable:
				// An unreadable configuration might name this folder: unknown, never
				// a not-covered claim that the stored evidence cannot support.
				row.State, row.Reason = CoverageUnknown, "configuration_unavailable"
			case len(configurations) == 0:
				row.State, row.Reason = CoverageNotCovered, "no_configuration"
			default:
				row.State, row.Reason = CoverageNotCovered, "not_in_any_source"
			}
		}
		result.Folders = append(result.Folders, row)
	}
	return result, nil
}

// coversFolder reports whether a configured source is the folder or one of its
// ancestors. Both are clean absolute paths; comparison is exact and resolves
// no links, so a spelling mismatch reads as not covered, never as covered.
func coversFolder(sources []string, folder string) bool {
	for _, source := range sources {
		if folder == source || strings.HasPrefix(folder, source+"/") {
			return true
		}
	}
	return false
}

// configurationCoverage maps one configuration's stored status to the state it
// gives the folders its sources contain. Only a current observation with at
// least one completed backup covers; evidence that is stale, from an earlier
// configuration, clock-suspect or pending removal is unknown.
func configurationCoverage(status Status) (state, reason string, completed *time.Time) {
	switch {
	case status.RemovalPending || status.Health.Observation == "clock_suspect" || status.Health.Recency == "clock_suspect":
		return CoverageUnknown, "evidence_unavailable", nil
	case status.LastSuccessfulRead == nil:
		return CoverageNotCovered, "no_completed_backup_observed", nil
	case !status.EvidenceCurrent:
		return CoverageUnknown, "evidence_unavailable", nil
	case status.LastCompletedAt == nil:
		return CoverageNotCovered, "no_completed_backup_observed", nil
	case status.Health.Observation == "stale":
		return CoverageUnknown, "evidence_stale", nil
	case status.Health.Observation == "current":
		return CoverageCovered, "completed_backup_observed", status.LastCompletedAt
	default:
		return CoverageUnknown, "evidence_unavailable", nil
	}
}
