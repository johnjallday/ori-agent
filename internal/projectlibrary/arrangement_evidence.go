package projectlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// ArrangementEvidence reads canonical saved catalog metadata as the ACTUAL
// bound Manager. owner is authenticated by the host, never taken from tool
// JSON. No path, root discovery, song facts or private child contents escape.
func (s *Store) ArrangementEvidence(ctx context.Context, owner string, authority ManagerAuthority) (workspacearrangement.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return workspacearrangement.Snapshot{}, err
	}
	scope, err := s.authorizeManager(authority)
	if err != nil || owner == "" || scope.OwnerUserID != owner {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrUnavailable
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return workspacearrangement.Snapshot{}, err
	}
	binding, err := json.Marshal(struct {
		Scope    Scope
		Manager  ManagerAuthority
		Revision int64
		Provider any
	}{scope, authority, state.HomeBindings.StateRevision, state.HomeProvider})
	if err != nil {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrUnavailable
	}
	out := workspacearrangement.Snapshot{Kind: workspacearrangement.HomeLibrary, Revision: strconv.FormatInt(doc.Revision, 10), Authority: string(binding),
		Coverage: workspacearrangement.Coverage{Origin: "saved_home_catalog", MaxEntries: maxEntries,
			Note: "Saved catalog observations only, not a current filesystem inspection. Source coverage may be incomplete or offline; dates are observed activity, not progress. No song-detail or private child content is included."}}
	for _, scan := range doc.Scans {
		out.Coverage.Partial = out.Coverage.Partial || scan.Status != "complete"
	}
	linked, inactive, roots := map[string]bool{}, map[string]bool{}, map[string]Root{}
	for _, id := range state.LinkedProjectIDs {
		linked[id] = true
	}
	for _, id := range state.ProjectLibraryInactiveRoots {
		inactive[id] = true
	}
	for _, root := range doc.Roots {
		roots[root.ID] = root
	}
	for _, entry := range doc.Entries {
		row := s.projectSearchRow(scope, entry, roots, linked, inactive, false)
		name := foldercontext.DisplayName(row.Name)
		if name == "" {
			name = "Unnamed project"
		}
		format := row.Format
		if format == "" {
			format = "unknown"
		}
		item := workspacearrangement.Item{ID: entry.ID, Revision: fmt.Sprintf("%d-%d", entry.Revision, entry.Fields.Revision),
			Name: name, Format: format, Connection: row.Connection, Availability: row.Availability, ObservedAt: row.LastScannedAt}
		if row.LastSavedAt != nil {
			item.ModifiedAt = row.LastSavedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		for _, observation := range entry.Observations {
			availability := observation.Availability
			if roots[observation.RootID].RevokedAt != nil || inactive[observation.RootID] {
				availability = "revoked_source"
			}
			item.Sources = append(item.Sources, workspacearrangement.ItemSource{RootID: observation.RootID, Format: observation.Format, Availability: availability})
		}
		out.Items = append(out.Items, item)
	}
	for _, collection := range doc.Collections {
		out.Collections = append(out.Collections, workspacearrangement.CollectionEvidence{ID: collection.ID, Name: collection.Name, Revision: collection.Revision, MemberIDs: append([]string(nil), collection.EntryIDs...)})
	}
	// Authorize again after store/linked-owner reads. A binding replacement or
	// provider removal during an asynchronous read cannot publish old evidence.
	freshScope, err := s.authorizeManager(authority)
	if err != nil || freshScope != scope {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrStale
	}
	fresh, freshState, err := s.readSnapshot(scope)
	if err != nil || fresh.Revision != doc.Revision || freshState.HomeBindings.StateRevision != state.HomeBindings.StateRevision ||
		(freshState.HomeProvider == nil) != (state.HomeProvider == nil) ||
		(freshState.HomeProvider != nil && *freshState.HomeProvider != *state.HomeProvider) {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrStale
	}
	return out, ctx.Err()
}
