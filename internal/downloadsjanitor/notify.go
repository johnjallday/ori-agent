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
//
// These are folder-neutral on purpose. The capability manages whichever folder
// the user approved — Scans, Desktop, an upload drop — so a title naming
// Downloads would be wrong everywhere except the preset.
//
// Changing a title orphans any entry created under the old one, because upsert
// dedups by title. That is a one-time, per-workspace cosmetic leftover, and the
// alternative — telling a user tidying ~/Scans that "Downloads" needs attention
// — is worse.
const (
	readyBatchTitle    = "Files ready to review"
	needsAttentionText = "File Janitor needs attention"
)

// folderLabel returns a short, safe name for the managed folder, for use in
// notification text. It falls back to a neutral phrase rather than guessing:
// before setup, and when settings cannot be read, there is no folder to name.
func (s *Service) folderLabel(workspaceID string) string {
	if s == nil || s.store == nil {
		return "the folder Ori is tidying"
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return "the folder Ori is tidying"
	}
	if name := folderDisplayName(settings.RootPath); name != "" {
		return name
	}
	return "the folder Ori is tidying"
}

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

	summary := fmt.Sprintf("%d file%s ready for review in %s.", pending, plural(pending), s.folderLabel(workspaceID))
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
		logger.Warn("File Janitor could not record a ready-batch notification", logger.Fields{
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
	label := s.folderLabel(workspaceID)
	summary := fmt.Sprintf("Ori could not scan %s.", label)
	switch {
	case strings.Contains(cause.Error(), "permission"):
		summary = fmt.Sprintf("Ori no longer has permission to read %s.", label)
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
		RecommendedAction: "Open File Janitor settings to reconnect the folder",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	if _, _, err := s.notifier.Upsert(opp); err != nil {
		logger.Warn("File Janitor could not record a needs-attention notification", logger.Fields{
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
