package dailybrief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type testBackupSource struct {
	read  backuprecovery.SummaryRead
	err   error
	scope WorkspaceScopeResult
	calls int
}

func (s *testBackupSource) BriefBackups(_ context.Context, _ string, scope WorkspaceScopeResult) (backuprecovery.SummaryRead, error) {
	s.calls++
	s.scope = scope
	return s.read, s.err
}
func backupScope(ids ...string) WorkspaceScopeResult {
	scope := WorkspaceScopeResult{Complete: true}
	for _, id := range ids {
		scope.Workspaces = append(scope.Workspaces, ScopedWorkspace{ID: id, Slug: id, Name: "PRIVATE NAME DO NOT REPEAT", Workspace: &workspace.Workspace{ID: id}})
	}
	return scope
}
func TestBackupReadScopedFailuresRoutesAndGaps(t *testing.T) {
	source := &testBackupSource{read: backuprecovery.SummaryRead{Complete: false, Sources: []backuprecovery.SummaryFacts{
		{WorkspaceID: "b", Generation: "gen-1", Connection: "configured", Route: "javascript:PRIVATE", Health: backuprecovery.EvidenceHealth{Observation: "stale", Problems: []string{"observation_stale"}}},
		{WorkspaceID: "foreign", Generation: "gen-1"},
	}, Failures: []backuprecovery.SummaryFailure{{WorkspaceID: "a", Code: "PRIVATE ERROR"}, {WorkspaceID: "foreign", Code: "PRIVATE ERROR"}}}}
	read := ReadBackups(context.Background(), source, "local", backupScope("b", "a"))
	if read.Complete || len(read.Sources) != 2 || read.Sources[0].Ref.WorkspaceID != "a" || len(read.Gaps) < 2 {
		t.Fatalf("read=%+v", read)
	}
	if read.Sources[1].Facts.Route != "/workspaces/b?panel=backup-recovery&tab=overview" {
		t.Fatal(read.Sources[1])
	}
	encoded, _ := json.Marshal(read)
	if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "foreign") {
		t.Fatal(string(encoded))
	}
	if BackupRoute(SourceRef{WorkspaceID: "../bad", EntityType: EntityBackupObservation, EntityID: "gen"}) != "" {
		t.Fatal("unsafe route accepted")
	}
}
func TestBackupReadBoundAndNoUnscopedEnumeration(t *testing.T) {
	ids := []string{}
	source := &testBackupSource{read: backuprecovery.SummaryRead{Complete: true}}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("ws-%02d", i)
		ids = append(ids, id)
		source.read.Sources = append(source.read.Sources, backuprecovery.SummaryFacts{WorkspaceID: id, Generation: "gen"})
	}
	read := ReadBackups(context.Background(), source, "local", backupScope(ids...))
	if len(read.Sources) != maxBackupSources || read.Complete {
		t.Fatalf("read=%+v", read)
	}
	empty := ReadBackups(context.Background(), source, "local", backupScope())
	if len(empty.Sources) != 0 || len(source.scope.Workspaces) != 0 {
		t.Fatal("empty scope widened")
	}
	calls := source.calls
	ReadBackups(context.Background(), source, "foreign", backupScope(ids...))
	if source.calls != calls {
		t.Fatal("foreign reader called")
	}
}
func TestBackupProblemPauseExpectedDisconnectAndUserReports(t *testing.T) {
	facts := backuprecovery.SummaryFacts{Connection: "configured", Monitoring: "paused", RecoveryState: "not_tested", Health: backuprecovery.EvidenceHealth{Problems: []string{"observation_stale", "metadata_check_failed"}}}
	if code, _, _ := BackupProblem(BackupSnapshot{Facts: facts}); code != "" {
		t.Fatal(code)
	}
	facts.Health.Problems = nil
	facts.Health.Disconnection = "within_grace"
	if code, _, _ := BackupProblem(BackupSnapshot{Facts: facts}); code != "" {
		t.Fatal(code)
	}
	facts.Health.Problems = []string{"backup_completion_overdue", "destination_identity_mismatch"}
	if code, _, _ := BackupProblem(BackupSnapshot{Facts: facts}); code != "destination_identity_mismatch" {
		t.Fatal(code)
	}
	facts.Health.Problems = nil
	facts.RecoveryState = "user_reported_failed"
	if code, title, _ := BackupProblem(BackupSnapshot{Facts: facts}); code != "user_reported_recovery_failed" || !strings.Contains(title, "report") {
		t.Fatal(code, title)
	}
	facts.RecoveryState = "user_reported_passed"
	if code, _, _ := BackupProblem(BackupSnapshot{Facts: facts}); code != "" {
		t.Fatal(code)
	}
}
func TestBackupSnapshotUsesCanonicalScopeAndKeepsOtherSources(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store := workspace.NewInMemoryStore()
	for _, id := range []string{"backup-a", "backup-b", "excluded"} {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Private"})
		ws.ID = id
		ws.FolderSlug = id
		ws.OwnerUserID = "local"
		ws.CreatedAt = now.Add(-time.Hour)
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	source := &testBackupSource{read: backuprecovery.SummaryRead{Complete: false, Sources: []backuprecovery.SummaryFacts{{WorkspaceID: "backup-a", Generation: "gen", Connection: "not_configured", RecoveryState: "not_tested"}}, Failures: []backuprecovery.SummaryFailure{{WorkspaceID: "backup-b", Code: "stored_state_unavailable"}}}}
	snap := BuildSnapshot(context.Background(), SnapshotSources{Workspaces: store, Backups: source}, Config{Scope: ScopeSelected, SelectedWorkspaceIDs: []string{"backup-b", "backup-a"}}, "local", now)
	if len(source.scope.Workspaces) != 2 || len(snap.Backups) != 2 || !snap.BackupIncomplete || len(snap.AllRefs()) != 2 {
		t.Fatalf("scope=%+v snapshot=%+v", source.scope, snap)
	}
	content := BuildDeterministicContent(snap, now.Add(-time.Hour), false)
	if len(content.BackupEvidence) != 0 || len(content.NeedsAttention) != 2 || len(content.DataGaps) == 0 {
		t.Fatalf("content=%+v", content)
	}
}
func TestBackupSynthesisCannotPublishModelProtectionClaims(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	count := 1
	source := BackupSnapshot{Ref: SourceRef{WorkspaceID: "backup", WorkspaceSlug: "backup-console", EntityType: EntityBackupObservation, EntityID: "gen"}, Facts: backuprecovery.SummaryFacts{Connection: "configured", SnapshotCount: &count, RecoveryState: "user_reported_passed", RecoveryTestedAt: &now}}
	for _, missing := range []bool{false, true} {
		snap := sampleSnapshot(now)
		snap.BackupIncomplete = missing
		if !missing {
			snap.Backups = []BackupSnapshot{source}
		}
		chat := &fakeChatCompleter{response: &llm.ChatResponse{Content: `{"opening_summary":"YOUR BACKUP AND RESTORE ARE VERIFIED"}`}}
		synthesizer := Synthesizer{Chat: chat, Resolver: resolverFor(snap, nil, nil)}
		result, err := synthesizer.Generate(context.Background(), GenerationRequest{}, Config{})
		if err != nil {
			t.Fatal(err)
		}
		if chat.calls != 0 || strings.Contains(result.ContentJSON, "ARE VERIFIED") {
			t.Fatal("model invented backup protection", result)
		}
		if !missing && (!strings.Contains(result.ContentJSON, "user reports sample recovery passed") || !strings.Contains(result.ContentJSON, "not proof")) {
			t.Fatal(result.ContentJSON)
		}
	}
	forged := BriefContent{BackupEvidence: []BriefBackupEvidence{{Ref: source.Ref, Summary: "verified"}}, NeedsAttention: []BriefAttentionItem{{Ref: source.Ref, Title: "verified"}}}
	got, dropped := ValidateAgainstAllowlist(forged, map[string]SourceRef{source.Ref.Key(): source.Ref})
	if dropped != 2 || len(got.BackupEvidence) > 0 || len(got.NeedsAttention) > 0 {
		t.Fatal(got, dropped)
	}
	sourceRead := &testBackupSource{err: errors.New("PRIVATE /secret")}
	read := ReadBackups(context.Background(), sourceRead, "local", backupScope("backup"))
	if read.Complete || strings.Contains(strings.Join(read.Gaps, " "), "PRIVATE") {
		t.Fatal(read)
	}
}
