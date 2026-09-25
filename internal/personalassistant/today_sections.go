package personalassistant

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// buildTodaySections composes the three user-facing regions from the same
// bounded, canonical reads used by the legacy fields. Offer cards keep their
// independent controllers; their items here are placement signals, not a
// second copy of their buttons or a permission to perform their operations.
func (s *TodayService) buildTodaySections(ctx context.Context, userID string, relationship *Projection, hq *workspace.Workspace, hqRoute string, recent []FolderOffer, janitorRows []JanitorResult, now time.Time, out *TodayProjection) {
	working := []TodayItem{{ID: hq.ID, Kind: "hq_status", Title: hq.Name,
		Detail: hqBriefSchedule(relationship), Route: hqRoute}}
	needs := make([]TodayItem, 0)
	if s.folderDigest != nil {
		view, err := s.folderDigest.Current(ctx, userID)
		if err != nil {
			out.UnavailableSources = appendSource(out.UnavailableSources, "folder offers")
		} else if view.Offer != nil && (view.Offer.Status == FolderOfferPending || view.Offer.Status == FolderOfferAwaitingOutcome) {
			needs = append(needs, TodayItem{ID: view.Offer.ID, Kind: "folder_offer", Title: "Look at " + view.Offer.Folder})
		}
	}
	// The independent specialist-offer controller mounts its card in Needs you
	// only if detection found an actual domain. No speculative row is invented.
	if out.SpecialistSetup != nil {
		switch out.SpecialistSetup.Lifecycle {
		case "needs_attention":
			needs = append(needs, TodayItem{ID: out.SpecialistSetup.JourneyID, Kind: "setup_attention",
				Title: "Specialist setup needs attention", Route: firstTodaySetupRoute(out.SpecialistSetup)})
		case "in_progress":
			working = append(working, TodayItem{ID: out.SpecialistSetup.JourneyID, Kind: "setup_in_progress",
				Title: "Specialist setup in progress", Route: firstTodaySetupRoute(out.SpecialistSetup)})
		}
	}
	// Read all retained resolved offers for Working on; only the last seven
	// days enter Done. An unavailable source is reported once in the footer.
	all := recent
	if s.folderReceipts != nil {
		if history, err := s.folderReceipts.RecentReceipts(ctx, userID, time.Time{}); err == nil {
			all = history
		} else {
			out.UnavailableSources = appendSource(out.UnavailableSources, "folder setups")
		}
	}
	seen := map[string]bool{hq.ID: true}
	for _, offer := range all {
		if offer.Outcome == nil || offer.Outcome.Kind != FolderChoiceProject || offer.Outcome.WorkspaceID == "" || seen[offer.Outcome.WorkspaceID] {
			continue
		}
		ws, err := s.workspaces.Get(offer.Outcome.WorkspaceID)
		if err != nil || ws == nil || ws.ID != offer.Outcome.WorkspaceID || !todaySafeSlug.MatchString(ws.FolderSlug) {
			continue
		}
		seen[ws.ID] = true
		detail := "Folder workspace"
		for _, row := range offer.Outcome.Receipt {
			if row.Kind == "blueprint" && strings.TrimSpace(row.Name) != "" {
				detail = row.Name
				break
			}
		}
		var last time.Time
		for _, task := range ws.Tasks {
			if task.CanonicalState() == workspace.TicketStateReview && task.CompletedAt != nil && task.CompletedAt.After(last) {
				last = *task.CompletedAt
			}
		}
		if !last.IsZero() {
			detail += " · Last result " + last.UTC().Format("Jan 2")
		}
		working = append(working, TodayItem{ID: ws.ID, Kind: "folder_workspace", Title: ws.Name,
			Detail: detail, Route: "/workspaces/" + url.PathEscape(ws.FolderSlug)})
	}
	for _, row := range janitorRows {
		if item, ok := janitorTodayItem(row, now); ok {
			working = append(working, TodayItem{ID: item.ID, Kind: "janitor_work", Title: item.Attribution,
				Detail: item.Title, Route: item.Route})
		}
	}
	if out.Studio != nil {
		for _, project := range out.Studio.Projects {
			if strings.HasPrefix(project.Route, "/workspaces/") && !strings.HasPrefix(project.Route, "//") {
				working = append(working, TodayItem{ID: project.WorkspaceID, Kind: "studio_project",
					Title: project.Name, Route: project.Route})
			}
		}
	}
	// Ready tasks and follow-ups have their own canonical links. Exclude the
	// decision subset from the follow-up pass so each actionable item appears
	// only once, with one primary action (its link).
	for _, task := range out.Priorities.Items {
		if task.State == "waiting_for_input" || task.State == "waiting_for_choice" {
			needs = append(needs, task)
		}
	}
	needs = append(needs, s.followUpItemsByStatus(ctx, userID, relationship, now, followup.StatusCandidate, out)...)
	needs = append(needs, out.Decisions.Items...)
	decisionIDs := map[string]bool{}
	for _, item := range out.Decisions.Items {
		decisionIDs[item.ID] = true
	}
	for _, item := range out.FollowUps.Items {
		if !decisionIDs[item.ID] {
			needs = append(needs, item)
		}
	}
	// Brief attention states are not raw copy: the client maps item.State and
	// the reason tag before showing them as labels.
	for _, item := range out.Brief.Items {
		if item.Route != "" && (item.State == "waiting_for_choice" || item.State == "waiting_for_input" ||
			strings.Contains(item.Detail, "waiting_for_choice")) {
			needs = append(needs, item)
		}
	}
	done := append([]TodayItem(nil), out.Results.Items...)
	if hq.CreatedAt.After(now.Add(-todayFolderReceiptWindow)) && !hq.CreatedAt.After(now) {
		done = append([]TodayItem{{ID: "hq-receipt-" + hq.ID, Kind: "hq_setup",
			Title: "Set up " + hq.Name, Detail: hqBriefSchedule(relationship), Route: hqRoute,
			SourceAt: hq.CreatedAt}}, done...)
	}
	done = append(done, s.followUpItemsByStatus(ctx, userID, relationship, now, followup.StatusCompleted, out)...)
	if len(done) > todayResultCap {
		done = done[:todayResultCap]
	}
	if done == nil {
		done = []TodayItem{}
	}
	out.WorkingOn = TodaySection{Items: working, Health: combinedTodayHealth(working, out.Brief.Health, meetingsHealth(out.Meetings), setupHealth(out.SpecialistSetup))}
	out.NeedsYou = TodaySection{Items: needs, Health: combinedTodayHealth(needs, out.Priorities.Health, out.FollowUps.Health, out.Decisions.Health)}
	out.Done = TodaySection{Items: done, Health: combinedTodayHealth(done, out.Results.Health)}
	for _, src := range []struct {
		name   string
		health TodaySourceHealth
	}{
		{"Daily Brief", out.Brief.Health}, {"meetings", meetingsHealth(out.Meetings)},
		{"priorities", out.Priorities.Health}, {"follow-ups", out.FollowUps.Health},
		{"decisions", out.Decisions.Health}, {"results", out.Results.Health},
		{"memory", out.Remembered.Health}, {"specialist setup", setupHealth(out.SpecialistSetup)},
	} {
		if src.health.Status == TodaySectionUnavailable || src.health.Status == TodaySectionPartial {
			out.UnavailableSources = appendSource(out.UnavailableSources, src.name)
		}
	}
}

