package personalassistant

import (
	"context"
	"sort"
	"time"

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
	if s.backups == nil {
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
	out.Backups.Health = todayHealthForItems(out.Backups.Items, now)
	if !read.Complete {
		out.Backups.Health = TodaySourceHealth{Status: TodaySectionPartial, Reason: "backup_source_unavailable", UpdatedAt: now}
		if len(read.Sources) == 0 {
			out.Backups.Health.Status = TodaySectionUnavailable
		}
		out.UnavailableSources = appendSource(out.UnavailableSources, "Backup & Recovery")
	}
	return all
}
