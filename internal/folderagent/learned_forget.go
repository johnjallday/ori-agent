package folderagent

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ForgetLearned drops what the folder agent wrote from the songs it learned, for
// every folder that was set up into the Home: the summary (the agent's, or the
// host's line of ranges) and the goal it suggested. The Home's song-details
// switch calls it once the switch is off and the library has cleared its facts,
// so nothing written from a track or marker name outlives the consent that let
// Ori read it. What stays is what happened, not what was read: that a reading ran
// and how many songs it covered. A goal the owner already accepted is theirs and
// is not touched, and a goal offer still waiting falls back to the first look's.
//
// It only ever removes, so telling it twice does no harm. It returns how many
// folders it changed.
func (s *Service) ForgetLearned(homeID string) int {
	if s == nil || s.deps.Workspaces == nil || homeID == "" {
		return 0
	}
	ids, err := s.deps.Workspaces.IDs()
	if err != nil {
		return 0
	}
	changed := 0
	for _, id := range ids {
		rec, err := s.deps.Store.Read(id)
		if err != nil || songsHome(rec) != homeID || rec.Learning == nil || !holdsLearnedWords(rec.Learning) {
			continue
		}
		if _, err := s.deps.Store.Update(id, func(r *Record) error {
			if r.Learning != nil {
				r.Learning.Summary, r.Learning.SummaryBy, r.Learning.Goal = "", "", ""
			}
			return nil
		}); err == nil {
			changed++
		}
	}
	return changed
}

func holdsLearnedWords(l *Learning) bool {
	return l.Summary != "" || l.SummaryBy != "" || l.Goal != ""
}

// stillAllowed is whether the Home's song-details switch is on right now. A
// reading's words are written from facts the consent let Ori read, and the
// switch can go off while the agent is still writing them; they are kept only
// while it is on. An answer that cannot be had counts as off.
func (s *Service) stillAllowed(ctx context.Context, rec Record, homeID string) bool {
	state, _, err := s.deps.Songs.Consent(ctx, rec.OwnerUserID, rec.WorkspaceID, homeID)
	return err == nil && state == workspace.SongDetailsOn
}
