package workspacearrangement

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	MaxRecordBytes        = 1 << 20
	MaxContextRecords     = 32
	MaxContextRecordBytes = 4 << 20
)

// Record is canonical inert intent, not executable review state. Context
// ownership and originating message are stored by the canonical owning store.
// It deliberately retains no selector handles, source paths or copied labels.
// Live authority must be reacquired; decoding history cannot restore it.
type Record struct {
	Version             int               `json:"version"`
	ID                  string            `json:"id"`
	Revision            int64             `json:"revision"`
	State               string            `json:"state"`
	Origin              string            `json:"origin"`
	SourceKind          SourceKind        `json:"source_kind"`
	SourceID            string            `json:"source_id"`
	Draft               Draft             `json:"draft"`
	ItemRevisions       map[string]string `json:"item_revisions"`
	CollectionRevisions map[string]int64  `json:"collection_revisions"`
	Coverage            Coverage          `json:"coverage"`
	Provider            string            `json:"provider,omitempty"`
	Model               string            `json:"model,omitempty"`
	SkillDigest         string            `json:"skill_digest,omitempty"`
	ProviderFingerprint string            `json:"provider_fingerprint,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

// Provenance is supplied by the authenticated host. ProviderFingerprint pins
// the configured provider/model revision without storing credentials; SkillDigest
// pins verified installed guidance when one was used.
type Provenance struct {
	Origin, SourceID, Provider, Model, SkillDigest, ProviderFingerprint string
}

// NewRecord binds the resolved intent to exact item/collection revisions before
// saving. This method is host-only; never decode a browser/model Record and
// treat it as a new plan. The owning store adds authenticated context pins.
func (e *Evidence) NewRecord(ctx context.Context, draft Draft, provenance Provenance) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return Record{}, err
	}
	if draft.SourceRevision != e.revision || (e.snapshot.Kind == Attachment && provenance.SourceID != e.snapshot.Revision) {
		return Record{}, ErrStale
	}
	encoded, err := json.Marshal(draft)
	if err != nil || len(encoded) > MaxRecordBytes {
		return Record{}, ErrLimit
	}
	var owned Draft
	if json.Unmarshal(encoded, &owned) != nil {
		return Record{}, ErrInvalid
	}
	now := e.now().UTC()
	record := Record{Version: Version, ID: uuid.NewString(), Revision: 1, State: "draft", Origin: provenance.Origin,
		SourceKind: e.snapshot.Kind, SourceID: provenance.SourceID, Draft: owned,
		ItemRevisions: map[string]string{}, CollectionRevisions: map[string]int64{}, Coverage: e.snapshot.Coverage,
		Provider: provenance.Provider, Model: provenance.Model, SkillDigest: provenance.SkillDigest, ProviderFingerprint: provenance.ProviderFingerprint, CreatedAt: now, UpdatedAt: now}
	items := map[string]string{}
	for _, item := range e.snapshot.Items {
		items[item.ID] = item.Revision
	}
	for _, selection := range recordSelections(owned) {
		if selection.Handle != "" {
			return Record{}, ErrInvalid
		}
		for _, id := range selection.IDs {
			if items[id] == "" {
				return Record{}, ErrStale
			}
			record.ItemRevisions[id] = items[id]
		}
	}
	for _, op := range owned.Operations {
		if op.CollectionID == "" {
			continue
		}
		for _, collection := range e.snapshot.Collections {
			if collection.ID == op.CollectionID && collection.Revision == op.CollectionRevision {
				record.CollectionRevisions[collection.ID] = collection.Revision
			}
		}
	}
	if len(owned.NeedsReview) > 0 || len(owned.Unknowns) > 0 {
		record.State = "needs_more_evidence"
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	return record, nil
}

// NewClarificationRecord captures only the goal/source/provenance for a
// discussion-only completion. The owning store must separately identify the
// outcome as clarification, not expose this empty envelope as a model proposal.
func (e *Evidence) NewClarificationRecord(ctx context.Context, goal string, provenance Provenance) (Record, error) {
	e.mu.Lock()
	revision := e.revision
	e.mu.Unlock()
	record, err := e.NewRecord(ctx, Draft{Version: Version, SourceRevision: revision, Goal: goal}, provenance)
	if err != nil {
		return Record{}, err
	}
	record.State = "needs_more_evidence"
	return record, nil
}

// ValidateRecord checks a newly prepared record against this live source. Saved
// records require their owning store's context CAS too; this is not approval.
func (e *Evidence) ValidateRecord(ctx context.Context, record Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return err
	}
	if record.Validate() != nil || record.SourceKind != e.snapshot.Kind || record.Draft.SourceRevision != e.revision {
		return ErrStale
	}
	items := map[string]string{}
	for _, item := range e.snapshot.Items {
		items[item.ID] = item.Revision
	}
	for id, revision := range record.ItemRevisions {
		if items[id] != revision {
			return ErrStale
		}
	}
	collections := map[string]int64{}
	for _, collection := range e.snapshot.Collections {
		collections[collection.ID] = collection.Revision
	}
	for id, revision := range record.CollectionRevisions {
		if collections[id] != revision {
			return ErrStale
		}
	}
	return ctx.Err()
}

func recordSelections(draft Draft) []Selection {
	out := []Selection{draft.Unchanged}
	for _, item := range draft.NeedsReview {
		out = append(out, item.Items)
	}
	for _, op := range draft.Operations {
		out = append(out, op.Members)
	}
	return out
}

func (r Record) Validate() error {
	if r.Version != Version || !label(r.ID, 160) || r.Revision < 1 || !label(r.SourceID, 160) ||
		(r.Origin != "model" && r.Origin != "manual" && r.Origin != "rule_based") ||
		(r.State != "draft" && r.State != "needs_more_evidence" && r.State != "stale" && r.State != "cancelled") ||
		r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || len(r.ItemRevisions) > MaxSourceItems || len(r.CollectionRevisions) > 100 ||
		!label(r.Coverage.Origin, 80) || !text(r.Coverage.Note, 2000) || r.Coverage.MaxDepth < 0 || r.Coverage.MaxEntries < 0 || r.Coverage.TreeOmitted < 0 || r.Coverage.SkippedLinks < 0 ||
		!text(r.Provider, 160) || !text(r.Model, 512) {
		return ErrInvalid
	}
	if r.Origin == "model" && (r.Provider == "" || r.Model == "" || r.ProviderFingerprint == "") {
		return ErrInvalid
	}
	for _, digest := range []string{r.SkillDigest, r.ProviderFingerprint} {
		if digest == "" {
			continue
		}
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 || strings.ToLower(digest) != digest {
			return ErrInvalid
		}
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxRecordBytes {
		return ErrLimit
	}
	// Reuse the wire validator without its 100-explicit-ID/32-KiB wire limits:
	// substitute temporary validation-only handles for durable exact sets.
	var draft Draft
	raw, err := json.Marshal(r.Draft)
	if err != nil || json.Unmarshal(raw, &draft) != nil {
		return ErrInvalid
	}
	view := ValidationView{Kind: r.SourceKind, Revision: draft.SourceRevision, Items: map[string]bool{}, Handles: map[string][]string{}, Collections: r.CollectionRevisions}
	for id, revision := range r.ItemRevisions {
		if !label(id, 160) || !label(revision, 160) {
			return ErrInvalid
		}
		view.Items[id] = true
	}
	for id, revision := range r.CollectionRevisions {
		if !label(id, 160) || revision < 1 {
			return ErrInvalid
		}
	}
	used := map[string]bool{}
	freeze := func(selection *Selection) error {
		if selection.Handle != "" {
			return ErrInvalid
		}
		if len(selection.IDs) == 0 {
			return nil
		}
		for _, id := range selection.IDs {
			used[id] = true
		}
		handle := fmt.Sprintf("stored-%d", len(view.Handles))
		view.Handles[handle] = selection.IDs
		selection.IDs, selection.Handle = nil, handle
		return nil
	}
	if err := freeze(&draft.Unchanged); err != nil {
		return err
	}
	for i := range draft.NeedsReview {
		if err := freeze(&draft.NeedsReview[i].Items); err != nil {
			return err
		}
	}
	collections := map[string]bool{}
	for i := range draft.Operations {
		if err := freeze(&draft.Operations[i].Members); err != nil {
			return err
		}
		if id := draft.Operations[i].CollectionID; id != "" {
			collections[id] = true
		}
	}
	if len(used) != len(r.ItemRevisions) || len(collections) != len(r.CollectionRevisions) {
		return ErrInvalid
	}
	return draft.Validate(view)
}

// UnmarshalJSON keeps the same strictness when a record is nested in a Home
// envelope instead of decoded through the PA's dedicated record reader.
func (r *Record) UnmarshalJSON(raw []byte) error {
	decoded, err := DecodeRecord(raw)
	if err != nil {
		return err
	}
	*r = decoded
	return nil
}

func DecodeRecord(raw []byte) (Record, error) {
	if len(raw) > MaxRecordBytes || !utf8.Valid(raw) || !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return Record{}, ErrInvalid
	}
	if uniqueJSONKeys(json.NewDecoder(strings.NewReader(string(raw))), 0, true, false) != nil {
		return Record{}, ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var record Record
	type plainRecord Record // Avoid recursion through the strict nested decoder.
	if decoder.Decode((*plainRecord)(&record)) != nil || decoder.Decode(new(any)) != io.EOF || record.Validate() != nil {
		return Record{}, ErrInvalid
	}
	return record, nil
}
