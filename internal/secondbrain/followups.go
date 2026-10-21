package secondbrain

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
)

const (
	// FollowUpSourceType is the SourceRef type of a follow-up made from an
	// open item in the user's notes (FR28).
	FollowUpSourceType = "note_item"
	// MaxSuggested caps the items the primary action adds (FR24).
	MaxSuggested = 10
)

// Suggest chooses the items the primary action adds (FR24): dated items
// first, soonest due first; then items in notes that mark an open decision;
// then items in notes edited in the last week; at most MaxSuggested. When
// none of those apply, the most recently edited notes' items stand in, so a
// look with open items always has something to add. It returns the chosen
// indices of loops.
func Suggest(loops []OpenLoop) map[int]bool {
	order := make([]int, 0, len(loops))
	for index := range loops {
		order = append(order, index)
	}
	rank := func(loop OpenLoop) int {
		switch {
		case loop.Due != "":
			return 0
		case loop.InDecision:
			return 1
		case loop.InActive:
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := loops[order[i]], loops[order[j]]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.Due != b.Due {
			return a.Due < b.Due
		}
		return false // keep the newest-note-first order of loops
	})
	chosen := make(map[int]bool, MaxSuggested)
	for _, index := range order {
		if len(chosen) == MaxSuggested || rank(loops[index]) == 3 {
			break
		}
		chosen[index] = true
	}
	if len(chosen) == 0 {
		for index := range loops {
			if len(chosen) == MaxSuggested {
				break
			}
			chosen[index] = true
		}
	}
	return chosen
}

// FollowUps is the part of the follow-up service an acceptance uses.
type FollowUps interface {
	Capture(ctx context.Context, in followup.CaptureInput) (*followup.FollowUp, error)
	FindBySource(ctx context.Context, userID, sourceType, sourceID string) (*followup.FollowUp, error)
}

// Acceptance turns chosen open items into HQ follow-ups.
type Acceptance struct {
	UserID      string
	WorkspaceID string
	Source      Source
	Items       []LookItem
	// Location places a due date; it defaults to the server's own zone.
	Location *time.Location
}

// Accept creates a follow-up for each item that does not have one yet (FR28):
// SourceRef{note_item, item id}, the line as the title, the note's relative
// path as the detail, the item's date as the due date. An item accepted
// before, by this look or an earlier one, in whatever state the user left it,
// is skipped and counted, never captured again.
func Accept(ctx context.Context, followUps FollowUps, in Acceptance) (created, skipped int, err error) {
	if followUps == nil {
		return 0, 0, errors.New("secondbrain: follow-ups unavailable")
	}
	location := in.Location
	if location == nil {
		location = time.Local
	}
	for _, item := range in.Items {
		_, err := followUps.FindBySource(ctx, in.UserID, FollowUpSourceType, item.ID)
		switch {
		case err == nil:
			skipped++
			continue
		case !errors.Is(err, followup.ErrNotFound):
			return created, skipped, err
		}
		if _, err := followUps.Capture(ctx, captureInput(in, item, location)); err != nil {
			return created, skipped, err
		}
		created++
	}
	return created, skipped, nil
}

func captureInput(in Acceptance, item LookItem, location *time.Location) followup.CaptureInput {
	capture := followup.CaptureInput{
		UserID: in.UserID, WorkspaceID: in.WorkspaceID,
		Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound,
		Title: item.Text, Detail: item.Note,
		Source:     followup.SourceRef{Type: FollowUpSourceType, ID: item.ID},
		Provenance: followup.ProvenanceExplicit,
	}
	if item.InDecision {
		capture.Category, capture.Direction = followup.CategoryNeedsDecision, followup.DirectionNone
	}
	if due, err := time.ParseInLocation("2006-01-02", item.Due, location); err == nil {
		// Due at the end of that day, so an item due today is not yet late.
		end := due.AddDate(0, 0, 1).Add(-time.Second)
		capture.DueAt = &end
	}
	return capture
}
