package dailybrief

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
)

// protectionTestSource answers each folder from a per-workspace table and
// reports which folders it was asked about.
type protectionTestSource struct {
	rows     map[string]backuprecovery.FolderCoverage
	consoles []backuprecovery.CoverageConsole
	asked    []string
}

func (s *protectionTestSource) BriefCoverage(_ context.Context, _ string, _ WorkspaceScopeResult, folders []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error) {
	read := backuprecovery.CoverageRead{Complete: true, Consoles: s.consoles}
	for _, folder := range folders {
		s.asked = append(s.asked, folder.WorkspaceID)
		row, ok := s.rows[folder.WorkspaceID]
		if !ok {
			row = backuprecovery.FolderCoverage{State: backuprecovery.CoverageNotCovered, Reason: "not_in_any_source"}
		}
		row.WorkspaceID = folder.WorkspaceID
		read.Folders = append(read.Folders, row)
	}
	return read, nil
}

func TestWorkspaceProtectionStatesTheThreeFactsFromScopeIdentity(t *testing.T) {
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	completed := now.Add(-14 * time.Hour)
	song := coverageScopedWorkspace("song", "Night Drive", "/Invented/Songs/Night Drive")
	backup := coverageScopedWorkspace("backup", "My Backups", "")
	backup.Slug = "my-backups"
	// The viewed workspace is outside the saved scope; the scope's console still covers it.
	scope := WorkspaceScopeResult{Complete: true, Workspaces: []ScopedWorkspace{backup}}
	source := &protectionTestSource{consoles: []backuprecovery.CoverageConsole{{WorkspaceID: "backup", Configured: true}},
		rows: map[string]backuprecovery.FolderCoverage{"song": {State: backuprecovery.CoverageCovered, Reason: "completed_backup_observed", Provider: "restic", CoveringWorkspaceID: "backup", LastCompletedAt: &completed}}}
	fact, ok := WorkspaceProtection(context.Background(), source, "local", scope, song.Workspace, "/chooser", now)
	if !ok || fact.State != backuprecovery.CoverageCovered || fact.Sentence != "Protected by restic in My Backups, last completed backup 14 h ago" ||
		fact.Route != "/workspaces/my-backups?panel=backup-recovery&tab=overview&section=coverage" || fact.ProviderLabel != "restic" {
		t.Fatal(fact, ok)
	}
	line := ProtectionPromptLine(fact)
	if line != "Backup protection of this workspace's folder (stored evidence as of 2026-10-19T09:00:00Z, not a new check): Protected by restic in My Backups, last completed backup 14 h ago. Protection is set up in the Backup & Recovery workspace." {
		t.Fatal(line)
	}

	source.rows = nil
	fact, _ = WorkspaceProtection(context.Background(), source, "local", scope, song.Workspace, "/chooser", now)
	if fact.State != backuprecovery.CoverageNotCovered || fact.Sentence != "Not in any backup Ori observes" || fact.RouteLabel != "Check protection" ||
		fact.Route != "/workspaces/my-backups?panel=backup-recovery&tab=overview&section=coverage" || fact.Covering != nil || fact.LastCompletedAt != nil {
		t.Fatal(fact)
	}
	source.consoles = nil
	if fact, _ = WorkspaceProtection(context.Background(), source, "local", scope, song.Workspace, "/chooser", now); fact.Route != "/chooser" {
		t.Fatal("no console in scope must lead to the chooser", fact)
	}

	source.rows = map[string]backuprecovery.FolderCoverage{"song": {State: backuprecovery.CoverageUnknown, Reason: "evidence_stale"}}
	source.consoles = []backuprecovery.CoverageConsole{{WorkspaceID: "backup", Configured: true}}
	fact, _ = WorkspaceProtection(context.Background(), source, "local", scope, song.Workspace, "/chooser", now)
	if fact.State != backuprecovery.CoverageUnknown || fact.Sentence != "Protection unknown: backup evidence is unavailable" || fact.RouteLabel != "Open Backup & Recovery" {
		t.Fatal(fact)
	}
	for _, payload := range []any{fact, ProtectionPromptLine(fact)} {
		encoded, _ := json.Marshal(payload)
		if strings.Contains(string(encoded), "/Invented") {
			t.Fatal("a path reached the fact", string(encoded))
		}
	}
}

