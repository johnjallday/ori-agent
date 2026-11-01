package personalassistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// An inbox hire's first look is its first batch (folder-inbox-hire FR19,
// FR20): File Curator reports it back under Needs you, on Home's Today and in
// the drawer, with the batch's counts and one primary action. The card is the
// same report card a hired agent's first look uses; the kind tells it which
// actions and routes are its own.
const TodayKindFirstBatchReport = "folder_first_batch"

// The actions an inbox hire's report offers: Review batch and Open File
// Janitor open the review surface; Retry runs the first scan again; Not now
// takes the report away. Check protection opens the folder's protection
// destination; it is navigation, never a POST.
const (
	FirstBatchActionReview     = "review_batch"
	FirstBatchActionOpen       = "open_janitor"
	FirstBatchActionRetry      = "retry"
	FirstBatchActionProtection = "check_protection"
	// FirstBatchActionLearned opens the card of what Ori learned from the files
	// the user approved. It is navigation inside the drawer, never a POST.
	FirstBatchActionLearned = "review_learned"
)

// firstBatchLearnedSentence is the line a report gains once the approved files
// taught Ori something: how many things, and where they go if approved.
func firstBatchLearnedSentence(learned int) string {
	if learned <= 0 {
		return ""
	}
	if learned == 1 {
		return "I learned 1 thing from this folder; approve it and it goes into your second brain."
	}
	return fmt.Sprintf("I learned %d things from this folder; approve them and they go into your second brain.", learned)
}

// withLearnedSentence gives a Ready first-batch report the line above and the
// action that opens the card where the user answers it (first-week FR16). Only a
// report with a batch to review changes: a failed or empty scan has nothing the
// files could have taught, and the action goes before Not now.
func withLearnedSentence(items []TodayItem, learned int) []TodayItem {
	sentence := firstBatchLearnedSentence(learned)
	if sentence == "" {
		return items
	}
	out := make([]TodayItem, len(items))
	copy(out, items)
	for index, item := range out {
		if item.Kind != TodayKindFirstBatchReport || item.State != "finished" || !slices.Contains(item.Actions, FirstBatchActionReview) {
			continue
		}
		actions := make([]string, 0, len(item.Actions)+1)
		for _, action := range item.Actions {
			if action == FirstLookActionNotNow {
				actions = append(actions, FirstBatchActionLearned)
			}
			actions = append(actions, action)
		}
		out[index].Detail, out[index].Actions = sentence, actions
	}
	return out
}

// firstBatchProtectionSentences is the evidence line for each protection
// state that earns a backup suggestion. It must equal the review console's
// protectionSentence in file-janitor-protection.js; a test holds them together.
// covered and unavailable earn none.
var firstBatchProtectionSentences = map[string]string{
	"not_covered": "This folder is not in the backup evidence Ori observes. Other backups may exist.",
	"unknown":     "Protection is unknown. Missing evidence does not mean there is no backup.",
	"configured":  "A backup source is configured, but Ori has not observed a completed backup.",
}

// firstBatchBackupSuggestion is the host's own backup suggestion for a Ready
// first batch (backup-first FR1 to FR3): the folder's stored protection fact,
// read as the curator's chat reads it, and nil when it is covered,
// unavailable, unread or not this workspace's.
func firstBatchBackupSuggestion(ctx context.Context, reader filecuratorguidance.ProtectionReader, ws *workspace.Workspace, folder string) *FirstLookSuggestion {
	if reader == nil {
		return nil
	}
	userID := ws.OwnerUserID
	if userID == "" {
		userID = userprofile.LocalUserID
	}
	fact, err := reader.ReadProtection(ctx, userID, ws.ID)
	if err != nil {
		logger.Warn("The first batch's protection fact could not be read", logger.Fields{"workspace_id": ws.ID, "error": err.Error()})
		return nil
	}
	evidence, unmet := firstBatchProtectionSentences[fact.State]
	if fact.WorkspaceID != ws.ID || !unmet {
		return nil
	}
	return &FirstLookSuggestion{Text: truncateRunes("Back up "+folder+" before the first filing batch", 200), Evidence: evidence}
}

