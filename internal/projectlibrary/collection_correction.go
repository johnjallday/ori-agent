package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// CollectionCorrection records the latest owner correction without retaining
// old names or excluded IDs that Forget would otherwise have to recover/redact.
type CollectionCorrection struct {
	Revision  int64     `json:"revision"`
	Operation int       `json:"operation"`
	Renamed   bool      `json:"renamed"`
	Excluded  int       `json:"excluded"`
	Added     int       `json:"added,omitempty"`
	At        time.Time `json:"at"`
	// Converted is the owner's action conversion: catalog_only (an activation
	// selection removed) or select_activation (a collection's catalog-only
	// members also selected for independent activation review).
	Converted string `json:"converted,omitempty"`
	// Resolved says an unresolved question was settled; Released counts the
	// explicit global unchanged items the owner released. Counts/flags only.
	Resolved bool `json:"resolved,omitempty"`
	Released int  `json:"released,omitempty"`
}

// Owner action conversions between catalog-only and activation intent.
const (
	CorrectionCatalogOnly      = "catalog_only"
	CorrectionSelectActivation = "select_activation"
)

// Resolutions of one unresolved question.
const (
	ResolutionUnchanged = "unchanged"
	ResolutionDismiss   = "dismiss"
)

type CollectionCorrectionRequest struct {
	ReviewToken string   `json:"review_token"`
	Operation   int      `json:"operation"`
	Name        *string  `json:"name,omitempty"`
	ExcludeIDs  []string `json:"exclude_ids"`
	AddIDs      []string `json:"add_ids,omitempty"`
	// Exactly one structural edit, never combined with member edits.
	Convert          string `json:"convert,omitempty"`
	ResolveQuestion  *int   `json:"resolve_question,omitempty"`
	Resolution       string `json:"resolution,omitempty"`
	ReleaseUnchanged bool   `json:"release_unchanged,omitempty"`
}

func (in CollectionCorrectionRequest) structural() (bool, bool) {
	kinds := 0
	if in.Convert != "" {
		kinds++
	}
	if in.ResolveQuestion != nil || in.Resolution != "" {
		kinds++
	}
	if in.ReleaseUnchanged {
		kinds++
	}
	members := in.Name != nil || len(in.ExcludeIDs) > 0 || len(in.AddIDs) > 0
	valid := (kinds == 1 && !members) || (kinds == 0 && members)
	switch {
	case in.Convert != "" && in.Convert != CorrectionCatalogOnly && in.Convert != CorrectionSelectActivation:
		valid = false
	case (in.ResolveQuestion == nil) != (in.Resolution == ""):
		valid = false
	case in.Resolution != "" && in.Resolution != ResolutionUnchanged && in.Resolution != ResolutionDismiss:
		valid = false
	}
	return kinds == 1, valid
}

func validCollectionCorrection(saved Arrangement) bool {
	c := saved.CollectionCorrection
	return c == nil || (saved.outcomeKind() == "proposal" && c.Revision >= 2 && c.Revision <= saved.Record.Revision &&
		c.Operation >= 0 && c.Operation < workspacearrangement.MaxOperations && c.Excluded >= 0 && c.Excluded <= 50 &&
		c.Added >= 0 && c.Added <= 50 && c.Released >= 0 && c.Released <= workspacearrangement.MaxSourceItems &&
		(c.Converted == "" || c.Converted == CorrectionCatalogOnly || c.Converted == CorrectionSelectActivation) &&
		(c.Renamed || c.Excluded > 0 || c.Added > 0 || c.Converted != "" || c.Resolved || c.Released > 0) &&
		!c.At.Before(saved.Record.CreatedAt) && !c.At.After(saved.Record.UpdatedAt))
}

