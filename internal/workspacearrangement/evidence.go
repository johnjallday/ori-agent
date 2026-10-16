package workspacearrangement

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUnavailable = errors.New("arrangement evidence is unavailable")
	ErrStale       = errors.New("arrangement evidence changed; start a new plan")
	ErrLimit       = errors.New("arrangement limit reached; narrow the selection")
)

// Item is metadata, not a grant. Source adapters must never put a path, file
// contents, child transcript or guessed progress in this projection.
type Item struct {
	ID           string       `json:"id"`
	Revision     string       `json:"revision"`
	Name         string       `json:"name"`
	Format       string       `json:"format"`
	Connection   string       `json:"connection"`
	Availability string       `json:"observed_availability"`
	Files        int          `json:"files,omitempty"`
	ObservedAt   time.Time    `json:"observed_at"`
	ModifiedAt   string       `json:"observed_modified_at,omitempty"`
	Root         bool         `json:"root,omitempty"`
	Sources      []ItemSource `json:"sources,omitempty"`
	Tree         []TreeNode   `json:"observed_tree,omitempty"`
}

// TreeNode is an observed relationship, not a selectable creator target.
type TreeNode struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// Root-specific facts travel together, so a filter cannot combine one root's
// format with another root's availability. IDs are opaque, never grant paths.
type ItemSource struct {
	RootID       string `json:"root_id"`
	Format       string `json:"format"`
	Availability string `json:"observed_availability"`
}

type Coverage struct {
	Origin       string `json:"origin"`
	Partial      bool   `json:"partial"`
	MaxDepth     int    `json:"max_depth,omitempty"`
	MaxEntries   int    `json:"max_entries,omitempty"`
	TreeOmitted  int    `json:"tree_omitted,omitempty"`
	SkippedLinks int    `json:"skipped_links,omitempty"`
	Note         string `json:"note"`
}

// CollectionEvidence is canonical metadata supplied by the owning Home, not
// another editable collection registry. MemberIDs never reach an overview.
type CollectionEvidence struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Revision  int64    `json:"revision"`
	MemberIDs []string `json:"member_ids"`
}

// Snapshot is internal input. Only query projections go to the model. Authority
// fingerprints the owner/instance/provider/skill binding and is never exposed.
type Snapshot struct {
	Kind        SourceKind           `json:"kind"`
	Revision    string               `json:"revision"`
	Coverage    Coverage             `json:"coverage"`
	Items       []Item               `json:"items"`
	Collections []CollectionEvidence `json:"collections,omitempty"`
	Authority   string               `json:"-"`
}

// EvidenceSource rereads only already-held or canonically saved metadata and
// reauthorizes the caller. It must not resolve a path or discover files.
type EvidenceSource func(context.Context) (Snapshot, error)

type Filter struct {
	SelectionHandle string `json:"selection_handle,omitempty"`
	Text            string `json:"text,omitempty"`
	Format          string `json:"format,omitempty"`
	Connection      string `json:"connection,omitempty"`
	Availability    string `json:"availability,omitempty"`
	RootID          string `json:"root_id,omitempty"`
	CollectionID    string `json:"collection_id,omitempty"`
}

type Page struct {
	Revision   string `json:"source_revision"`
	Rows       []Item `json:"rows"`
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type CollectionSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	Count    int    `json:"count"`
}

type Overview struct {
	Kind                 SourceKind          `json:"source_kind"`
	Revision             string              `json:"source_revision"`
	Coverage             Coverage            `json:"coverage"`
	Total                int                 `json:"total"`
	Formats              map[string]int      `json:"formats"`
	Connections          map[string]int      `json:"connections"`
	Availability         map[string]int      `json:"observed_availability"`
	Roots                map[string]int      `json:"roots"`
	Samples              []Item              `json:"samples"`
	SampleNotice         string              `json:"sample_notice"`
	Collections          []CollectionSummary `json:"collection_samples,omitempty"`
	TotalCollections     int                 `json:"total_collections"`
	MatchingCollections  int                 `json:"matching_collections"`
	NextCollectionCursor string              `json:"next_collection_cursor,omitempty"`
}

