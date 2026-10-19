package personalassistant

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BuildPreparationClaim is host-only accepted creation authority, not a model
// patch or an operational-readiness flag. The current draft is frozen until
// creation settles it; canonical workspace/domain receipts own consequences.
// No path, selection token, prompt, or arbitrary request JSON is retained here.
type BuildPreparationClaim struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspace_id"`
	BuildVersion         int64     `json:"build_version"`
	ReviewRevision       string    `json:"review_revision"`
	RootRevision         string    `json:"root_revision"`
	TermsRevision        string    `json:"terms_revision"`
	Monitoring           bool      `json:"monitoring"`
	AcceptedAt           time.Time `json:"accepted_at"`
	LinkedReviewRevision string    `json:"linked_review_revision,omitempty"`
}

func (c BuildPreparationClaim) Validate() error {
	for _, id := range []string{c.ID, c.WorkspaceID} {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("%w: preparation identity", ErrWorkspaceBuildInvalid)
		}
	}
	for _, digest := range []string{c.ReviewRevision, c.RootRevision, c.TermsRevision} {
		if !buildPreparationDigest(digest) {
			return fmt.Errorf("%w: preparation revision", ErrWorkspaceBuildInvalid)
		}
	}
	if (c.LinkedReviewRevision != "" && !buildPreparationDigest(c.LinkedReviewRevision)) || c.AcceptedAt.IsZero() || c.BuildVersion < 1 {
		return fmt.Errorf("%w: preparation receipt", ErrWorkspaceBuildInvalid)
	}
	return nil
}

func buildPreparationDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
