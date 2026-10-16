package projectlibrary

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Global exclusions and unresolved selections cannot be overridden by a local
// membership edit. Removing a member locally does not create a global exclusion.
func collectionCorrectionBlocked(record workspacearrangement.Record) map[string]bool {
	blocked := map[string]bool{}
	for _, id := range record.Draft.Unchanged.IDs {
		blocked[id] = true
	}
	for _, question := range record.Draft.NeedsReview {
		for _, id := range question.Items.IDs {
			blocked[id] = true
		}
	}
	return blocked
}

type CollectionCandidate struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Availability string `json:"availability"`
}
type CollectionCandidatePage struct {
	ArrangementID   string                `json:"arrangement_id"`
	Token           string                `json:"review_token"`
	CatalogRevision int64                 `json:"catalog_revision"`
	Operation       int                   `json:"operation"`
	Query           string                `json:"query"`
	Offset          int                   `json:"offset"`
	Total           int                   `json:"total"`
	Members         []CollectionCandidate `json:"members"`
	NextOffset      *int                  `json:"next_offset,omitempty"`
}

// Candidates is an owner-only saved-metadata picker, not discovery or approval.
// The review token freezes both the catalog and target proposal. No source paths,
// child contents or model calls are used to populate the bounded page.
func (s *CollectionReviewService) Candidates(ctx context.Context, owner, homeID, id, token string, operation, offset int, query string) (CollectionCandidatePage, error) {
	fail := CollectionCandidatePage{}
	if offset < 0 || offset > workspacearrangement.MaxSourceItems || offset%20 != 0 || !validText(query, 160) {
		return fail, ErrConflict
	}
	page, err := s.Page(ctx, owner, homeID, id, token, operation, 0)
	if err != nil {
		return fail, err
	}
	if operation >= len(page.Effects) || (page.Effects[operation].Kind != workspacearrangement.CreateCollection && page.Effects[operation].Kind != workspacearrangement.SetMembers && page.Effects[operation].Kind != workspacearrangement.SelectActivation) {
		return fail, ErrConflict
	}
	authority, err := s.authority(owner, homeID)
	if err != nil {
		return fail, err
	}
	scope, err := s.store.authorizeCollectionOwner(owner, authority)
	if err != nil {
		return fail, err
	}
	doc, err := s.store.Read(scope)
	if err != nil {
		return fail, err
	}
	index := arrangementIndex(doc, id)
	if doc.Revision != page.CatalogRevision || index < 0 {
		return fail, ErrConflict
	}
	record := doc.Arrangements[index].Record
	blocked := collectionCorrectionBlocked(record)
	if record.Draft.Operations[operation].Kind == workspacearrangement.SelectActivation {
		for _, entry := range doc.Entries {
			if entry.Link != nil {
				blocked[entry.ID] = true
			}
		}
	}
	for _, id := range record.Draft.Operations[operation].Members.IDs {
		blocked[id] = true
	}
	evidence, err := s.store.ArrangementEvidence(ctx, owner, authority)
	if err != nil {
		return fail, err
	}
	if evidence.Revision != strconv.FormatInt(page.CatalogRevision, 10) {
		return fail, ErrConflict
	}
	items := []CollectionCandidate{}
	for _, item := range evidence.Items {
		if !blocked[item.ID] && strings.Contains(strings.ToLower(item.Name), strings.ToLower(query)) {
			items = append(items, CollectionCandidate{ID: item.ID, Name: item.Name, Availability: item.Availability})
		}
	}
	slices.SortFunc(items, func(a, b CollectionCandidate) int { return strings.Compare(a.ID, b.ID) })
	if offset > len(items) || (offset == len(items) && offset != 0) {
		return fail, ErrConflict
	}
	end := min(offset+20, len(items))
	view := CollectionCandidatePage{ArrangementID: id, Token: token, CatalogRevision: page.CatalogRevision, Operation: operation, Query: query, Offset: offset, Total: len(items), Members: items[offset:end]}
	if end < len(items) {
		view.NextOffset = &end
	}
	// Recheck authority, expiry, runtime and the compiled review after projecting.
	if _, err := s.Page(ctx, owner, homeID, id, token, operation, 0); err != nil {
		return fail, err
	}
	raw, err := json.Marshal(view)
	if err != nil || len(raw) > workspacearrangement.MaxSummaryBytes {
		return fail, ErrLimit
	}
	return view, ctx.Err()
}
