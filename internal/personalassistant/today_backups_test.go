package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
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

// todayTestCoverage answers every adopted folder with one state, plus fixed
// per-workspace overrides; it records nothing a real reader would not.
type todayTestCoverage struct {
	state     string
	overrides map[string]backuprecovery.FolderCoverage
	consoles  []backuprecovery.CoverageConsole
}

func (c *todayTestCoverage) BriefCoverage(_ context.Context, _ string, _ dailybrief.WorkspaceScopeResult, folders []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error) {
	read := backuprecovery.CoverageRead{Complete: true, Consoles: c.consoles}
	for _, folder := range folders {
		row, ok := c.overrides[folder.WorkspaceID]
		if !ok {
			row = backuprecovery.FolderCoverage{WorkspaceID: folder.WorkspaceID, State: c.state, Reason: "no_configuration"}
		}
		read.Folders = append(read.Folders, row)
	}
	return read, nil
}

type failingCoverageDeclines struct{}

func (failingCoverageDeclines) DomainDecline(context.Context, string, string) (*FolderDomainDecline, error) {
	return nil, errors.New("PRIVATE unreadable digest")
}
func (failingCoverageDeclines) DeclineDomain(context.Context, string, string, string) error {
	return errors.New("PRIVATE unreadable digest")
}

func saveAdoptedTodayWorkspace(t *testing.T, store workspace.Store, id, name string) {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	ws.ID = id
	ws.OwnerUserID = "local"
	ws.FolderSlug = id
	ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "ref-" + id, WorkspaceID: id, Name: name, Path: "/Invented/Projects/" + id}}
	ws.SharedData = map[string]any{"primary_directory_id": "ref-" + id}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
}

func todayCoverageItems(t *testing.T, service *TodayService) (backups, needs []TodayItem) {
	t.Helper()
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range got.Backups.Items {
		if item.Kind == "backup_coverage" {
			backups = append(backups, item)
		}
	}
	for _, item := range got.NeedsYou.Items {
		if item.Kind == "backup_coverage" {
			needs = append(needs, item)
		}
	}
	return backups, needs
}

