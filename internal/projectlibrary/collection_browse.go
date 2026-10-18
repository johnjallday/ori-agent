package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

type CollectionCard struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Revision      int64  `json:"revision"`
	Members       int    `json:"members"`
	ArrangementID string `json:"arrangement_id,omitempty"`
}
type CollectionCatalogPage struct {
	Collections []CollectionCard `json:"collections"`
	ContextPin  string           `json:"context_pin"`
	Revision    int64            `json:"revision"`
	Offset      int              `json:"offset"`
	Total       int              `json:"total"`
	NextOffset  *int             `json:"next_offset,omitempty"`
}
type CollectionCatalogMember struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Format       string     `json:"format"`
	Availability string     `json:"last_observed_availability"`
	ObservedAt   *time.Time `json:"observed_at,omitempty"`
}
type CollectionCatalogMembers struct {
	Collection CollectionCard            `json:"collection"`
	ContextPin string                    `json:"context_pin"`
	Revision   int64                     `json:"revision"`
	Offset     int                       `json:"offset"`
	Total      int                       `json:"total"`
	Members    []CollectionCatalogMember `json:"members"`
	NextOffset *int                      `json:"next_offset,omitempty"`
}

func collectionCard(c LogicalCollection) CollectionCard {
	return CollectionCard{ID: c.ID, Name: c.Name, Revision: c.Revision, Members: len(c.EntryIDs), ArrangementID: c.ArrangementID}
}

// A comparison pin, not a credential, cursor capability or review token. Scope
// is authenticated independently on every read, even after provider loss.
func collectionCatalogPin(scope Scope, doc Document, state *workspace.AssistantProgramState) string {
	raw, _ := json.Marshal(struct {
		Scope         Scope
		Revision      int64
		StateRevision int64
		Inactive      []string
		Available     bool
	}{scope, doc.Revision, state.StateRevision, state.ProjectLibraryInactiveRoots, state.PluginAvailable})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (s *Store) checkCollectionCatalog(ctx context.Context, scope Scope, pin string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh, state, err := s.readSnapshot(scope)
	if err != nil {
		return err
	}
	if collectionCatalogPin(scope, fresh, state) != pin {
		return ErrConflict
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > workspacearrangement.MaxSummaryBytes {
		return ErrLimit
	}
	return ctx.Err()
}

// CollectionCatalog reads canonical metadata only. No initialization, migration,
// provider/Manager resolution or model call occurs. At most 20 cards escape.
func (s *Store) CollectionCatalog(ctx context.Context, scope Scope, pin string, offset int) (CollectionCatalogPage, error) {
	fail := CollectionCatalogPage{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	if offset < 0 || offset > 100 || offset%20 != 0 || (pin != "" && !validDigest(pin)) || (offset > 0 && pin == "") {
		return fail, ErrConflict
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return fail, err
	}
	current := collectionCatalogPin(scope, doc, state)
	if pin != "" && pin != current {
		return fail, ErrConflict
	}
	if offset > len(doc.Collections) || (offset == len(doc.Collections) && offset != 0) {
		return fail, ErrConflict
	}
	collections := slices.Clone(doc.Collections)
	slices.SortFunc(collections, func(a, b LogicalCollection) int {
		if comparison := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); comparison != 0 {
			return comparison
		}
		return strings.Compare(a.ID, b.ID)
	})
	end := min(offset+20, len(collections))
	page := CollectionCatalogPage{Collections: []CollectionCard{}, ContextPin: current, Revision: doc.Revision, Offset: offset, Total: len(collections)}
	for _, c := range collections[offset:end] {
		page.Collections = append(page.Collections, collectionCard(c))
	}
	if end < len(collections) {
		page.NextOffset = &end
	}
	if err := s.checkCollectionCatalog(ctx, scope, current, page); err != nil {
		return fail, err
	}
	return page, nil
}

// CollectionMembers projects only names from the saved catalog, not private
// child workspace state, paths, song facts, grants or executable open actions.
// Paging must retain the current catalog pin, including for an empty collection.
func (s *Store) CollectionMembers(ctx context.Context, scope Scope, id, pin string, offset int) (CollectionCatalogMembers, error) {
	fail := CollectionCatalogMembers{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	if !collectionLabel(id, 160) || !validDigest(pin) || offset < 0 || offset > maxEntries || offset%50 != 0 {
		return fail, ErrConflict
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return fail, err
	}
	if collectionCatalogPin(scope, doc, state) != pin {
		return fail, ErrConflict
	}
	index := slices.IndexFunc(doc.Collections, func(c LogicalCollection) bool { return c.ID == id })
	if index < 0 {
		return fail, ErrUnavailable
	}
	collection := doc.Collections[index]
	ids := slices.Clone(collection.EntryIDs)
	slices.Sort(ids)
	if offset > len(ids) || (offset == len(ids) && offset != 0) {
		return fail, ErrConflict
	}
	roots, inactive := map[string]Root{}, map[string]bool{}
	for _, root := range doc.Roots {
		roots[root.ID] = root
	}
	for _, id := range state.ProjectLibraryInactiveRoots {
		inactive[id] = true
	}
	entries := map[string]Entry{}
	for _, entry := range doc.Entries {
		entries[entry.ID] = entry
	}
	end := min(offset+50, len(ids))
	page := CollectionCatalogMembers{Collection: collectionCard(collection), ContextPin: pin, Revision: doc.Revision, Offset: offset, Total: len(ids), Members: []CollectionCatalogMember{}}
	for _, id := range ids[offset:end] {
		entry, ok := entries[id]
		if !ok {
			return fail, ErrCorrupt
		}
		// An empty linked set deliberately prevents child workspace lookups.
		row := s.projectSearchRow(scope, entry, roots, nil, inactive, false)
		name := foldercontext.DisplayName(row.Name)
		if name == "" {
			name = "Unnamed project"
		}
		member := CollectionCatalogMember{ID: entry.ID, Name: name, Format: row.Format, Availability: row.Availability}
		if !row.LastScannedAt.IsZero() {
			at := row.LastScannedAt
			member.ObservedAt = &at
		}
		page.Members = append(page.Members, member)
	}
	if end < len(ids) {
		page.NextOffset = &end
	}
	if err := s.checkCollectionCatalog(ctx, scope, pin, page); err != nil {
		return fail, err
	}
	return page, nil
}
