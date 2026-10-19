package server

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

type dailyBriefBackupSource struct{ reader backupSummaryReader }

func (source dailyBriefBackupSource) BriefBackups(ctx context.Context, owner string, scope dailybrief.WorkspaceScopeResult) (backuprecovery.SummaryRead, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return readScopedBackupSummaries(ctx, owner, scope, source.reader)
}

type backupSummaryReader interface {
	ReadBackupSummaries(context.Context, string, []string) (backuprecovery.SummaryRead, error)
}

// readScopedBackupSummaries consumes the SAME canonical saved selected/all scope
// as the surrounding HQ/Brief read. It neither enumerates an extra workspace nor
// trusts a hydrated name/owner as backup authority: the domain rechecks SQL.
func readScopedBackupSummaries(ctx context.Context, owner string, scope dailybrief.WorkspaceScopeResult, reader backupSummaryReader) (backuprecovery.SummaryRead, error) {
	result := backuprecovery.SummaryRead{Sources: []backuprecovery.SummaryFacts{}, Failures: []backuprecovery.SummaryFailure{}, Complete: scope.Complete}
	if owner != "local" {
		return result, backuprecovery.ErrWorkspaceUnavailable
	}
	if reader == nil {
		result.Complete = false
		result.Failures = append(result.Failures, backuprecovery.SummaryFailure{Code: "backup_reader_unavailable"})
		return result, nil
	}
	ids, allowed, complete := scopedBackupIDs(scope)
	result.Complete = result.Complete && complete
	loaded, err := reader.ReadBackupSummaries(ctx, owner, ids)
	if err != nil {
		result.Complete = false
		result.Failures = append(result.Failures, backuprecovery.SummaryFailure{Code: "backup_reader_unavailable"})
		return result, nil
	}
	result.Complete = result.Complete && loaded.Complete
	result.Truncated = loaded.Truncated
	for _, source := range loaded.Sources {
		if allowed[source.WorkspaceID] {
			result.Sources = append(result.Sources, source)
		} else {
			result.Complete = false
		}
	}
	for _, failure := range loaded.Failures {
		if failure.WorkspaceID == "" || allowed[failure.WorkspaceID] {
			result.Failures = append(result.Failures, failure)
		} else {
			result.Complete = false
		}
	}
	if !scope.Complete || !result.Complete && len(result.Failures) == 0 && !result.Truncated {
		result.Failures = append(result.Failures, backuprecovery.SummaryFailure{Code: "workspace_scope_unavailable"})
	}
	return result, nil
}

// scopedBackupIDs is the candidate identity list of the already-resolved
// scope; malformed entries are dropped and reported as incomplete.
func scopedBackupIDs(scope dailybrief.WorkspaceScopeResult) ([]string, map[string]bool, bool) {
	ids := []string{}
	allowed := map[string]bool{}
	complete := true
	for _, candidate := range scope.Workspaces {
		if candidate.Workspace == nil || candidate.ID == "" || strings.TrimSpace(candidate.ID) != candidate.ID || candidate.Workspace.ID != candidate.ID {
			complete = false
			continue
		}
		if !allowed[candidate.ID] {
			allowed[candidate.ID] = true
			ids = append(ids, candidate.ID)
		}
	}
	return ids, allowed, complete
}

type backupCoverageReader interface {
	ReadFolderCoverage(context.Context, string, []string, []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error)
}

// BriefCoverage passes the same scope identities, plus the scope's adopted
// folders, to the owning domain. Paths are compared there and never returned.
func (source dailyBriefBackupSource) BriefCoverage(ctx context.Context, owner string, scope dailybrief.WorkspaceScopeResult, folders []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	reader, ok := source.reader.(backupCoverageReader)
	if !ok || reader == nil || owner != "local" {
		return backuprecovery.CoverageRead{}, backuprecovery.ErrUnavailable
	}
	ids, _, complete := scopedBackupIDs(scope)
	read, err := reader.ReadFolderCoverage(ctx, owner, ids, folders)
	if err != nil {
		return backuprecovery.CoverageRead{}, err
	}
	read.Complete = read.Complete && complete
	return read, nil
}
