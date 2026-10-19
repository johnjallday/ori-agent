package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type backupSummaryScopeSpy struct {
	ids    []string
	owner  string
	result backuprecovery.SummaryRead
	err    error
}

func (s *backupSummaryScopeSpy) ReadBackupSummaries(_ context.Context, owner string, ids []string) (backuprecovery.SummaryRead, error) {
	s.ids = append([]string{}, ids...)
	s.owner = owner
	return s.result, s.err
}
func backupScopedWorkspace(id string) dailybrief.ScopedWorkspace {
	return dailybrief.ScopedWorkspace{ID: id, Name: "private /Volumes/not-a-summary-label", Workspace: &workspace.Workspace{ID: id}}
}
func TestBackupSummaryUsesExistingSelectedAllScopeWithoutWidening(t *testing.T) {
	for _, mode := range []dailybrief.Scope{dailybrief.ScopeSelected, dailybrief.ScopeAll} {
		t.Run(string(mode), func(t *testing.T) {
			rows := []dailybrief.ScopedWorkspace{backupScopedWorkspace("backup-a")}
			if mode == dailybrief.ScopeAll {
				rows = append(rows, backupScopedWorkspace("backup-b"))
			}
			spy := &backupSummaryScopeSpy{result: backuprecovery.SummaryRead{Complete: true, Sources: []backuprecovery.SummaryFacts{{WorkspaceID: "backup-a"}}}}
			result, err := readScopedBackupSummaries(t.Context(), "local", dailybrief.WorkspaceScopeResult{Scope: mode, Complete: true, Workspaces: rows}, spy)
			if err != nil || !result.Complete || spy.owner != "local" || len(spy.ids) != len(rows) || len(result.Sources) != 1 {
				t.Fatal("scope was replaced or widened", result, err, spy.ids)
			}
		})
	}
	spy := &backupSummaryScopeSpy{result: backuprecovery.SummaryRead{Complete: true, Sources: []backuprecovery.SummaryFacts{{WorkspaceID: "outside-scope"}}}}
	result, err := readScopedBackupSummaries(t.Context(), "local", dailybrief.WorkspaceScopeResult{Complete: true, Workspaces: []dailybrief.ScopedWorkspace{backupScopedWorkspace("backup-a")}}, spy)
	if err != nil || result.Complete || len(result.Sources) != 0 {
		t.Fatal("over-returning reader widened scope", result, err)
	}
}
func TestBackupSummaryPreservesIndependentFailureWithoutPrivateLabelsOrErrors(t *testing.T) {
	scope := dailybrief.WorkspaceScopeResult{Complete: false, Gaps: []string{"private /Volumes/not-for-prompts"}, Workspaces: []dailybrief.ScopedWorkspace{backupScopedWorkspace("backup-a")}}
	spy := &backupSummaryScopeSpy{result: backuprecovery.SummaryRead{Complete: true, Sources: []backuprecovery.SummaryFacts{{WorkspaceID: "backup-a"}}}}
	result, err := readScopedBackupSummaries(t.Context(), "local", scope, spy)
	if err != nil || result.Complete || len(result.Sources) != 1 || len(result.Failures) != 1 {
		t.Fatal(result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "/Volumes/") {
		t.Fatal("scope labels entered backup facts")
	}
	spy.err = errors.New("private database /Volumes/not-for-prompts")
	result, err = readScopedBackupSummaries(t.Context(), "local", scope, spy)
	if err != nil || result.Complete || len(result.Failures) != 1 || result.Failures[0].Code != "backup_reader_unavailable" {
		t.Fatal("reader failure became empty/healthy", result, err)
	}
	spy.owner = ""
	if _, err := readScopedBackupSummaries(t.Context(), "LOCAL", scope, spy); err != backuprecovery.ErrWorkspaceUnavailable || spy.owner != "" {
		t.Fatal("folded actor invoked reader", err)
	}
}
