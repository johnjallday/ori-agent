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
	ids := []string{}
	allowed := map[string]bool{}
	for _, candidate := range scope.Workspaces {
		if candidate.Workspace == nil || candidate.ID == "" || strings.TrimSpace(candidate.ID) != candidate.ID || candidate.Workspace.ID != candidate.ID {
			result.Complete = false
			continue
		}
		if !allowed[candidate.ID] {
			allowed[candidate.ID] = true
			ids = append(ids, candidate.ID)
		}
	}
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
