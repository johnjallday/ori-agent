package downloadsjanitor

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Action Center is an entry point, not a second copy of the Janitor's state.
//
// A ready batch produces one item per workspace, updated in place as later
// scans add to it — the Janitor's whole promise is to reduce noise, so a
// feature that emitted one notification per file would be a feature that made
// the problem worse (FR-103, FR-104).
//
// The batch itself lives in the Janitor's store. Dismissing or resolving a
// finding here does not discard pending candidates: the user still has files
// waiting, and the review surface still says so (FR-13 of this group's intent).

// Notifier is the slice of Action Center the Janitor uses. *workspace's
// opportunity store satisfies it.
type Notifier interface {
	Upsert(opp workspace.Opportunity) (workspace.Opportunity, bool, error)
}

// Fixed titles, because the title is the dedup key: one ready-batch item and
// one needs-attention item per workspace, updated rather than multiplied.
const (
	readyBatchTitle    = "Downloads ready to review"
	needsAttentionText = "Downloads Janitor needs attention"
)

// SetNotifier injects the Action Center store. A nil notifier disables
// notifications without affecting scanning.
func (s *Service) SetNotifier(notifier Notifier) {
	if s != nil {
		s.notifier = notifier
	}
}

// notifyBatchReady creates or updates the single ready-batch entry for a
// workspace.
func (s *Service) notifyBatchReady(workspaceID string, batch JanitorBatch) {
	if s == nil || s.notifier == nil {
		return
	}
	pending := batch.Summary.Proposed
	if pending == 0 {
		// Nothing for the user to do: no entry. A scan that proposed nothing is
		// not worth an interruption.
		return
	}

	summary := fmt.Sprintf("%d file%s ready for review in your Downloads folder.", pending, plural(pending))
	if batch.Summary.NeedsReview > 0 {
		summary += fmt.Sprintf(" %d need%s a closer look.", batch.Summary.NeedsReview, pluralVerb(batch.Summary.NeedsReview))
	}

	opp := workspace.Opportunity{
		WorkspaceID: workspaceID,
		Title:       readyBatchTitle,
		Summary:     summary,
		Priority:    "low",
		Confidence:  "high",
		Status:      workspace.OpportunityNew,
		// The link is a deep link into the review surface, so the entry is a
		// way in rather than a place to approve from.
		RecommendedAction: "Review the proposed filing",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	if _, _, err := s.notifier.Upsert(opp); err != nil {
		logger.Warn("Downloads Janitor could not record a ready-batch notification", logger.Fields{
			"workspace_id": workspaceID, "error": err,
		})
	}
}

// reportScanFailure creates or updates the single needs-attention entry.
// Repeated failures of the same kind update one item rather than flooding
// (FR-107).
func (s *Service) reportScanFailure(workspaceID string, cause error) {
	if s == nil || s.notifier == nil || cause == nil {
		return
	}
	summary := "Ori could not scan your Downloads folder."
	switch {
	case strings.Contains(cause.Error(), "permission"):
		summary = "Ori no longer has permission to read your Downloads folder."
	case strings.Contains(cause.Error(), "no longer exists"), strings.Contains(cause.Error(), "no longer linked"):
		summary = "The folder Ori was tidying is no longer available."
	}

	opp := workspace.Opportunity{
		WorkspaceID:       workspaceID,
		Title:             needsAttentionText,
		Summary:           summary,
		Priority:          "medium",
		Confidence:        "high",
		Status:            workspace.OpportunityNew,
		RecommendedAction: "Open Downloads Janitor settings to reconnect the folder",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	if _, _, err := s.notifier.Upsert(opp); err != nil {
		logger.Warn("Downloads Janitor could not record a needs-attention notification", logger.Fields{
			"workspace_id": workspaceID, "error": err,
		})
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func pluralVerb(n int) string {
	if n == 1 {
		return "s"
	}
	return ""
}
