package workspacearrangement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/google/uuid"
)

// RebindDraft is host-only: the caller must load an owner-authorized canonical
// record, never browser/model JSON. Exact old membership becomes fresh bounded
// handles, not a dump of thousands of IDs into model history. This grants no
// review/approval and refuses changed evidence rather than repairing it.
func (e *Evidence) RebindDraft(ctx context.Context, record Record) (Draft, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return Draft{}, err
	}
	if record.Validate() != nil || record.SourceKind != e.snapshot.Kind || (record.State != "draft" && record.State != "needs_more_evidence" && record.State != "awaiting_user_review") {
		return Draft{}, ErrInvalid
	}
	items := map[string]string{}
	for _, item := range e.snapshot.Items {
		items[item.ID] = item.Revision
	}
	for id, revision := range record.ItemRevisions {
		if items[id] != revision {
			return Draft{}, ErrStale
		}
	}
	collections := map[string]int64{}
	for _, collection := range e.snapshot.Collections {
		collections[collection.ID] = collection.Revision
	}
	for id, revision := range record.CollectionRevisions {
		if collections[id] != revision {
			return Draft{}, ErrStale
		}
	}
	raw, err := json.Marshal(record.Draft)
	if err != nil {
		return Draft{}, ErrInvalid
	}
	var draft Draft
	if json.Unmarshal(raw, &draft) != nil {
		return Draft{}, ErrInvalid
	}
	staged := maps.Clone(e.selections)
	freeze := func(selection *Selection) error {
		if len(selection.IDs) == 0 {
			return nil
		}
		wanted := map[string]bool{}
		for _, id := range selection.IDs {
			wanted[id] = true
		}
		ids := make([]string, 0, len(selection.IDs))
		hash := sha256.New()
		for _, item := range e.snapshot.Items {
			if wanted[item.ID] {
				ids = append(ids, item.ID)
				_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", item.ID, item.Revision)
			}
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		for _, frozen := range staged {
			if frozen.Digest == digest {
				selection.IDs = nil
				selection.Handle = frozen.Handle
				return nil
			}
		}
		if len(staged) >= 16 {
			return ErrLimit
		}
		handle := uuid.NewString()
		staged[handle] = frozenSelection{FrozenSelection: FrozenSelection{Handle: handle, Revision: e.revision, Count: len(selection.IDs), Digest: digest, ExpiresAt: e.expires}, ids: ids}
		selection.IDs = nil
		selection.Handle = handle
		return nil
	}
	if err := freeze(&draft.Unchanged); err != nil {
		return Draft{}, err
	}
	for i := range draft.NeedsReview {
		if err := freeze(&draft.NeedsReview[i].Items); err != nil {
			return Draft{}, err
		}
	}
	for i := range draft.Operations {
		if err := freeze(&draft.Operations[i].Members); err != nil {
			return Draft{}, err
		}
	}
	draft.SourceRevision = e.revision
	encoded, err := json.Marshal(draft)
	if err != nil || len(encoded) > MaxDraftBytes {
		return Draft{}, ErrLimit
	}
	if err := e.check(ctx); err != nil {
		return Draft{}, err
	}
	e.selections = staged
	for id := range record.CollectionRevisions {
		e.collections[id] = true
	}
	return draft, nil
}
