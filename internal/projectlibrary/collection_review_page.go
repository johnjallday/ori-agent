package projectlibrary

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// CollectionReviewPage discloses metadata-only effects. Its token belongs to
// this owner/Home review, never to a model tool or a portable transcript.
type CollectionReviewPage struct {
	ArrangementID   string                        `json:"arrangement_id"`
	Token           string                        `json:"review_token"`
	CatalogRevision int64                         `json:"catalog_revision"`
	ExpiresAt       time.Time                     `json:"expires_at"`
	Goal            string                        `json:"goal"`
	Rationale       string                        `json:"rationale"`
	NeedsReview     []CollectionReviewCaution     `json:"needs_review"`
	Unchanged       int                           `json:"unchanged"`
	Unknowns        []string                      `json:"unknowns"`
	Coverage        workspacearrangement.Coverage `json:"coverage"`
	Effects         []CollectionEffectView        `json:"effects"`
	Operation       int                           `json:"operation"`
	Offset          int                           `json:"offset"`
	Total           int                           `json:"total"`
	Members         []CollectionMemberView        `json:"members"`
	NextOffset      *int                          `json:"next_offset,omitempty"`
}
type CollectionReviewCaution struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}
type CollectionEffectView struct {
	Kind          string `json:"kind"`
	CollectionID  string `json:"collection_id"`
	QueueID       string `json:"queue_id,omitempty"`
	BeforeName    string `json:"before_name"`
	AfterName     string `json:"after_name"`
	BeforeMembers int    `json:"before_members"`
	AfterMembers  int    `json:"after_members"`
}
type CollectionMemberView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Availability string `json:"availability"`
	Before       bool   `json:"before"`
	After        bool   `json:"after"`
}

// CollectionReviewService exposes only canonical collection review and commit,
// not an arbitrary document writer. Manager identity is derived from the Home.
type CollectionReviewService struct {
	store   *Store
	resolve ArrangementRuntimeResolver
}