// Correct edits one canonical collection intention using saved catalog IDs, not compiled
// effects. It invalidates prior approval, increments the proposal revision and
// requires a new review. It never calls a model or changes collections.
func (s *CollectionReviewService) Correct(ctx context.Context, owner, homeID, id string, in CollectionCorrectionRequest) (*ArrangementHistory, error) {
	structural, valid := in.structural()
	if !collectionLabel(in.ReviewToken, 160) || in.Operation < 0 || in.Operation >= workspacearrangement.MaxOperations || len(in.ExcludeIDs) > 50 || len(in.AddIDs) > 50 || !valid {
		return nil, ErrConflict
	}
	// Reuse the full review reauthorization and digest boundary, not a history ref.
	page, err := s.Page(ctx, owner, homeID, id, in.ReviewToken, in.Operation, 0)
	if err != nil {
		return nil, err
	}
	authority, err := s.authority(owner, homeID)
	if err != nil {
		return nil, err
	}
	scope, err := s.store.authorizeCollectionOwner(owner, authority)
	if err != nil {
		return nil, err
	}
	doc, state, err := s.store.readSnapshot(scope)
	if err != nil {
		return nil, err
	}
	if doc.Revision != page.CatalogRevision {
		return nil, ErrConflict
	}
	i := arrangementIndex(doc, id)
	if i < 0 {
		return nil, ErrConflict
	}
	saved := doc.Arrangements[i]
	if err := checkCollectionRuntime(ctx, authority, saved, s.resolve); err != nil {
		return nil, err
	}
	pin := arrangementBinding(state, authority)
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, ErrConflict
	}
	digest := sha256.Sum256(raw)
	at := s.store.now().UTC()
	_, _, err = s.store.mutateWithHomePolicy(scope, doc.Revision, operation{key: newID(), action: "correct_collection_arrangement", digest: hex.EncodeToString(digest[:])},
		func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return ctx.Err() == nil && boundManager(state, home, authority) && arrangementBinding(state, authority) == pin
		}, func(current *Document) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			index := arrangementIndex(*current, id)
			if index < 0 {
				return "", ErrConflict
			}
			target := &current.Arrangements[index]
			review, ok := findReview(*current, in.ReviewToken, collectionReviewAction)
			if !ok || review.TargetID != id || review.Collection == nil || review.ConsumedAt != nil || review.Revision != current.Revision || !review.ExpiresAt.After(s.store.now().UTC()) || target.Record.State != "awaiting_user_review" || review.Collection.ArrangementRevision != target.Record.Revision || target.BindingDigest != pin {
				return "", ErrConflict
			}
			changes, err := compileCollectionChanges(*current, id, review.Collection.PreparedAt)
			if err != nil {
				return "", err
			}
			actual, err := collectionChangesDigest(changes)
			if err != nil || actual != review.Digest {
				return "", ErrConflict
			}
			record := &target.Record
			// Resolving a question or releasing unchanged items needs no intention.
			needsOperation := !structural || in.Convert != ""
			if (needsOperation && in.Operation >= len(record.Draft.Operations)) || at.Before(record.UpdatedAt) {
				return "", ErrConflict
			}
			entries := make(map[string]Entry, len(current.Entries))
			for _, entry := range current.Entries {
				entries[entry.ID] = entry
			}
			renamed, removed, added := false, map[string]bool{}, map[string]bool{}
			converted, resolved, released := "", false, 0
			if structural {
				var err error
				converted, resolved, released, err = correctStructure(record, in, entries)
				if err != nil {
					return "", err
				}
			} else {
				op := &record.Draft.Operations[in.Operation]
				if in.Name != nil {
					if (op.Kind != workspacearrangement.CreateCollection && op.Kind != workspacearrangement.RenameCollection) || !collectionLabel(*in.Name, 128) {
						return "", ErrConflict
					}
					renamed = op.Name != *in.Name
					op.Name = *in.Name
				}
				if (len(in.ExcludeIDs) > 0 || len(in.AddIDs) > 0) && op.Kind != workspacearrangement.CreateCollection && op.Kind != workspacearrangement.SetMembers && op.Kind != workspacearrangement.SelectActivation {
					return "", ErrConflict
				}
				for _, entryID := range in.ExcludeIDs {
					if removed[entryID] || !slices.Contains(op.Members.IDs, entryID) {
						return "", ErrConflict
					}
					removed[entryID] = true
				}
				blocked := collectionCorrectionBlocked(*record)
				if len(in.AddIDs) > 0 && record.ItemRevisions == nil {
					record.ItemRevisions = map[string]string{}
				}
				for _, entryID := range in.AddIDs {
					entry, exists := entries[entryID]
					if !exists || added[entryID] || blocked[entryID] || removed[entryID] || slices.Contains(op.Members.IDs, entryID) || (op.Kind == workspacearrangement.SelectActivation && entry.Link != nil) {
						return "", ErrConflict
					}
					added[entryID] = true
					record.ItemRevisions[entryID] = fmt.Sprintf("%d-%d", entry.Revision, entry.Fields.Revision)
				}
				if !renamed && len(removed) == 0 && len(added) == 0 {
					return "", ErrConflict
				}
				op.Members.IDs = slices.DeleteFunc(op.Members.IDs, func(id string) bool { return removed[id] })
				op.Members.IDs = append(op.Members.IDs, in.AddIDs...)
				slices.Sort(op.Members.IDs)
			}
			// Preserve revision pins needed by other overlapping intentions/exclusions.
			used := map[string]bool{}
			keep := func(selection workspacearrangement.Selection) {
				for _, id := range selection.IDs {
					used[id] = true
				}
			}
			keep(record.Draft.Unchanged)
			for _, item := range record.Draft.NeedsReview {
				keep(item.Items)
			}
			for _, operation := range record.Draft.Operations {
				keep(operation.Members)
			}
			for id := range record.ItemRevisions {
				if !used[id] {
					delete(record.ItemRevisions, id)
				}
			}
			record.Revision++
			record.UpdatedAt = at
			record.State = "draft"
			if len(record.Draft.Unknowns) > 0 || len(record.Draft.NeedsReview) > 0 {
				record.State = "needs_more_evidence"
			}
			target.CollectionCorrection = &CollectionCorrection{Revision: record.Revision, Operation: in.Operation, Renamed: renamed, Excluded: len(removed), Added: len(added),
				Converted: converted, Resolved: resolved, Released: released, At: at}
			if record.Validate() != nil || !validCollectionCorrection(*target) {
				return "", ErrConflict
			}
			// Compile against the old envelope pin first. Updating it happens only with
			// the successful CAS; no old review can authorize this new revision.
			if _, err := compileCollectionChanges(*current, id, at); err != nil {
				return "", err
			}
			target.CatalogRevision = current.Revision + 1
			if _, err := arrangementHistory(*target, -1); err != nil {
				return "", err
			}
			return id, nil
		})
	if err != nil {
		return nil, err
	}
	return s.store.ReadArrangementHistory(ctx, scope, id)
}

