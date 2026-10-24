package personalassistant

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// FolderFirstLookStart is how one start of a hired agent's first look went:
// started, or the reason it was not (the first look's own reason codes), with
// the task when there is one.
type FolderFirstLookStart struct {
	Started bool
	Reason  string
	TaskID  string
}

// FolderFirstLookStarter starts the first look a setup seeded, under the hire's
// consent: the same one start Start first look makes, through the same path,
// and only when the look could start now. A look whose agent has no usable
// model is left waiting with its reason.
type FolderFirstLookStarter interface {
	StartFirstLook(ctx context.Context, userID, workspaceID string) (FolderFirstLookStart, error)
}

// FolderFirstLookRecord is what a hire did about the first look once the
// workspace was verified: started it, or why it did not. It is kept on the
// outcome so the card can tell the look the hire started from one the user
// starts later, and so a replayed settle never starts it twice. An inbox
// hire's first look is its first batch instead: no task is started.
type FolderFirstLookRecord struct {
	Started bool              `json:"started"`
	Reason  string            `json:"reason,omitempty"`
	TaskID  string            `json:"task_id,omitempty"`
	Batch   *FolderFirstBatch `json:"batch,omitempty"`
	At      time.Time         `json:"at"`
}

func (r *FolderFirstLookRecord) validate() error {
	if r == nil {
		return nil
	}
	if err := r.Batch.validate(); err != nil {
		return err
	}
	if r.Batch != nil && (r.Started || r.TaskID != "") {
		return fmt.Errorf("%w: first look record", errFolderDigestInvalid)
	}
	for _, text := range []string{r.Reason, r.TaskID} {
		if len(text) > 200 || strings.ContainsAny(text, "/\\\x00\r\n") {
			return fmt.Errorf("%w: first look record", errFolderDigestInvalid)
		}
	}
	return nil
}

// startHiredLook starts the first look of a workspace a hire just set up, once.
// Only a run that hired someone does this: the consent given by Hire and start
// covers reading the folder, and nothing else. It runs after the outcome was
// verified, so the workspace exists and the folder is linked; a failure to
// start is recorded and leaves the look where Start first look can start it.
func (s *FolderDigestService) startHiredLook(ctx context.Context, userID, offerID string) {
	if s.deps.FirstLook == nil {
		return
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Status != FolderOfferResolved || offer.Outcome == nil || strings.TrimSpace(offer.Outcome.WorkspaceID) == "" ||
		offer.Outcome.FirstLook != nil || offer.Setup == nil || offer.Setup.Hire == nil {
		return
	}
	// An inbox hire's first look is the batch its setup prepared; no model
	// task is ever started for it.
	if offer.Setup.Intent.Inbox != nil || isInboxOffer(*offer) {
		return
	}
	workspaceID := offer.Outcome.WorkspaceID
	start, err := s.deps.FirstLook.StartFirstLook(ctx, userID, workspaceID)
	record := FolderFirstLookRecord{Started: start.Started, Reason: start.Reason, TaskID: start.TaskID, At: s.now()}
	if err != nil {
		logger.Warn("The hired agent's first look could not be started", logger.Fields{"offer_id": offerID, "workspace_id": workspaceID, "error": err.Error()})
		record.Started = false
		if record.Reason == "" {
			record.Reason = FolderFirstTaskReasonStartFailed
		}
	}
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil || item.Outcome == nil || item.Outcome.WorkspaceID != workspaceID || item.Outcome.FirstLook != nil {
			return errFolderReplay
		}
		item.Outcome.FirstLook = &record
		return nil
	})
	if err != nil && err != errFolderReplay {
		logger.Warn("The hired agent's first look was not recorded", logger.Fields{"offer_id": offerID, "error": err.Error()})
	}
}
