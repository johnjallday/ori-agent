package personalassistant

import (
	"encoding/hex"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

// FolderHomeStaffingReview is bounded private draft state on the existing
// offer, not a run or another approval journal. ContextDigest binds the offer's
// owner/subject and pre-staffing plan. Team contains only explicitly selected
// original witnesses; refreshing Advice cannot select, clear or renew it.
// A clear still increments Revision so a late recommendation cannot overwrite
// an otherwise empty draft. Public views must project Advice.Public/Choices.
type FolderHomeStaffingReview struct {
	Version       int                        `json:"version"`
	Revision      int64                      `json:"revision"`
	ContextDigest string                     `json:"context_digest"`
	Advice        *rosterstaffing.Advice     `json:"advice,omitempty"`
	Team          *rosterstaffing.TeamIntent `json:"team,omitempty"`
}

func (r *FolderHomeStaffingReview) Clone() *FolderHomeStaffingReview {
	if r == nil {
		return nil
	}
	copy := *r
	copy.Advice, copy.Team = r.Advice.Clone(), r.Team.Clone()
	return &copy
}

func (r *FolderHomeStaffingReview) validate() error {
	if r == nil {
		return nil
	}
	// Revisions are compared by the existing browser review controllers too.
	if r.Version != 1 || r.Revision < 1 || r.Revision > 1<<53-1 || len(r.ContextDigest) != 64 {
		return ErrValidation
	}
	if _, err := hex.DecodeString(r.ContextDigest); err != nil {
		return ErrValidation
	}
	if r.Advice != nil {
		if r.Advice.Version != 2 || r.Advice.TemplateID == "" || r.Advice.TemplateRevision == "" || r.Advice.Validate(0) != nil {
			return ErrValidation
		}
		for _, role := range r.Advice.Roles {
			if role.Scope != "home" {
				return ErrValidation
			}
		}
	}
	if r.Team != nil {
		if r.Team.Validate() != nil {
			return ErrValidation
		}
		for _, role := range r.Team.Roles {
			if role.Scope != "home" {
				return ErrValidation
			}
		}
	}
	return nil
}