func TestWorkspaceProtectionIsAbsentWithoutAnAdoptedFolder(t *testing.T) {
	hq := coverageScopedWorkspace("hq", "Personal HQ", "")
	source := &protectionTestSource{}
	if _, ok := WorkspaceProtection(context.Background(), source, "local", WorkspaceScopeResult{Complete: true}, hq.Workspace, "/chooser", time.Now()); ok || len(source.asked) != 0 {
		t.Fatal("a workspace without an adopted folder got a fact or a read")
	}
}

func TestWorkspaceProtectionKeepsTheViewedWorkspaceInsideTheCap(t *testing.T) {
	scope := WorkspaceScopeResult{Complete: true}
	for i := 0; i < MaxAdoptedFolders; i++ {
		id := fmt.Sprintf("a-%02d", i)
		scope.Workspaces = append(scope.Workspaces, coverageScopedWorkspace(id, id, "/Invented/"+id))
	}
	viewed := coverageScopedWorkspace("z-viewed", "Viewed", "/Invented/viewed")
	source := &protectionTestSource{}
	fact, ok := WorkspaceProtection(context.Background(), source, "local", scope, viewed.Workspace, "/chooser", time.Now())
	if !ok || fact.State != backuprecovery.CoverageNotCovered || len(source.asked) != MaxAdoptedFolders || source.asked[0] != "z-viewed" {
		t.Fatal("the viewed workspace fell outside the folder cap", fact, len(source.asked))
	}
}

func TestTimeMachineProblemsUseFixedTimeMachineWording(t *testing.T) {
	snapshot := func(provider string, problems ...string) BackupSnapshot {
		return BackupSnapshot{Facts: backuprecovery.SummaryFacts{Provider: provider, Connection: "configured", Health: backuprecovery.EvidenceHealth{Problems: problems}}}
	}
	for _, c := range []struct {
		facts      BackupSnapshot
		code, want string
	}{
		{snapshot("time_machine", "permission_required", "no_completed_snapshot"), "permission_required", "Time Machine needs Full Disk Access before Ori can read it"},
		{snapshot("time_machine", "no_completed_snapshot", "not_configured"), "not_configured", "Time Machine is not set up on this Mac"},
		{snapshot("time_machine", "no_completed_snapshot"), "no_completed_snapshot", "No completed Time Machine backup observed"},
		{snapshot("restic", "no_completed_snapshot"), "no_completed_snapshot", "No completed backup snapshot has been observed"},
		{snapshot("time_machine", "source_excluded"), "source_excluded", "A checked folder is excluded from Time Machine"},
	} {
		code, title, _ := BackupProblem(c.facts)
		if code != c.code || title != c.want || strings.Contains(strings.ToLower(title), "verified") {
			t.Fatal(c.code, code, title)
		}
	}
}

func TestRelativeAgeAndProviderLabelsAreFixed(t *testing.T) {
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	for age, want := range map[time.Duration]string{30 * time.Second: "just now", 5 * time.Minute: "5 min ago", 14 * time.Hour: "14 h ago", 50 * time.Hour: "2 d ago"} {
		if got := relativeAge(now.Add(-age), now); got != want {
			t.Fatal(age, got)
		}
	}
	if ProviderLabel("time_machine") != "Time Machine" || ProviderLabel("restic") != "restic" || ProviderLabel("UNTRUSTED /bin/sh") != "a backup provider" {
		t.Fatal("provider label echoed an unknown id")
	}
}