func TestTodayProtectionLineIsOneStickyDeclineThatRevivesOnce(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	relationship := baseTodayProjection()
	relationship.DailyBrief = &BriefConfigProjection{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}
	digest := newFolderDigestFixture(t)
	service := NewTodayService(stubTodayRelationship{projection: relationship}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetBackupCoverage(&todayTestCoverage{state: backuprecovery.CoverageNotCovered}, digest.service)

	if backups, _ := todayCoverageItems(t, service); len(backups) != 0 {
		t.Fatal("a line without any adopted folder", backups)
	}
	saveAdoptedTodayWorkspace(t, store, "song-1", "Song 1")
	backups, needs := todayCoverageItems(t, service)
	if len(backups) != 1 || len(needs) != 1 {
		t.Fatal("want exactly one protection line", backups, needs)
	}
	line := backups[0]
	if line.Title != "1 project folder has no backup Ori can see" || line.Detail != "Song 1" || line.Attribution != "Backup & Recovery" ||
		line.Route != backupChooserRoute || line.State != "not_covered" || len(line.Actions) != 1 || line.Actions[0] != "not_now" {
		t.Fatal(line)
	}
	encoded, _ := json.Marshal(line)
	if strings.Contains(string(encoded), "/Invented") {
		t.Fatal("a path reached Today", string(encoded))
	}

	if err := service.DeclineBackupCoverage(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // sticky across reloads
		if backups, _ := todayCoverageItems(t, service); len(backups) != 0 {
			t.Fatal("declined line returned", backups)
		}
	}
	for i := 2; i <= 4; i++ {
		saveAdoptedTodayWorkspace(t, store, fmt.Sprintf("song-%d", i), fmt.Sprintf("Song %d", i))
	}
	if backups, _ := todayCoverageItems(t, service); len(backups) != 0 {
		t.Fatal("revived below portfolio scale", backups)
	}
	saveAdoptedTodayWorkspace(t, store, "song-5", "Song 5")
	backups, _ = todayCoverageItems(t, service)
	if len(backups) != 1 || backups[0].Title != "5 project folders have no backup Ori can see" || backups[0].Detail != "Song 1, Song 2, Song 3 +2 more" {
		t.Fatal("did not revive once at five", backups)
	}
	if err := service.DeclineBackupCoverage(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	saveAdoptedTodayWorkspace(t, store, "song-6", "Song 6")
	if backups, _ := todayCoverageItems(t, service); len(backups) != 0 {
		t.Fatal("a second decline was not final", backups)
	}
	decline, err := digest.service.DomainDecline(context.Background(), "local", BackupCoverageDomain)
	if err != nil || decline == nil || decline.Count != 2 || decline.FirstEvidence != "single_project" {
		t.Fatal(decline, err)
	}
}

func TestTodayProtectionLineFirstDeclineAtPortfolioScaleIsFinal(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	relationship := baseTodayProjection()
	relationship.DailyBrief = &BriefConfigProjection{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}
	digest := newFolderDigestFixture(t)
	service := NewTodayService(stubTodayRelationship{projection: relationship}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetBackupCoverage(&todayTestCoverage{state: backuprecovery.CoverageNotCovered}, digest.service)
	for i := 1; i <= 5; i++ {
		saveAdoptedTodayWorkspace(t, store, fmt.Sprintf("song-%d", i), fmt.Sprintf("Song %d", i))
	}
	if err := service.DeclineBackupCoverage(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	saveAdoptedTodayWorkspace(t, store, "song-6", "Song 6")
	if backups, _ := todayCoverageItems(t, service); len(backups) != 0 {
		t.Fatal("a portfolio-scale no revived", backups)
	}
}

func TestTodayProtectionLineCountsCoverageAndOpensTheConsole(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	relationship := baseTodayProjection()
	relationship.DailyBrief = &BriefConfigProjection{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}
	console := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "My Backups"})
	console.ID, console.OwnerUserID, console.FolderSlug = "backup", "local", "my-backups"
	if err := store.Save(console); err != nil {
		t.Fatal(err)
	}
	saveAdoptedTodayWorkspace(t, store, "song-a", "Song A")
	saveAdoptedTodayWorkspace(t, store, "song-b", "Song B")
	at := now.Add(-14 * time.Hour)
	coverage := &todayTestCoverage{state: backuprecovery.CoverageNotCovered, consoles: []backuprecovery.CoverageConsole{{WorkspaceID: "backup", Configured: true}},
		overrides: map[string]backuprecovery.FolderCoverage{"song-a": {WorkspaceID: "song-a", State: backuprecovery.CoverageCovered, Provider: "restic", CoveringWorkspaceID: "backup", LastCompletedAt: &at}}}
	problems := &todayTestBackupReader{read: backuprecovery.SummaryRead{Complete: true}}
	for i := 0; i < 6; i++ {
		problems.read.Sources = append(problems.read.Sources, backuprecovery.SummaryFacts{WorkspaceID: "backup", Generation: fmt.Sprintf("gen-%d", i), Connection: "configured", Health: backuprecovery.EvidenceHealth{Problems: []string{"destination_identity_mismatch"}}})
	}
	service := NewTodayService(stubTodayRelationship{projection: relationship}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetBackupReader(problems)
	service.SetBackupCoverage(coverage, newFolderDigestFixture(t).service)
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	items := got.Backups.Items
	if len(items) != 2 || items[0].Kind != "backup_observation" || items[1].Kind != "backup_coverage" {
		t.Fatal("the line counted against or replaced a backup problem", items)
	}
	line := items[1]
	if line.Title != "1 of your 2 project folders is not covered by a backup Ori observes" || line.Detail != "Song B" ||
		line.Route != "/workspaces/my-backups?panel=backup-recovery&tab=overview&section=coverage" {
		t.Fatal(line)
	}

	// Unknown alone is never the line: stale evidence is the existing problem's job.
	coverage.state = backuprecovery.CoverageUnknown
	coverage.overrides = nil
	if backups, _ := todayCoverageItems(t, service); len(backups) != 0 {
		t.Fatal("unknown produced the line", backups)
	}
}

func TestTodayProtectionLineHidesWhenTheDeclineIsUnreadable(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	relationship := baseTodayProjection()
	relationship.DailyBrief = &BriefConfigProjection{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}
	saveAdoptedTodayWorkspace(t, store, "song-1", "Song 1")
	service := NewTodayService(stubTodayRelationship{projection: relationship}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetBackupCoverage(&todayTestCoverage{state: backuprecovery.CoverageNotCovered}, failingCoverageDeclines{})
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "backup_coverage") || strings.Contains(string(data), "PRIVATE") {
		t.Fatal("an unreadable earlier no was overridden or leaked", string(data))
	}
	if err := service.DeclineBackupCoverage(context.Background(), "local"); err == nil {
		t.Fatal("a failed decline reported success")
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
