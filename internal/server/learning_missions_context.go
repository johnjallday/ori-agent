package server

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

func (s learningMissionSource) Activity(ctx context.Context, owner, id string, since, until time.Time) (learningmissions.ActivityCounts, error) {
	unknown := learningmissions.ActivityCounts{Coverage: "unknown"}
	if _, err := s.owned(owner, id); err != nil {
		return unknown, learningmissions.ErrUnavailable
	}
	reader, ok := s.runs.(workspacerun.RecentOutcomeReader)
	if !ok {
		return unknown, learningmissions.ErrUnavailable
	}
	outcomes, err := reader.RecentOutcomes(ctx, id, since, until, learningmissions.RecentActivityLimit)
	if err != nil {
		return unknown, learningmissions.ErrUnavailable
	}
	// This covers canonical Runs only, not all old Ticket history or whether
	// the user found a result useful. Keep coverage partial even on a short page.
	counts := learningmissions.ActivityCounts{Coverage: "partial", Observed: len(outcomes)}
	for _, status := range outcomes {
		switch status {
		case workspacerun.RunStatusSucceeded:
			counts.Succeeded++
		case workspacerun.RunStatusFailed:
			counts.Failed++
		}
	}
	return counts, nil
}