func NewCollectionReviewService(store *Store, resolve ArrangementRuntimeResolver) *CollectionReviewService {
	return &CollectionReviewService{store: store, resolve: resolve}
}
func (s *CollectionReviewService) authority(owner, homeID string) (ManagerAuthority, error) {
	if s == nil || s.store == nil || s.store.workspaces == nil || !collectionLabel(homeID, 160) || owner == "" {
		return ManagerAuthority{}, ErrUnavailable
	}
	home, err := s.store.workspaces.Get(homeID)
	if err != nil || home == nil || home.OwnerUserID != owner {
		return ManagerAuthority{}, ErrUnavailable
	}
	authority, ok := boundHomeManager(home.GetAssistantProgramState(), home)
	if !ok {
		return ManagerAuthority{}, ErrUnavailable
	}
	return authority, nil
}
func (s *CollectionReviewService) Review(ctx context.Context, owner, homeID, id string, catalogRevision, recordRevision int64) (CollectionReviewPage, error) {
	authority, err := s.authority(owner, homeID)
	if err != nil {
		return CollectionReviewPage{}, err
	}
	review, err := s.store.reviewCollectionChanges(ctx, owner, authority, id, catalogRevision, recordRevision, s.resolve)
	if err != nil {
		return CollectionReviewPage{}, err
	}
	return s.Page(ctx, owner, homeID, id, review.Token, 0, 0)
}
func (s *CollectionReviewService) Commit(ctx context.Context, owner, homeID, id, token, key string, confirmed bool) (*ArrangementHistory, error) {
	authority, err := s.authority(owner, homeID)
	if err != nil {
		return nil, err
	}
	if _, _, err := s.store.commitCollectionChanges(ctx, owner, authority, id, token, key, confirmed, s.resolve); err != nil {
		return nil, err
	}
	scope, err := s.store.authorizeCollectionOwner(owner, authority)
	if err != nil {
		return nil, err
	}
	return s.store.ReadArrangementHistory(ctx, scope, id)
}
func (s *CollectionReviewService) Page(ctx context.Context, owner, homeID, id, token string, operation, offset int) (CollectionReviewPage, error) {
	fail := CollectionReviewPage{}
	if operation < 0 || operation >= workspacearrangement.MaxOperations || offset < 0 || offset > workspacearrangement.MaxSourceItems || offset%50 != 0 {
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
	doc, state, err := s.store.readSnapshot(scope)
	if err != nil {
		return fail, err
	}
	review, ok := findReview(doc, token, collectionReviewAction)
	i := arrangementIndex(doc, id)
	if !ok || i < 0 || review.TargetID != id || review.Collection == nil || review.ConsumedAt != nil || review.Revision != doc.Revision || !review.ExpiresAt.After(s.store.now().UTC()) {
		return fail, ErrConflict
	}
	saved := doc.Arrangements[i]
	if saved.Record.State != "awaiting_user_review" || saved.Record.Revision != review.Collection.ArrangementRevision || saved.BindingDigest != arrangementBinding(state, authority) || saved.BindingDigest != review.Collection.BindingDigest {
		return fail, ErrConflict
	}
	if err := checkCollectionRuntime(ctx, authority, saved, s.resolve); err != nil {
		return fail, err
	}
	changes, err := compileCollectionChanges(doc, id, review.Collection.PreparedAt)
	if err != nil {
		return fail, err
	}
	digest, err := collectionChangesDigest(changes)
	if err != nil || digest != review.Digest {
		return fail, ErrConflict
	}
	if operation >= max(1, len(changes.Effects)) {
		return fail, ErrConflict
	}
	// Member names and availability come from this same authorized snapshot,
	// projected exactly as planning evidence projects them, for this page only.
	// The fresh re-read and runtime check below still guard the whole page.
	project := s.store.arrangementItems(scope, doc, state)
	entries := make(map[string]Entry, len(doc.Entries))
	for _, entry := range doc.Entries {
		entries[entry.ID] = entry
	}
	before, after := map[string]LogicalCollection{}, map[string]LogicalCollection{}
	for _, c := range doc.Collections {
		before[c.ID] = c
	}
	for _, c := range changes.Collections {
		after[c.ID] = c
	}
	view := CollectionReviewPage{ArrangementID: id, Token: token, CatalogRevision: doc.Revision, ExpiresAt: review.ExpiresAt, Goal: saved.Record.Draft.Goal, Unknowns: slices.Clone(saved.Record.Draft.Unknowns), Coverage: saved.Record.Coverage, Effects: []CollectionEffectView{}, Operation: operation, Offset: offset, Members: []CollectionMemberView{}}
	view.Rationale = saved.Record.Draft.Rationale
	view.Unchanged = len(saved.Record.Draft.Unchanged.IDs)
	view.NeedsReview = []CollectionReviewCaution{}
	for _, recommendation := range saved.Record.Draft.NeedsReview {
		view.NeedsReview = append(view.NeedsReview, CollectionReviewCaution{Reason: recommendation.Reason, Count: len(recommendation.Items.IDs)})
	}
	for _, effect := range changes.Effects {
		if effect.QueueID != "" {
			view.Effects = append(view.Effects, CollectionEffectView{Kind: effect.Kind, QueueID: effect.QueueID, AfterName: "Projects awaiting separate activation review", AfterMembers: effect.SelectedCount})
			continue
		}
		a, b := before[effect.CollectionID], after[effect.CollectionID]
		view.Effects = append(view.Effects, CollectionEffectView{Kind: effect.Kind, CollectionID: effect.CollectionID, BeforeName: a.Name, AfterName: b.Name, BeforeMembers: len(a.EntryIDs), AfterMembers: len(b.EntryIDs)})
	}
	if len(changes.Effects) > 0 {
		target := changes.Effects[operation].CollectionID
		includedBefore, includedAfter := map[string]bool{}, map[string]bool{}
		for _, id := range before[target].EntryIDs {
			includedBefore[id] = true
		}
		for _, id := range after[target].EntryIDs {
			includedAfter[id] = true
		}
		if changes.Effects[operation].QueueID != "" {
			for _, id := range changes.Queue.IDs {
				includedAfter[id] = true
			}
		}
		// Source IDs give stable order; names are display data, never selectors.
		ids := make([]string, 0, len(includedBefore)+len(includedAfter))
		for id := range includedBefore {
			ids = append(ids, id)
		}
		for id := range includedAfter {
			if !includedBefore[id] {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		view.Total = len(ids)
		if offset > len(ids) || (offset == len(ids) && offset != 0) {
			return fail, ErrConflict
		}
		end := min(offset+50, len(ids))
		for _, id := range ids[offset:end] {
			entry, exists := entries[id]
			if !exists {
				return fail, ErrConflict
			}
			item := project(entry)
			view.Members = append(view.Members, CollectionMemberView{ID: id, Name: item.Name, Availability: item.Availability, Before: includedBefore[id], After: includedAfter[id]})
		}
		if end < len(ids) {
			view.NextOffset = &end
		}
	} else if offset != 0 {
		return fail, ErrConflict
	}
	fresh, freshState, err := s.store.readSnapshot(scope)
	if err != nil || fresh.Revision != doc.Revision || arrangementBinding(freshState, authority) != saved.BindingDigest || !review.ExpiresAt.After(s.store.now().UTC()) {
		return fail, ErrConflict
	}
	if err := checkCollectionRuntime(ctx, authority, saved, s.resolve); err != nil {
		return fail, err
	}
	raw, err := json.Marshal(view)
	if err != nil || len(raw) > workspacearrangement.MaxSummaryBytes {
		return fail, ErrLimit
	}
	return view, ctx.Err()
}
