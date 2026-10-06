package personalassistant

import (
	"context"
	"sort"
	"strings"
	"time"
)

// A folder set up as a project is given one read-only first task: the
// assistant's first look. This file lets the Home card follow it: every
// resolved project offer's view carries the look's state, and the digest names
// the latest look so the mission card can start it without knowing an offer.
const (
	// folderFirstLookCandidates bounds how many recent folder workspaces a read
	// tries to find one with a first look. The digest read is polled, and each
	// candidate is one workspace read from disk.
	folderFirstLookCandidates = 3
	// folderFirstLookReceiptWindow is how long after its setup a folder's
	// receipt comes back to Home because its first look still needs the user.
	folderFirstLookReceiptWindow = 7 * 24 * time.Hour
	// folderFirstLookResultWindow is how long a finished look keeps its receipt
	// on Home, so the result is still there when the user comes back to it.
	// After that the result is in Today's Done.
	folderFirstLookResultWindow = time.Hour
)

// firstTaskView reads the first look of the workspace a resolved project offer
// made. ok is false for any other offer, and when the host has no read wired or
// has nothing to show for that workspace.
func (s *FolderDigestService) firstTaskView(ctx context.Context, offer FolderOffer) (FolderFirstTaskView, bool) {
	if s == nil || s.deps.FirstTaskView == nil || offer.Status != FolderOfferResolved || offer.Outcome == nil ||
		offer.Outcome.Kind != FolderChoiceProject || strings.TrimSpace(offer.Outcome.WorkspaceID) == "" {
		return FolderFirstTaskView{}, false
	}
	view, ok := s.deps.FirstTaskView(ctx, offer.Outcome.WorkspaceID)
	if !ok {
		return FolderFirstTaskView{}, false
	}
	view.OfferID = offer.ID
	return view, true
}

// latestFirstLook finds the first look to follow: the newest resolved project
// offer whose workspace still has one. More than the very latest is tried,
// because that workspace may have been trashed or set up without a first task.
func (s *FolderDigestService) latestFirstLook(ctx context.Context, doc FolderDigestDocument) (*FolderOffer, FolderFirstTaskView, bool) {
	if s == nil || s.deps.FirstTaskView == nil {
		return nil, FolderFirstTaskView{}, false
	}
	resolved := make([]*FolderOffer, 0, len(doc.Offers))
	for i := range doc.Offers {
		o := &doc.Offers[i]
		if o.Status == FolderOfferResolved && o.ResolvedAt != nil && o.Outcome != nil &&
			o.Outcome.Kind == FolderChoiceProject && strings.TrimSpace(o.Outcome.WorkspaceID) != "" {
			resolved = append(resolved, o)
		}
	}
	sort.SliceStable(resolved, func(i, j int) bool { return resolved[i].ResolvedAt.After(*resolved[j].ResolvedAt) })
	if len(resolved) > folderFirstLookCandidates {
		resolved = resolved[:folderFirstLookCandidates]
	}
	for _, offer := range resolved {
		if view, ok := s.firstTaskView(ctx, *offer); ok {
			return offer, view, true
		}
	}
	return nil, FolderFirstTaskView{}, false
}

// LatestFirstLook is where the first look of the user's most recent folder
// workspace stands, for the mission that pays for its result. ok is false when
// there is no folder workspace with a first look. It is a pure read.
func (s *FolderDigestService) LatestFirstLook(ctx context.Context, userID string) (FolderFirstTaskView, bool, error) {
	if s == nil || s.store == nil {
		return FolderFirstTaskView{}, false, ErrRepairNeeded
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderFirstTaskView{}, false, err
	}
	_, view, ok := s.latestFirstLook(ctx, doc)
	return view, ok, nil
}

// firstLookNeedsHome reports whether a first look brings its folder's receipt
// back to Home when nothing else is waiting: the look is still to start, is
// running, is waiting on the user or did not finish, and the folder was set up
// recently; or it finished a moment ago and its result is still news.
func firstLookNeedsHome(offer FolderOffer, view FolderFirstTaskView, now time.Time) bool {
	if offer.ResolvedAt == nil || offer.ResolvedAt.After(now) || now.Sub(*offer.ResolvedAt) > folderFirstLookReceiptWindow {
		return false
	}
	switch view.State {
	case FolderFirstTaskSeeded, FolderFirstTaskRunning, FolderFirstTaskWaiting, FolderFirstTaskFailed:
		return true
	case FolderFirstTaskFinished:
		return view.FinishedAt != nil && !view.FinishedAt.After(now) && now.Sub(*view.FinishedAt) <= folderFirstLookResultWindow
	default:
		return false
	}
}
