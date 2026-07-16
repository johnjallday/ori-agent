package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalhq"
)

// journalSnapshotBuilder assembles the grounded end-of-day journal snapshot. v1
// grounds the draft on follow-ups the user closed during the local day; completed
// tasks and notable email threads are future additions.
type journalSnapshotBuilder struct {
	followups *followup.Service
}

func (b *journalSnapshotBuilder) BuildJournalSnapshot(ctx context.Context, userID, localDate string) (personalhq.JournalSnapshot, error) {
	var snap personalhq.JournalSnapshot
	if b == nil || b.followups == nil {
		return snap, nil
	}
	completed, err := b.followups.List(ctx, followup.Filter{UserID: userID, Statuses: []followup.Status{followup.StatusCompleted}})
	if err != nil {
		return snap, err
	}
	for _, f := range completed {
		if f.CompletedAt != nil && f.CompletedAt.Format("2006-01-02") == localDate {
			snap.FollowUpChanges = append(snap.FollowUpChanges, "Closed: "+f.Title)
		}
	}
	return snap, nil
}
