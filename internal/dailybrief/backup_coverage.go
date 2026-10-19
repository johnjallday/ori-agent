package dailybrief

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
)

// MaxAdoptedFolders bounds one coverage read across the saved scope. Past it
// the read is incomplete, never a healthy all-clear.
const MaxAdoptedFolders = 32

// BackupCoverageSource is a stored-only read: it compares the scope's adopted
// folders with stored backup evidence in the owning Backup & Recovery service.
// It exposes no provider read, path or backup/restore action.
type BackupCoverageSource interface {
	BriefCoverage(context.Context, string, WorkspaceScopeResult, []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error)
}

// ScopedAdoptedFolders lists the adopted folders of the scope's workspaces in
// scope order, capped at MaxAdoptedFolders. A workspace's own Ori-managed
// folder (a Personal HQ links its own) is never one. Paths are server-only:
// they go to the owning backup reader and nowhere else.
func ScopedAdoptedFolders(scope WorkspaceScopeResult) (folders []backuprecovery.AdoptedFolder, truncated bool) {
	for _, candidate := range scope.Workspaces {
		if candidate.Workspace == nil || candidate.Workspace.ID != candidate.ID {
			continue
		}
		managed := scope.managedFolders[candidate.ID]
		for _, folder := range candidate.Workspace.AdoptedFolders() {
			if managed != "" && (folder.Path == managed || strings.HasPrefix(folder.Path, managed+"/")) {
				continue
			}
			if len(folders) >= MaxAdoptedFolders {
				return folders, true
			}
			folders = append(folders, backuprecovery.AdoptedFolder{WorkspaceID: candidate.ID, Path: folder.Path})
		}
	}
	return folders, false
}

// CoverageWorkspace is safe display/navigation identity from the scope.
type CoverageWorkspace struct {
	ID   string `json:"id"`
	Slug string `json:"slug,omitempty"`
	Name string `json:"name"`
}

// WorkspaceCoverage is one adopted workspace's answer. A workspace with
// several adopted folders reports its weakest state.
type WorkspaceCoverage struct {
	Workspace       CoverageWorkspace  `json:"workspace"`
	State           string             `json:"state"`
	Reason          string             `json:"reason"`
	Provider        string             `json:"provider,omitempty"`
	Covering        *CoverageWorkspace `json:"covering,omitempty"`
	LastCompletedAt *time.Time         `json:"last_completed_at,omitempty"`
}

// CoverageResult carries no path. Console is the Backup & Recovery workspace a
// protection action opens (a configured one first); nil when none is in scope.
type CoverageResult struct {
	Workspaces []WorkspaceCoverage `json:"workspaces"`
	NotCovered int                 `json:"not_covered"`
	Configured bool                `json:"configured"`
	Console    *CoverageWorkspace  `json:"console,omitempty"`
	Complete   bool                `json:"complete"`
}

// Adopted is the number of workspaces with at least one evaluated adopted folder.
func (r CoverageResult) Adopted() int { return len(r.Workspaces) }

var coverageRank = map[string]int{backuprecovery.CoverageCovered: 1, backuprecovery.CoverageUnknown: 2, backuprecovery.CoverageNotCovered: 3}

// ReadCoverage resolves the scope's adopted folders and their stored coverage.
// Covering and console identities outside the scope are dropped.
func ReadCoverage(ctx context.Context, source BackupCoverageSource, owner string, scope WorkspaceScopeResult) CoverageResult {
	out := CoverageResult{Workspaces: []WorkspaceCoverage{}, Complete: scope.Complete}
	folders, truncated := ScopedAdoptedFolders(scope)
	if truncated {
		out.Complete = false
	}
	if source == nil || owner != "local" {
		out.Complete = false
		return out
	}
	read, err := source.BriefCoverage(ctx, owner, scope, folders)
	if err != nil {
		out.Complete = false
		return out
	}
	out.Complete = out.Complete && read.Complete
	identity := map[string]CoverageWorkspace{}
	for _, ws := range scope.Workspaces {
		if ws.Workspace != nil && ws.Workspace.ID == ws.ID {
			identity[ws.ID] = CoverageWorkspace{ID: ws.ID, Slug: ws.Slug, Name: ws.Name}
		}
	}
	for _, console := range read.Consoles {
		ws, ok := identity[console.WorkspaceID]
		if !ok || ws.Slug == "" {
			continue
		}
		out.Configured = out.Configured || console.Configured
		if out.Console == nil || console.Configured && !consoleConfigured(read.Consoles, out.Console.ID) {
			copied := ws
			out.Console = &copied
		}
	}
	byWorkspace := map[string]int{}
	for _, folder := range read.Folders {
		ws, ok := identity[folder.WorkspaceID]
		if !ok || coverageRank[folder.State] == 0 {
			out.Complete = false
			continue
		}
		row := WorkspaceCoverage{Workspace: ws, State: folder.State, Reason: folder.Reason}
		if folder.State == backuprecovery.CoverageCovered {
			covering, ok := identity[folder.CoveringWorkspaceID]
			if !ok || covering.Slug == "" || folder.LastCompletedAt == nil {
				// Evidence from outside the scope is not a protection claim.
				row.State, row.Reason = backuprecovery.CoverageUnknown, "evidence_unavailable"
			} else {
				at := folder.LastCompletedAt.UTC()
				row.Provider, row.Covering, row.LastCompletedAt = folder.Provider, &covering, &at
			}
		}
		index, seen := byWorkspace[ws.ID]
		if !seen {
			byWorkspace[ws.ID] = len(out.Workspaces)
			out.Workspaces = append(out.Workspaces, row)
			continue
		}
		if coverageRank[row.State] > coverageRank[out.Workspaces[index].State] {
			out.Workspaces[index] = row
		}
	}
	sort.SliceStable(out.Workspaces, func(i, j int) bool { return out.Workspaces[i].Workspace.ID < out.Workspaces[j].Workspace.ID })
	for _, row := range out.Workspaces {
		if row.State == backuprecovery.CoverageNotCovered {
			out.NotCovered++
		}
	}
	return out
}

func consoleConfigured(consoles []backuprecovery.CoverageConsole, id string) bool {
	for _, console := range consoles {
		if console.WorkspaceID == id {
			return console.Configured
		}
	}
	return false
}

// CoverageConsoleRoute opens the owning console's Overview at its coverage
// section, from stable scope identity only.
func CoverageConsoleRoute(console *CoverageWorkspace) string {
	if console == nil || !backupIdentity.MatchString(console.ID) || !backupSlug.MatchString(console.Slug) {
		return ""
	}
	return BackupRoute(SourceRef{WorkspaceID: console.ID, WorkspaceSlug: console.Slug, EntityType: EntityBackupObservation, EntityID: "coverage"}) + "&section=coverage"
}