func appendSource(sources []string, name string) []string {
	for _, source := range sources {
		if source == name {
			return sources
		}
	}
	return append(sources, name)
}

func hqBriefSchedule(relationship *Projection) string {
	if relationship != nil && relationship.DailyBrief != nil && relationship.DailyBrief.ScheduleEnabled {
		timeOfDay := strings.TrimSpace(relationship.DailyBrief.ScheduleTime)
		if timeOfDay != "" {
			return "Daily Brief at " + timeOfDay
		}
	}
	return "Daily Brief in Personal HQ"
}

func firstTodaySetupRoute(setup *TodaySpecialistSetupProjection) string {
	if setup == nil {
		return ""
	}
	for _, action := range setup.Actions {
		if strings.HasPrefix(action.Route, "/workspaces/") && !strings.HasPrefix(action.Route, "//") {
			return action.Route
		}
	}
	return ""
}

func meetingsHealth(meetings *TodayMeetingsProjection) TodaySourceHealth {
	if meetings == nil {
		return TodaySourceHealth{Status: TodaySectionHealthyEmpty}
	}
	return meetings.Health
}
func setupHealth(setup *TodaySpecialistSetupProjection) TodaySourceHealth {
	if setup == nil {
		return TodaySourceHealth{Status: TodaySectionHealthyEmpty}
	}
	return setup.Health
}