type FrozenSelection struct {
	Handle    string    `json:"handle,omitempty"`
	Revision  string    `json:"source_revision"`
	Count     int       `json:"count"`
	Digest    string    `json:"digest"`
	ExpiresAt time.Time `json:"expires_at"`
}

type frozenSelection struct {
	FrozenSelection
	ids []string
}

// Evidence lives for one planning turn. No filesystem resolver or mutation seam
// is present. All methods reauthorize before exposing data or resolving intent.
type Evidence struct {
	mu          sync.Mutex
	source      EvidenceSource
	snapshot    Snapshot
	revision    string
	secret      [32]byte
	issued      map[string]bool
	collections map[string]bool
	selections  map[string]frozenSelection
	expires     time.Time
	now         func() time.Time
}

func NewEvidence(ctx context.Context, source EvidenceSource) (*Evidence, error) {
	if source == nil {
		return nil, ErrUnavailable
	}
	snapshot, err := source(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := snapshotRevision(snapshot)
	if err != nil {
		return nil, err
	}
	// Clone across the adapter boundary, including nested source/member slices.
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, ErrInvalid
	}
	var owned Snapshot
	if err := json.Unmarshal(encoded, &owned); err != nil {
		return nil, ErrInvalid
	}
	owned.Authority = snapshot.Authority
	sort.Slice(owned.Items, func(i, j int) bool {
		a, b := owned.Items[i], owned.Items[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	e := &Evidence{source: source, snapshot: owned, revision: revision, now: time.Now,
		issued: map[string]bool{}, collections: map[string]bool{}, selections: map[string]frozenSelection{}, expires: time.Now().Add(SelectionTTL)}
	if _, err := rand.Read(e.secret[:]); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return e, nil
}

func snapshotRevision(s Snapshot) (string, error) {
	if (s.Kind != Attachment && s.Kind != HomeLibrary) || !label(s.Revision, 160) || s.Authority == "" ||
		len(s.Items) > MaxSourceItems || len(s.Collections) > 100 || !text(s.Coverage.Note, 2000) || !label(s.Coverage.Origin, 80) {
		return "", ErrInvalid
	}
	seen := map[string]bool{}
	for _, row := range s.Items {
		if !label(row.ID, 160) || !label(row.Revision, 160) || seen[row.ID] || row.Name == "" || !text(row.Name, 384) ||
			!label(row.Format, 80) || !label(row.Connection, 80) || !label(row.Availability, 80) || len(row.Sources) > 64 ||
			!text(row.ModifiedAt, 64) || row.Files < 0 {
			return "", ErrInvalid
		}
		seen[row.ID] = true
		if len(row.Tree) > 64 {
			return "", ErrInvalid
		}
		nodes := map[string]bool{}
		for _, node := range row.Tree {
			if !label(node.ID, 160) || nodes[node.ID] || (node.ParentID != "" && !nodes[node.ParentID]) ||
				node.Name == "" || !text(node.Name, 384) || (node.Kind != "file" && node.Kind != "folder") {
				return "", ErrInvalid
			}
			nodes[node.ID] = true
		}
		roots := map[string]bool{}
		for _, source := range row.Sources {
			if !label(source.RootID, 160) || roots[source.RootID] || !label(source.Format, 80) || !label(source.Availability, 80) {
				return "", ErrInvalid
			}
			roots[source.RootID] = true
		}
	}
	collections := map[string]bool{}
	total := 0
	for _, c := range s.Collections {
		if !label(c.ID, 160) || collections[c.ID] || !label(c.Name, 128) || c.Revision < 1 {
			return "", ErrInvalid
		}
		collections[c.ID] = true
		members := map[string]bool{}
		for _, id := range c.MemberIDs {
			if !seen[id] || members[id] {
				return "", ErrInvalid
			}
			members[id] = true
		}
		total += len(c.MemberIDs)
	}
	if total > MaxMemberships {
		return "", ErrLimit
	}
	encoded, err := json.Marshal(struct {
		Snapshot  Snapshot
		Authority string
	}{s, s.Authority})
	if err != nil || len(encoded) > 8<<20 {
		return "", ErrLimit
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (e *Evidence) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !e.now().Before(e.expires) {
		return ErrStale
	}
	fresh, err := e.source(ctx)
	if err != nil {
		return err
	}
	digest, err := snapshotRevision(fresh)
	if err != nil || digest != e.revision {
		return ErrStale
	}
	return ctx.Err()
}

func (e *Evidence) Revalidate(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.check(ctx)
}

func boundedResult(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxToolResultBytes {
		return ErrLimit
	}
	return nil
}

func cloneItem(row Item) Item {
	row.Sources = slices.Clone(row.Sources)
	row.Tree = slices.Clone(row.Tree)
	return row
}

func (e *Evidence) Overview(ctx context.Context) (Overview, error) {
	return e.OverviewPage(ctx, "", "")
}

// OverviewPage keeps collection lookup bounded too: a named collection beyond
// the first five is reachable without emitting the entire membership catalog.
func (e *Evidence) OverviewPage(ctx context.Context, collectionText, cursor string) (Overview, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return Overview{}, err
	}
	o := Overview{Kind: e.snapshot.Kind, Revision: e.revision, Coverage: e.snapshot.Coverage, Total: len(e.snapshot.Items),
		Formats: map[string]int{}, Connections: map[string]int{}, Availability: map[string]int{}, Roots: map[string]int{},
		SampleNotice: "At most five name-sorted examples, not a representative sample or the whole inventory.", TotalCollections: len(e.snapshot.Collections)}
	for _, row := range e.snapshot.Items {
		o.Formats[row.Format]++
		o.Connections[row.Connection]++
		o.Availability[row.Availability]++
		for _, source := range row.Sources {
			o.Roots[source.RootID]++
		}
		if len(o.Samples) < 5 {
			row.Tree = nil // Focused detail, not repeated on every page.
			o.Samples = append(o.Samples, cloneItem(row))
		}
	}
	collections, total, next, err := e.collectionPage(collectionText, cursor)
	if err != nil {
		return Overview{}, err
	}
	o.Collections, o.MatchingCollections, o.NextCollectionCursor = collections, total, next
	if err := boundedResult(o); err != nil {
		return Overview{}, err
	}
	for _, row := range o.Samples {
		e.issued[row.ID] = true
	}
	for _, c := range o.Collections {
		e.collections[c.ID] = true
	}
	return o, nil
}

func (e *Evidence) collectionPage(search, cursor string) ([]CollectionSummary, int, string, error) {
	if !text(search, 120) || strings.ContainsAny(search, `/\`) {
		return nil, 0, "", ErrInvalid
	}
	rows := []Item{}
	byID := map[string]CollectionEvidence{}
	for _, c := range e.snapshot.Collections {
		if strings.Contains(strings.ToLower(c.Name), strings.ToLower(search)) {
			rows = append(rows, Item{ID: c.ID, Name: c.Name})
			byID[c.ID] = c
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == rows[j].Name {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Name < rows[j].Name
	})
	filter := Filter{Text: search}
	start, err := e.pageOffset("collections", cursor, filter, rows)
	if err != nil {
		return nil, 0, "", err
	}
	end := min(start+5, len(rows))
	out := make([]CollectionSummary, 0, end-start)
	for _, row := range rows[start:end] {
		c := byID[row.ID]
		out = append(out, CollectionSummary{ID: c.ID, Name: c.Name, Revision: c.Revision, Count: len(c.MemberIDs)})
	}
	next := ""
	if end < len(rows) {
		next = e.pageCursor("collections", filter, end, rows[end-1].ID)
	}
	return out, len(rows), next, nil
}

func (e *Evidence) matching(filter Filter) ([]Item, error) {
	if !text(filter.Text, 120) || strings.ContainsAny(filter.Text, `/\`) {
		return nil, ErrInvalid
	}
	for _, value := range []string{filter.Format, filter.Connection, filter.Availability, filter.RootID, filter.CollectionID, filter.SelectionHandle} {
		if value != "" && !label(value, 160) {
			return nil, ErrInvalid
		}
	}
	var selected map[string]bool
	if filter.SelectionHandle != "" {
		frozen, ok := e.selections[filter.SelectionHandle]
		if !ok {
			return nil, ErrInvalid
		}
		selected = map[string]bool{}
		for _, id := range frozen.ids {
			selected[id] = true
		}
	}
	var collection map[string]bool
	if filter.CollectionID != "" {
		if !e.collections[filter.CollectionID] {
			return nil, ErrInvalid
		}
		collection = map[string]bool{}
		for _, c := range e.snapshot.Collections {
			if c.ID == filter.CollectionID {
				for _, id := range c.MemberIDs {
					collection[id] = true
				}
			}
		}
	}
	rootKnown := filter.RootID == ""
	rows := []Item{}
	for _, row := range e.snapshot.Items {
		projected := row
		if filter.RootID != "" {
			matched := false
			for _, source := range row.Sources {
				if source.RootID == filter.RootID {
					rootKnown, matched = true, true
					projected.Format, projected.Availability = source.Format, source.Availability
					projected.Sources = []ItemSource{source}
				}
			}
			if !matched {
				continue
			}
		}
		if (filter.Text != "" && !strings.Contains(strings.ToLower(row.Name), strings.ToLower(filter.Text))) ||
			(filter.Format != "" && filter.Format != projected.Format) || (filter.Connection != "" && filter.Connection != row.Connection) ||
			(filter.Availability != "" && filter.Availability != projected.Availability) || (collection != nil && !collection[row.ID]) || (selected != nil && !selected[row.ID]) {
			continue
		}
		rows = append(rows, projected)
	}
	if !rootKnown {
		return nil, ErrInvalid
	}
	return rows, nil
}

type evidenceCursor struct {
	Kind     string `json:"kind"`
	Filter   Filter `json:"filter"`
	Offset   int    `json:"offset"`
	LastID   string `json:"last_id"`
	Revision string `json:"revision"`
}

func (e *Evidence) cursor(filter Filter, offset int, lastID string) string {
	return e.pageCursor("items", filter, offset, lastID)
}

func (e *Evidence) pageCursor(kind string, filter Filter, offset int, lastID string) string {
	encoded, _ := json.Marshal(evidenceCursor{Kind: kind, Filter: filter, Offset: offset, LastID: lastID, Revision: e.revision}) // Fixed JSON-only fields.
	mac := hmac.New(sha256.New, e.secret[:])
	_, _ = mac.Write(encoded)
	return base64.RawURLEncoding.EncodeToString(encoded) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (e *Evidence) offset(cursor string, filter Filter, rows []Item) (int, error) {
	return e.pageOffset("items", cursor, filter, rows)
}

func (e *Evidence) pageOffset(kind, cursor string, filter Filter, rows []Item) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	parts := strings.Split(cursor, ".")
	if len(cursor) > 8192 || len(parts) != 2 {
		return 0, ErrStale
	}
	encoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, ErrStale
	}
	mac := hmac.New(sha256.New, e.secret[:])
	_, _ = mac.Write(encoded)
	signature, err := hex.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac.Sum(nil), signature) {
		return 0, ErrStale
	}
	var decoded evidenceCursor
	if json.Unmarshal(encoded, &decoded) != nil || decoded.Kind != kind || decoded.Filter != filter || decoded.Revision != e.revision ||
		decoded.Offset < 1 || decoded.Offset > len(rows) || rows[decoded.Offset-1].ID != decoded.LastID {
		return 0, ErrStale
	}
	return decoded.Offset, nil
}

func (e *Evidence) Search(ctx context.Context, filter Filter, cursor string, size int) (Page, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return Page{}, err
	}
	if size == 0 {
		size = ModelPageSize
	}
	if size < 1 || size > MaxPageSize {
		return Page{}, ErrLimit
	}
	rows, err := e.matching(filter)
	if err != nil {
		return Page{}, err
	}
	start, err := e.offset(cursor, filter, rows)
	if err != nil || start > len(rows) {
		return Page{}, ErrStale
	}
	end := min(start+size, len(rows))
	page := Page{Revision: e.revision, Total: len(rows), Rows: make([]Item, 0, end-start)}
	for _, row := range rows[start:end] {
		row.Tree = nil
		page.Rows = append(page.Rows, cloneItem(row))
	}
	if end < len(rows) {
		page.NextCursor = e.cursor(filter, end, rows[end-1].ID)
	}
	if err := boundedResult(page); err != nil {
		return Page{}, err
	}
	for _, row := range page.Rows {
		e.issued[row.ID] = true
	}
	return page, nil
}

func (e *Evidence) Detail(ctx context.Context, id string) (Item, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return Item{}, err
	}
	if !e.issued[id] {
		return Item{}, ErrInvalid
	}
	for _, row := range e.snapshot.Items {
		if row.ID == id {
			if err := boundedResult(row); err != nil {
				return Item{}, err
			}
			return cloneItem(row), nil
		}
	}
	return Item{}, ErrInvalid
}

func (e *Evidence) Select(ctx context.Context, filter Filter) (FrozenSelection, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return FrozenSelection{}, err
	}
	rows, err := e.matching(filter)
	if err != nil {
		return FrozenSelection{}, err
	}
	hash := sha256.New()
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", row.ID, row.Revision)
		ids = append(ids, row.ID)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	for _, selection := range e.selections {
		if selection.Digest == digest {
			return selection.FrozenSelection, nil
		}
	}
	selection := FrozenSelection{Revision: e.revision, Count: len(rows), Digest: digest, ExpiresAt: e.expires}
	if len(rows) == 0 {
		return selection, nil
	}
	if len(e.selections) >= 16 {
		return FrozenSelection{}, ErrLimit
	}
	selection.Handle = uuid.NewString()
	e.selections[selection.Handle] = frozenSelection{FrozenSelection: selection, ids: ids}
	return selection, nil
}

// Resolve validates terminal intent and replaces ephemeral handles with exact
// IDs for canonical persistence. The resolved draft is not model input; its
// membership may exceed the wire draft's explicit-ID/byte limits.
func (e *Evidence) Resolve(ctx context.Context, raw string) (Draft, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(ctx); err != nil {
		return Draft{}, err
	}
	view := ValidationView{Kind: e.snapshot.Kind, Revision: e.revision, Items: map[string]bool{}, Handles: map[string][]string{}, Collections: map[string]int64{}}
	for id := range e.issued {
		view.Items[id] = true
	}
	for handle, selection := range e.selections {
		view.Handles[handle] = selection.ids
		for _, id := range selection.ids {
			view.Items[id] = true
		}
	}
	for _, c := range e.snapshot.Collections {
		if e.collections[c.ID] {
			view.Collections[c.ID] = c.Revision
		}
	}
	draft, err := DecodeDraft(raw, view)
	if err != nil {
		return Draft{}, err
	}
	resolve := func(selection *Selection) error {
		for _, id := range selection.IDs {
			if !e.issued[id] {
				return ErrInvalid
			}
		}
		ids, err := view.members(*selection)
		if err != nil {
			return err
		}
		selection.IDs, selection.Handle = slices.Clone(ids), ""
		return nil
	}
	if err := resolve(&draft.Unchanged); err != nil {
		return Draft{}, err
	}
	for i := range draft.NeedsReview {
		if err := resolve(&draft.NeedsReview[i].Items); err != nil {
			return Draft{}, err
		}
	}
	for i := range draft.Operations {
		if err := resolve(&draft.Operations[i].Members); err != nil {
			return Draft{}, err
		}
	}
	return draft, nil
}