// correctStructure applies one owner structural edit to canonical intent. It
// changes no collection, queue or project; the caller saves a new revision that
// needs a fresh review. Every refusal leaves the record untouched.
func correctStructure(record *workspacearrangement.Record, in CollectionCorrectionRequest, entries map[string]Entry) (string, bool, int, error) {
	draft := &record.Draft
	switch {
	case in.Convert == CorrectionCatalogOnly:
		// Removing the activation selection leaves its projects catalog-only:
		// nothing is queued, and collection memberships are unchanged.
		// A proposal whose only intention is that selection would be left with
		// nothing to review or apply; declining it says that honestly instead.
		if draft.Operations[in.Operation].Kind != workspacearrangement.SelectActivation || len(draft.Operations) == 1 {
			return "", false, 0, ErrConflict
		}
		draft.Operations = slices.Delete(draft.Operations, in.Operation, in.Operation+1)
		return in.Convert, false, 0, nil
	case in.Convert == CorrectionSelectActivation:
		// A collection's current catalog-only members are also offered for
		// independent activation review. Linked entries are already projects.
		source := draft.Operations[in.Operation]
		if source.Kind != workspacearrangement.CreateCollection && source.Kind != workspacearrangement.SetMembers || len(draft.Operations) >= workspacearrangement.MaxOperations {
			return "", false, 0, ErrConflict
		}
		for _, op := range draft.Operations {
			if op.Kind == workspacearrangement.SelectActivation || op.Kind == workspacearrangement.CreateGroup {
				return "", false, 0, ErrConflict
			}
		}
		ids := []string{}
		for _, id := range source.Members.IDs {
			if entry, ok := entries[id]; ok && entry.Link == nil {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 || len(ids) > workspacearrangement.MaxActivationItems {
			return "", false, 0, ErrConflict
		}
		slices.Sort(ids)
		draft.Operations = append(draft.Operations, workspacearrangement.Operation{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: ids}})
		return in.Convert, false, 0, nil
	case in.ResolveQuestion != nil:
		index := *in.ResolveQuestion
		if index < 0 || index >= len(draft.NeedsReview) {
			return "", false, 0, ErrConflict
		}
		if in.Resolution == ResolutionUnchanged {
			// The question's items become explicitly unchanged in this proposal.
			ids := append(append([]string(nil), draft.Unchanged.IDs...), draft.NeedsReview[index].Items.IDs...)
			slices.Sort(ids)
			draft.Unchanged.IDs = slices.Compact(ids)
		}
		draft.NeedsReview = slices.Delete(draft.NeedsReview, index, index+1)
		return "", true, 0, nil
	case in.ReleaseUnchanged:
		// Released items stay unchanged unless a later correction adds them.
		released := len(draft.Unchanged.IDs)
		if released == 0 {
			return "", false, 0, ErrConflict
		}
		draft.Unchanged.IDs = nil
		return "", false, released, nil
	}
	return "", false, 0, ErrConflict
}