// firstBatchReportItems is an inbox hire's report as Today shows it: under
// Needs you until Not now, for as long as a folder receipt is shown. handled is
// true for every inbox hire, so no first-look item is made for one. The copy
// is the host's own and names no path: the folder by its name only.
func firstBatchReportItems(ctx context.Context, offer FolderOffer, ws *workspace.Workspace, now time.Time, protection filecuratorguidance.ProtectionReader) (needs []TodayItem, handled bool) {
	if offer.Outcome == nil || offer.Outcome.FirstLook == nil || offer.Outcome.FirstLook.Batch == nil || !isInboxOffer(offer) {
		return nil, false
	}
	look := offer.Outcome.FirstLook
	batch := look.Batch
	at := look.At
	if batch.DismissedAt != nil || at.IsZero() || at.After(now) || now.Sub(at) > todayFolderReceiptWindow || batch.validate() != nil {
		return nil, true
	}
	folder := strings.TrimSpace(offer.Subject.Name)
	if folder == "" || looksLikeFilesystemPath(folder) {
		return nil, true
	}
	agent := "File Curator"
	if offer.Setup != nil && offer.Setup.Intent.Inbox != nil && strings.TrimSpace(offer.Setup.Intent.Inbox.Agent.Name) != "" {
		agent = strings.TrimSpace(offer.Setup.Intent.Inbox.Agent.Name)
	}
	// The batch's route was recorded at setup; it opens the workspace by the
	// name it has now.
	recorded, _, _ := splitWorkspaceRoute(batch.Route)
	route := rerouteWorkspace(batch.Route, recorded, ws.FolderSlug)
	item := TodayItem{
		ID: "first-batch-" + offer.ID, Kind: TodayKindFirstBatchReport,
		Attribution: truncateRunes(agent, 100), Workspace: truncateRunes(ws.Name, 120),
		Route: route, ReportRoute: route, SourceAt: at, State: "finished",
		Ref: dailybrief.SourceRef{WorkspaceID: ws.ID, EntityType: "folder_offer", EntityID: offer.ID, Timestamp: at},
	}
	switch batch.Outcome {
	case FolderFirstBatchReady:
		item.Title = truncateRunes(firstBatchSentence(agent, folder, batch.Eligible, batch.NeedsReview), 200)
		item.Actions = []string{FirstBatchActionReview, FirstLookActionNotNow}
		// An unmet protection fact leads: back the folder up before filing.
		if suggestion := firstBatchBackupSuggestion(ctx, protection, ws, folder); suggestion != nil {
			item.Suggestion = suggestion
			item.Actions = []string{FirstBatchActionProtection, FirstBatchActionReview, FirstLookActionNotNow}
		}
	case FolderFirstBatchEmpty:
		item.Title = truncateRunes("Nothing to file in "+folder+" right now.", 200)
		item.Actions = []string{FirstBatchActionOpen, FirstLookActionNotNow}
	default:
		item.Title = truncateRunes("The first scan of "+folder+" did not complete.", 200)
		item.Detail = "The workspace, " + agent + " and the folder link are kept. Retry runs the scan again; nothing moves."
		item.State = "failed"
		item.Actions = []string{FirstBatchActionRetry}
	}
	return []TodayItem{item}, true
}

// firstBatchSentence is FR19's report: "File Curator prepared a first batch for
// Downloads: 23 files proposed for filing, 4 need your review."
func firstBatchSentence(agent, folder string, proposed, review int) string {
	files := fmt.Sprintf("%d files proposed for filing", proposed)
	if proposed == 1 {
		files = "1 file proposed for filing"
	}
	needs := fmt.Sprintf("%d need your review", review)
	if review == 1 {
		needs = "1 needs your review"
	}
	return fmt.Sprintf("%s prepared a first batch for %s: %s, %s.", agent, folder, files, needs)
}