func combinedTodayHealth(items []TodayItem, sources ...TodaySourceHealth) TodaySourceHealth {
	health := todayHealthForItems(items, time.Time{})
	for _, source := range sources {
		switch source.Status {
		case TodaySectionUnavailable:
			health.Status = TodaySectionUnavailable
		case TodaySectionPartial:
			if health.Status != TodaySectionUnavailable {
				health.Status = TodaySectionPartial
			}
		}
		if source.UpdatedAt.After(health.UpdatedAt) {
			health.UpdatedAt = source.UpdatedAt
		}
	}
	return health
}

// followUpItemsByStatus reads candidates and same-day completions in the
// same owner scope as the legacy active follow-up projection. It never infers a
// completed result from an active record.
func (s *TodayService) followUpItemsByStatus(ctx context.Context, userID string, relationship *Projection, now time.Time, status followup.Status, out *TodayProjection) []TodayItem {
	if s.followUps == nil {
		return nil
	}
	cfg := todayFollowUpConfig(relationship)
	scope := dailybrief.ResolveWorkspaceScope(s.followUpWorkspaces, cfg, userID)
	owners := dailybrief.ResolveFollowUpOwnerScope(s.followUpWorkspaces, scope, cfg.WorkspaceID, userID)
	items := make([]TodayItem, 0)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, owner := range owners.Owners {
		if !todaySafeSlug.MatchString(owner.WorkspaceSlug) {
			continue
		}
		rows, err := s.followUps.List(ctx, followup.Filter{UserID: userID, WorkspaceID: owner.WorkspaceID, Statuses: []followup.Status{status}})
		if err != nil {
			out.UnavailableSources = appendSource(out.UnavailableSources, "follow-ups")
			continue
		}
		for _, row := range rows {
			if row == nil || row.UserID != userID || row.WorkspaceID != owner.WorkspaceID || row.Status != status || strings.TrimSpace(row.ID) == "" {
				continue
			}
			when := row.UpdatedAt
			kind, title := "follow_up_candidate", "Confirm "+row.Title
			if status == followup.StatusCompleted {
				if row.CompletedAt == nil || row.CompletedAt.Before(start) || row.CompletedAt.After(now) {
					continue
				}
				when, kind, title = *row.CompletedAt, "follow_up_done", "Completed "+row.Title
			}
			items = append(items, TodayItem{ID: row.ID, Kind: kind, Title: truncateRunes(title, 200),
				Route:       recordTodayRoute("/workspaces/"+url.PathEscape(owner.WorkspaceSlug), "follow_up", row.ID),
				Attribution: truncateRunes(owner.Name, 100), SourceAt: when,
				Ref: dailybrief.SourceRef{WorkspaceID: owner.WorkspaceID, WorkspaceSlug: owner.WorkspaceSlug,
					EntityType: "follow_up", EntityID: row.ID, Timestamp: when},
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].SourceAt.After(items[j].SourceAt) })
	if len(items) > todayFollowUpCap {
		items = items[:todayFollowUpCap]
	}
	return items
}
