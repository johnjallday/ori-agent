package personalassistant

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

// SetBackupReader is composition-only. Today receives a stored-facts read,
// never a connection, metadata-check, report-write, provider or file tool.
func (s *TodayService) SetBackupReader(reader dailybrief.BackupSource) {
	if s != nil {
		s.backups = reader
	}
}
func (s *TodayService) loadBackups(ctx context.Context, owner string, relationship *Projection, now time.Time, out *TodayProjection) map[string]TodayItem {
	all := map[string]TodayItem{}
	if s.backups == nil && s.backupCoverage == nil {
		return all
	}
	scope := dailybrief.ResolveWorkspaceScope(s.followUpWorkspaces, todayFollowUpConfig(relationship), owner)
	read := dailybrief.ReadBackups(ctx, s.backups, owner, scope)
	type ranked struct {
		item TodayItem
		rank int
	}
	items := []ranked{}
	for _, source := range read.Sources {
		code, title, rank := dailybrief.BackupProblem(source)
		at := source.Facts.AsOf
		if source.Facts.LastSuccessfulRead != nil {
			at = *source.Facts.LastSuccessfulRead
		}
		item := TodayItem{ID: source.Ref.Key(), Kind: "backup_observation", Title: "Stored Backup & Recovery evidence", Detail: dailybrief.BackupEvidenceLine(source), Attribution: "Backup & Recovery", Route: dailybrief.BackupRoute(source.Ref), Ref: source.Ref, SourceAt: at}
		if item.Route == "" {
			continue
		}
		if code != "" {
			item.Title = title
			item.State = code
			items = append(items, ranked{item, rank})
		}
		all[source.Ref.Key()] = item
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank > items[j].rank
		}
		return items[i].item.Ref.Key() < items[j].item.Ref.Key()
	})
	out.Backups = TodaySection{Items: []TodayItem{}}
	for _, row := range items {
		if len(out.Backups.Items) >= 5 {
			break
		}
		out.Backups.Items = append(out.Backups.Items, row.item)
	}
	// The protection line is one more item, outside the five-problem cap.
	coverage, coverageComplete, due := s.backupCoverageItem(ctx, owner, relationship, scope, now)
	if due {
		out.Backups.Items = append(out.Backups.Items, coverage)
	}
	out.Backups.Health = todayHealthForItems(out.Backups.Items, now)
	if !read.Complete || !coverageComplete {
		out.Backups.Health = TodaySourceHealth{Status: TodaySectionPartial, Reason: "backup_source_unavailable", UpdatedAt: now}
		if len(read.Sources) == 0 && len(out.Backups.Items) == 0 {
			out.Backups.Health.Status = TodaySectionUnavailable
		}
		out.UnavailableSources = appendSource(out.UnavailableSources, "Backup & Recovery")
	}
	return all
}

// BackupCoverageDeclines is the folder digest's per-user domain decline, the
// only state the protection line keeps. It holds no path.
type BackupCoverageDeclines interface {
	DomainDecline(ctx context.Context, userID, domain string) (*FolderDomainDecline, error)
	DeclineDomain(ctx context.Context, userID, domain, evidence string) error
}

// SetBackupCoverage is composition-only: a stored coverage read and the
// decline record. Neither can read a provider, a file or a path.
func (s *TodayService) SetBackupCoverage(source dailybrief.BackupCoverageSource, declines BackupCoverageDeclines) {
	if s != nil {
		s.backupCoverage = source
		s.coverageDeclines = declines
	}
}

const (
	// backupCoverageReviveAt is portfolio scale: a first decline made below it
	// revives once when the uncovered count first reaches it.
	backupCoverageReviveAt = 5
	// backupChooserRoute is where the line leads when no Backup & Recovery
	// workspace is in scope.
	backupChooserRoute = "/?create=1&blueprint=backup-and-recovery"
)

