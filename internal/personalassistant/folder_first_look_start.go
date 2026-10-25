package personalassistant

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FolderFirstLookStart is how one start of a hired agent's first look went:
// started, or the reason it was not (the first look's own reason codes), with
// the task when there is one.
type FolderFirstLookStart struct {
	Started bool
	Reason  string
	TaskID  string
}

// FolderFirstLookStarter is the legacy injected starter. Setup no longer calls
// it: an old hire record is not consent to launch a newly optional task.
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

// startHiredLook is deliberately inert for legacy callers/replays. Neither a
// saved hire nor an old setup plan authorizes a new optional summary. Existing
// task state and historical automatic-start records are left untouched.
func (s *FolderDigestService) startHiredLook(_ context.Context, _, _ string) {}
