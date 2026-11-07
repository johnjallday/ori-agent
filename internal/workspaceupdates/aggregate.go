package workspaceupdates

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/session"
)

type aggregateStore interface {
	ListWorkspaceUpdateIDs(context.Context, string, string, int) ([]string, error)
	ReadWorkspaceUpdateTargets(context.Context, string, []string) ([]session.WorkspaceUpdateTarget, error)
}

type Page struct {
	Items     []Summary `json:"items"`
	HasMore   bool      `json:"has_more"`
	NextAfter string    `json:"next_after,omitempty"`
}

// Aggregate is a bounded read, not a delivery sweep or a new count authority.
// A continuation is explicit even when intervening deletes/transfers empty the
// visible page. Clients must not present a partial traversal as an all-clear.
func (s *Service) Aggregate(ctx context.Context, owner, after string, limit int) (Page, error) {
	if owner == "" || !session.ValidWorkspaceUpdatePage(after, limit) {
		return Page{}, session.ErrInvalidID
	}
	store, ok := s.store.(aggregateStore)
	if !ok {
		return Page{}, ErrUnavailable
	}
	ids, err := store.ListWorkspaceUpdateIDs(ctx, owner, after, limit)
	if err != nil {
		return Page{}, ErrUnavailable
	}
	out := Page{Items: []Summary{}, HasMore: len(ids) > limit}
	if out.HasMore {
		ids = ids[:limit]
		out.NextAfter = ids[len(ids)-1]
	}
	observed := make(map[string]Summary, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		if summary, err := s.Summary(ctx, owner, id); err == nil {
			observed[id] = summary
		}
	}
	// Do not return private labels/messages from an earlier row if that row's
	// owner or lifecycle changed while a later source was being read. A single
	// unreadable workspace is retained as unavailable, not silently omitted.
	targets, err := store.ReadWorkspaceUpdateTargets(ctx, owner, ids)
	if err != nil {
		return Page{}, ErrUnavailable
	}
	for _, target := range targets {
		summary, found := observed[target.ID]
		if !found || summary.version != target.Version {
			summary = Summary{WorkspaceID: target.ID, Name: safeLabel(target.Name, 120), FolderSlug: target.FolderSlug,
				ObservedAt: time.Now().UTC(), Welcome: WelcomeSource{State: StateUnavailable}, Review: filejanitor.FilingReview{State: StateUnavailable}}
		}
		out.Items = append(out.Items, summary)
	}
	return out, nil
}
