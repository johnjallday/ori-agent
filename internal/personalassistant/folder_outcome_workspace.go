package personalassistant

import (
	"context"
	"net/url"
	"strings"
)

// A resolved offer's receipt is written once, when its setup finishes, with
// links to the workspace it made. That workspace can be renamed or deleted
// afterwards, so a view follows it: the links take the workspace's current
// slug, and a deleted workspace's links are dropped and the view says why,
// rather than leading to a page that is no longer there.

// Why a resolved offer's workspace is no longer there.
const (
	// FolderWorkspaceTrashed is a workspace deleted to the Trash: restoring it
	// brings back everything the setup made.
	FolderWorkspaceTrashed = "trashed"
	// FolderWorkspaceRemoved is a workspace with no record left at all.
	FolderWorkspaceRemoved = "removed"
)

// FolderOutcomeWorkspace is where a resolved offer's workspace stands now.
type FolderOutcomeWorkspace struct {
	// Slug is the workspace's current folder slug, while it is there.
	Slug string
	// Gone is FolderWorkspaceTrashed or FolderWorkspaceRemoved, and empty
	// while the workspace is there.
	Gone string
}

// followOutcomeWorkspace points a resolved offer's view at its workspace as it
// stands now. A workspace that could not be read keeps its links as recorded:
// not being able to check is not the same as gone.
func (s *FolderDigestService) followOutcomeWorkspace(ctx context.Context, v *FolderOfferView) {
	if s == nil || s.deps.OutcomeWorkspace == nil || v == nil || v.Status != FolderOfferResolved || v.Outcome == nil {
		return
	}
	id := strings.TrimSpace(v.Outcome.WorkspaceID)
	recorded, _, ok := splitWorkspaceRoute(v.Outcome.Route)
	if id == "" || !ok {
		return
	}
	now, read := s.deps.OutcomeWorkspace(ctx, id)
	if !read {
		return
	}
	switch now.Gone {
	case FolderWorkspaceTrashed, FolderWorkspaceRemoved:
		v.Outcome = outcomeOnWorkspace(*v.Outcome, recorded, "")
		v.WorkspaceGone = now.Gone
	case "":
		if now.Slug != "" && now.Slug != recorded {
			v.Outcome = outcomeOnWorkspace(*v.Outcome, recorded, now.Slug)
		}
	}
}

// outcomeOnWorkspace is a copy of outcome whose links to the workspace recorded
// as from open it as to instead, or are dropped when to is empty. Links to any
// other page (a parent Home's library) are kept. The stored outcome is never
// changed.
func outcomeOnWorkspace(outcome FolderOutcome, from, to string) *FolderOutcome {
	outcome.Route = rerouteWorkspace(outcome.Route, from, to)
	outcome.HomeRoute = rerouteWorkspace(outcome.HomeRoute, from, to)
	if outcome.Receipt != nil {
		rows := make([]FolderReceiptRow, len(outcome.Receipt))
		for i, row := range outcome.Receipt {
			row.Route = rerouteWorkspace(row.Route, from, to)
			rows[i] = row
		}
		outcome.Receipt = rows
	}
	if outcome.FirstLook != nil {
		look := *outcome.FirstLook
		if look.Batch != nil {
			batch := *look.Batch
			batch.Route = rerouteWorkspace(batch.Route, from, to)
			look.Batch = &batch
		}
		outcome.FirstLook = &look
	}
	return &outcome
}

// splitWorkspaceRoute splits a workspace page route into the slug it opens and
// what follows it: "/workspaces/downloads?panel=file-janitor" is "downloads"
// and "?panel=file-janitor".
func splitWorkspaceRoute(route string) (slug, rest string, ok bool) {
	tail, found := strings.CutPrefix(route, "/workspaces/")
	if !found {
		return "", "", false
	}
	end := strings.IndexAny(tail, "/?#")
	if end < 0 {
		end = len(tail)
	}
	slug, err := url.PathUnescape(tail[:end])
	if err != nil || slug == "" {
		return "", "", false
	}
	return slug, tail[end:], true
}

// rerouteWorkspace gives a route that opens the workspace recorded as from the
// slug to, or drops it ("") when to is empty. A route to any other page is
// returned as it is.
func rerouteWorkspace(route, from, to string) string {
	slug, rest, ok := splitWorkspaceRoute(route)
	if !ok || slug != from {
		return route
	}
	if to == "" {
		return ""
	}
	return "/workspaces/" + url.PathEscape(to) + rest
}