// backupCoverageItem builds the single protection line (FR6–FR10, FR13–FR14).
// ok is false when no line is due; complete reports the coverage read itself.
func (s *TodayService) backupCoverageItem(ctx context.Context, owner string, relationship *Projection, scope dailybrief.WorkspaceScopeResult, now time.Time) (item TodayItem, complete, ok bool) {
	if s.backupCoverage == nil || s.coverageDeclines == nil {
		return TodayItem{}, true, false
	}
	coverage := dailybrief.ReadCoverage(ctx, s.backupCoverage, owner, scope)
	if coverage.NotCovered == 0 {
		return TodayItem{}, coverage.Complete, false
	}
	decline, err := s.coverageDeclines.DomainDecline(ctx, owner, BackupCoverageDomain)
	if err != nil || backupCoverageSuppressed(decline, coverage.NotCovered) {
		// An unreadable earlier no is never overridden by showing the line.
		return TodayItem{}, coverage.Complete, false
	}
	route := backupChooserRoute
	if coverage.Console != nil {
		route = dailybrief.CoverageConsoleRoute(coverage.Console)
		if route == "" {
			return TodayItem{}, false, false
		}
	}
	hqID := ""
	if relationship != nil {
		hqID = relationship.HQWorkspaceID
	}
	return TodayItem{
		ID: "backup-coverage", Kind: "backup_coverage", Title: backupCoverageTitle(coverage), Detail: backupCoverageDetail(coverage),
		State: "not_covered", Attribution: "Backup & Recovery", Route: route, SourceAt: now, Actions: []string{"not_now"},
		Ref: dailybrief.SourceRef{WorkspaceID: hqID, EntityType: "backup_coverage", EntityID: "adopted_folders", Timestamp: now},
	}, coverage.Complete, true
}

// backupCoverageSuppressed applies the sticky decline: a first no made below
// portfolio scale lifts once the uncovered count reaches it; a no made at
// portfolio scale, or a second no, is final.
func backupCoverageSuppressed(decline *FolderDomainDecline, notCovered int) bool {
	if decline == nil {
		return false
	}
	return decline.Count != 1 || decline.FirstEvidence != "single_project" || notCovered < backupCoverageReviveAt
}

func backupCoverageTitle(coverage dailybrief.CoverageResult) string {
	n, m := coverage.NotCovered, coverage.Adopted()
	if !coverage.Configured {
		if n == 1 {
			return "1 project folder has no backup Ori can see"
		}
		return fmt.Sprintf("%d project folders have no backup Ori can see", n)
	}
	switch {
	case m == 1:
		return "Your project folder is not covered by a backup Ori observes"
	case n == 1:
		return fmt.Sprintf("1 of your %d project folders is not covered by a backup Ori observes", m)
	default:
		return fmt.Sprintf("%d of your %d project folders are not covered by a backup Ori observes", n, m)
	}
}

func backupCoverageDetail(coverage dailybrief.CoverageResult) string {
	names := []string{}
	for _, row := range coverage.Workspaces {
		if row.State == backuprecovery.CoverageNotCovered {
			names = append(names, row.Workspace.Name)
		}
	}
	detail := strings.Join(names[:min(3, len(names))], ", ")
	if len(names) > 3 {
		detail += fmt.Sprintf(" +%d more", len(names)-3)
	}
	return detail
}

// DeclineBackupCoverage records "Not now" on the protection line. The evidence
// is the current uncovered count, read here, never supplied by the client.
func (s *TodayService) DeclineBackupCoverage(ctx context.Context, userID string) error {
	if s == nil || s.relationship == nil || s.backupCoverage == nil || s.coverageDeclines == nil {
		return ErrRepairNeeded
	}
	relationship, err := s.relationship.Get(ctx, strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	scope := dailybrief.ResolveWorkspaceScope(s.followUpWorkspaces, todayFollowUpConfig(relationship), userID)
	evidence := "single_project"
	if dailybrief.ReadCoverage(ctx, s.backupCoverage, userID, scope).NotCovered >= backupCoverageReviveAt {
		evidence = "portfolio"
	}
	return s.coverageDeclines.DeclineDomain(ctx, userID, BackupCoverageDomain, evidence)
}
