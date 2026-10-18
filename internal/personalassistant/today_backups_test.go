package personalassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type todayTestBackupReader struct {
	read  backuprecovery.SummaryRead
	scope dailybrief.WorkspaceScopeResult
	calls int
}

func (s *todayTestBackupReader) BriefBackups(_ context.Context, _ string, scope dailybrief.WorkspaceScopeResult) (backuprecovery.SummaryRead, error) {
	s.scope = scope
	s.calls++
	return s.read, nil
}
func TestTodayBackupsScopedNeedsYouUpdatesWithoutWorkCreation(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, hq := newTodayWorkspace(t, now)
	relationship := baseTodayProjection()
	relationship.DailyBrief = &BriefConfigProjection{Scope: dailybrief.ScopeSelected, SelectedWorkspaceIDs: []string{"backup"}}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "PRIVATE BACKUP NAME"})
	ws.ID = "backup"
	ws.OwnerUserID = "local"
	ws.FolderSlug = "backup-console"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	reader := &todayTestBackupReader{read: backuprecovery.SummaryRead{Complete: true, Sources: []backuprecovery.SummaryFacts{{WorkspaceID: "backup", Generation: "gen", Connection: "configured", Monitoring: "manual_only", AsOf: now, Health: backuprecovery.EvidenceHealth{Observation: "stale", Problems: []string{"observation_stale", "backup_completion_overdue"}}}}}}
	service := NewTodayService(stubTodayRelationship{projection: relationship}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetBackupReader(reader)
	initial, _ := store.Get(hq.ID)
	taskCount := len(initial.Tasks)
	var key string
	for i := 0; i < 2; i++ {
		got, err := service.Get(context.Background(), "local")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Backups.Items) != 1 || got.Backups.Health.Status != TodaySectionAvailable {
			t.Fatal(got.Backups)
		}
		row := got.Backups.Items[0]
		if key != "" && key != row.Ref.Key() {
			t.Fatal("unstable identity")
		}
		key = row.Ref.Key()
		if row.Route != "/workspaces/backup-console?panel=backup-recovery&tab=overview" || strings.Contains(row.Detail, "PRIVATE") {
			t.Fatal(row)
		}
		found := 0
		for _, item := range got.NeedsYou.Items {
			if item.Ref.Key() == key {
				found++
			}
		}
		if found != 1 {
			t.Fatal(got.NeedsYou)
		}
	}
	after, _ := store.Get(hq.ID)
	if len(after.Tasks) != taskCount || len(ws.Tasks) != 0 {
		t.Fatal("projection created work")
	}
	reader.read.Sources[0].Health = backuprecovery.EvidenceHealth{Observation: "current", Recency: "recent"}
	got, err := service.Get(context.Background(), "local")
	if err != nil || len(got.Backups.Items) != 0 || got.Backups.Health.Status != TodaySectionHealthyEmpty {
		t.Fatal(got, err)
	}
	relationship.DailyBrief.SelectedWorkspaceIDs = []string{}
	got, err = service.Get(context.Background(), "local")
	if err != nil || len(got.Backups.Items) != 0 || len(reader.scope.Workspaces) != 0 {
		t.Fatal("scope widened", got, err)
	}
}
func TestTodayBackupBriefGroundsLatestFactsAndDropsObsoleteScope(t *testing.T) {
	ref := dailybrief.SourceRef{WorkspaceID: "backup", EntityType: dailybrief.EntityBackupObservation, EntityID: "gen"}
	content := dailybrief.BriefContent{BackupHostRendered: true, OpeningSummary: "UNTRUSTED VERIFIED BACKUP", NeedsAttention: []dailybrief.BriefAttentionItem{{Ref: ref, Title: "UNTRUSTED VERIFIED RESTORE"}}, BackupEvidence: []dailybrief.BriefBackupEvidence{{Ref: ref, Summary: "UNTRUSTED COMMAND"}}}
	encoded, _ := json.Marshal(content)
	service := &TodayService{briefs: stubTodayBrief{revision: &dailybrief.Revision{UserID: "local", WorkspaceID: "hq", ContentJSON: string(encoded)}}}
	item := TodayItem{Ref: ref, Title: "Review stale stored backup evidence", Detail: "Stored metadata, not proof of protection.", Route: "/workspaces/backup?panel=backup-recovery&tab=overview"}
	out := &TodayProjection{}
	service.loadBrief(context.Background(), "local", "hq", "/workspaces/hq", nil, nil, nil, map[string]TodayItem{ref.Key(): item}, out)
	data, _ := json.Marshal(out.Brief)
	if strings.Contains(string(data), "UNTRUSTED") || len(out.Brief.Items) != 1 {
		t.Fatal(string(data))
	}
	service.loadBrief(context.Background(), "local", "hq", "/workspaces/hq", nil, nil, nil, map[string]TodayItem{}, out)
	if len(out.Brief.Items) != 0 {
		t.Fatal("obsolete scope/generation survived", out.Brief)
	}
}

func TestTodayBackupFailuresBoundedOrderedAndIndependent(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	relationship := baseTodayProjection()
	relationship.DailyBrief = &BriefConfigProjection{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}
	reader := &todayTestBackupReader{read: backuprecovery.SummaryRead{Complete: false, Failures: []backuprecovery.SummaryFailure{{WorkspaceID: "backup-0", Code: "PRIVATE"}}}}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("backup-%d", i)
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "PRIVATE"})
		ws.ID = id
		ws.OwnerUserID = "local"
		ws.FolderSlug = id
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			reader.read.Sources = append(reader.read.Sources, backuprecovery.SummaryFacts{WorkspaceID: id, Generation: "gen", Connection: "configured", Health: backuprecovery.EvidenceHealth{Problems: []string{"destination_identity_mismatch"}}})
		}
	}
	service := NewTodayService(stubTodayRelationship{projection: relationship}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.SetBackupReader(reader)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Backups.Items) != 5 || got.Backups.Items[0].Ref.WorkspaceID != "backup-1" || got.Backups.Health.Status != TodaySectionPartial || got.Results.Health.Status != TodaySectionAvailable || len(got.Results.Items) == 0 {
		t.Fatal(got)
	}
	if !strings.Contains(strings.Join(got.UnavailableSources, ","), "Backup & Recovery") {
		t.Fatal(got.UnavailableSources)
	}
	data, _ := json.Marshal(got.Backups)
	if strings.Contains(string(data), "PRIVATE") {
		t.Fatal(string(data))
	}
	reader.read = backuprecovery.SummaryRead{Complete: false}
	got, err = service.Get(context.Background(), "local")
	if err != nil || got.Backups.Health.Status != TodaySectionUnavailable || got.Results.Health.Status != TodaySectionAvailable {
		t.Fatal(got, err)
	}
}
