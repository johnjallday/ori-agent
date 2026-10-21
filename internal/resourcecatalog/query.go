package resourcecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Category states (contract §8.1). Host words for them are rendered by the UI.
const (
	CategoryNotInspected    = "not_inspected"
	CategoryInspected       = "inspected"
	CategoryPartial         = "partial"
	CategoryRefreshFailed   = "refresh_failed"
	CategoryUnavailable     = "unavailable"
	CategoryAccessWithdrawn = "access_withdrawn"
	// CategoryHistory has saved entries but no approval on this computer for
	// this installation: what another computer or setup saw.
	CategoryHistory = "history"
)

// Entry access, derived from this computer's grant every time it is read.
const (
	AccessCurrent   = "current"
	AccessWithdrawn = "access_withdrawn"
	AccessHistory   = "history"
)

const (
	defaultPageSize = 25
	maxPageSize     = 100
	maxQueryBytes   = 120
)

// ProviderView is the provider's availability now, apart from what was seen.
type ProviderView struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// CategoryCounts counts a category's entries by what they mean now.
type CategoryCounts struct {
	Present       int `json:"present"`
	NoLongerFound int `json:"no_longer_found"`
	Withdrawn     int `json:"withdrawn"`
	History       int `json:"history"`
}

// CategoryView is one category as the owner sees it.
type CategoryView struct {
	ID          string         `json:"id"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Kinds       []Kind         `json:"kinds"`
	Facts       []FactField    `json:"facts"`
	State       string         `json:"state"`
	InspectedAt *time.Time     `json:"inspected_at,omitempty"`
	Counts      CategoryCounts `json:"counts"`
}

// SourceView is one source on this computer: its approval and coverage. Its
// location is shown to the owner only.
type SourceView struct {
	SourceID       string     `json:"source_id"`
	Category       string     `json:"category"`
	Label          string     `json:"label"`
	Location       string     `json:"location,omitempty"`
	Access         string     `json:"access"`
	Approved       bool       `json:"approved"`
	Withdrawn      bool       `json:"withdrawn"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
	WithdrawnAt    *time.Time `json:"withdrawn_at,omitempty"`
	LastAttempt    *Attempt   `json:"last_attempt,omitempty"`
	LastCompleteAt *time.Time `json:"last_complete_at,omitempty"`
	CanContinue    bool       `json:"can_continue"`
	// Folder marks a folder the owner added; it is removed, not withdrawn.
	Folder bool `json:"folder,omitempty"`
}

// FolderCategoryView is a category the owner may add a folder to.
type FolderCategoryView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// OwnerView is the Studio's catalog page: words, states, sources and the
// inspection in flight or last finished.
type OwnerView struct {
	StudioID     string              `json:"studio_id"`
	Name         string              `json:"name"`
	Attached     bool                `json:"attached"`
	Provider     ProviderView        `json:"provider"`
	CanInspect   bool                `json:"can_inspect"`
	Approved     bool                `json:"approved"`
	Revision     int64               `json:"revision"`
	PublishedAt  *time.Time          `json:"published_at,omitempty"`
	Installation *InstallationRecord `json:"installation,omitempty"`
	Categories   []CategoryView      `json:"categories"`
	Sources      []SourceView        `json:"sources"`
	Operation    *Operation          `json:"operation,omitempty"`
	// FolderCategories are where the owner may add a folder now; CanAddFolder
	// is false once this computer holds MaxOwnerFolders.
	FolderCategories []FolderCategoryView `json:"folder_categories"`
	CanAddFolder     bool                 `json:"can_add_folder"`
}

// catalogState is everything a projection reads, loaded once.
type catalogState struct {
	ref          studioRef
	status       ProviderStatus
	observations Observations
	annotations  Annotations
	grants       Grants
	hasGrants    bool
	declaration  *Declaration
	folders      []OwnerFolder
}

func (s *Service) loadState(owner, studioID string) (catalogState, error) {
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return catalogState{}, err
	}
	observations, err := readObservations(ref.paths)
	if err != nil {
		return catalogState{}, err
	}
	annotations, err := readAnnotations(ref.paths)
	if err != nil {
		return catalogState{}, err
	}
	grants, hasGrants, err := readGrants(ref.paths)
	if err != nil {
		return catalogState{}, err
	}
	folders, err := readFolders(ref.paths)
	if err != nil {
		return catalogState{}, err
	}
	state := catalogState{ref: ref, observations: observations, annotations: annotations, grants: grants, hasGrants: hasGrants, folders: folders}
	if ref.link != nil {
		state.status = s.provider(ref, observations)
	} else {
		state.status = ProviderStatus{Reason: "detached"}
	}
	// The owner view keeps working without the plugin: the words saved with
	// the last publication stand in for the installed declaration.
	state.declaration = observations.DeclaredBy
	if state.status.Available && state.status.Declaration != nil {
		state.declaration = state.status.Declaration
	}
	return state, nil
}

// access says what one entry means on this computer now.
func (c catalogState) access(entry EntryRecord) string {
	if !c.hasGrants || entry.InstallationID != c.grants.InstallationID {
		return AccessHistory
	}
	granted := grantSource(c.grants, entry.SourceID)
	switch {
	case granted == nil:
		return AccessHistory
	case !granted.Active():
		return AccessWithdrawn
	default:
		return AccessCurrent
	}
}

// Overview is the owner's catalog page. It reads only Ori's own files and the
// provider's installed declaration; it calls no operation.
func (s *Service) Overview(owner, studioID string) (OwnerView, error) {
	state, err := s.loadState(owner, studioID)
	if err != nil {
		return OwnerView{}, err
	}
	view := OwnerView{
		StudioID: state.ref.workspace.ID, Name: state.ref.workspace.Name, Attached: state.ref.attached,
		Provider:   ProviderView{Available: state.status.Available, Reason: state.status.Reason},
		CanInspect: state.ref.attached && state.status.Available, Approved: state.hasGrants,
		Revision: state.observations.Revision, PublishedAt: state.observations.PublishedAt,
		Categories: []CategoryView{}, Sources: []SourceView{}, FolderCategories: []FolderCategoryView{},
	}
	if view.CanInspect {
		for _, category := range state.status.Declaration.FolderCategories() {
			view.FolderCategories = append(view.FolderCategories, FolderCategoryView{ID: category.ID, Label: category.Label})
		}
		view.CanAddFolder = len(view.FolderCategories) > 0 && len(state.folders) < MaxOwnerFolders
	}
	if state.hasGrants {
		for _, installation := range state.observations.Installations {
			if installation.ID == state.grants.InstallationID {
				record := installation
				view.Installation = &record
			}
		}
		for _, granted := range state.grants.Sources {
			source := SourceView{
				SourceID: granted.SourceID, Category: granted.Category, Label: granted.Label, Location: granted.Location,
				Access: granted.Access, Approved: granted.Active(), Withdrawn: !granted.Active(), WithdrawnAt: granted.WithdrawnAt,
				Folder: ownerFolder(state.folders, granted.SourceID) != nil,
			}
			approvedAt := granted.ApprovedAt
			source.ApprovedAt = &approvedAt
			if record := sourceRecord(state.observations, state.grants.InstallationID, granted.SourceID); record != nil {
				source.LastAttempt, source.LastCompleteAt = record.LastAttempt, record.LastCompleteAt
				source.CanContinue = granted.Active() && record.Cursor != ""
			}
			view.Sources = append(view.Sources, source)
		}
	}
	// A folder added on this computer but never approved is listed too, so the
	// owner sees it waiting for review.
	for _, folder := range state.folders {
		if state.hasGrants && grantSource(state.grants, folder.ID) != nil {
			continue
		}
		view.Sources = append(view.Sources, SourceView{
			SourceID: folder.ID, Category: folder.Category, Label: filepath.Base(folder.Path), Location: s.displayPath(folder.Path),
			Access: AccessFile, Folder: true,
		})
	}
	if state.declaration != nil {
		for _, category := range state.declaration.Categories {
			view.Categories = append(view.Categories, state.categoryView(category))
		}
	}
	if op := s.latestOperation(state.ref); op != nil {
		view.Operation = op
	}
	return view, nil
}

func (c catalogState) categoryView(category Category) CategoryView {
	view := CategoryView{
		ID: category.ID, Label: category.Label, Description: category.Description,
		Kinds: slices.Clone(category.Kinds), Facts: slices.Clone(category.Facts),
	}
	if view.Facts == nil {
		view.Facts = []FactField{}
	}
	for _, entry := range c.observations.Entries {
		if entry.Category != category.ID {
			continue
		}
		switch c.access(entry) {
		case AccessWithdrawn:
			view.Counts.Withdrawn++
		case AccessHistory:
			view.Counts.History++
		default:
			if entry.State == EntryPresent {
				view.Counts.Present++
			} else {
				view.Counts.NoLongerFound++
			}
		}
	}
	view.State, view.InspectedAt = c.categoryState(category.ID, view.Counts)
	return view
}

// categoryState derives the category's state from this computer's approved
// sources and their last attempts.
func (c catalogState) categoryState(categoryID string, counts CategoryCounts) (string, *time.Time) {
	var active, withdrawn []GrantSource
	if c.hasGrants {
		for _, granted := range c.grants.Sources {
			if granted.Category != categoryID {
				continue
			}
			if granted.Active() {
				active = append(active, granted)
			} else {
				withdrawn = append(withdrawn, granted)
			}
		}
	}
	if len(active) == 0 {
		switch {
		case len(withdrawn) > 0:
			return CategoryAccessWithdrawn, nil
		case counts.History > 0:
			return CategoryHistory, nil
		default:
			return CategoryNotInspected, nil
		}
	}
	var inspectedAt *time.Time
	partial, failed, attempted, everComplete := false, false, false, false
	for _, granted := range active {
		record := sourceRecord(c.observations, c.grants.InstallationID, granted.SourceID)
		if record == nil || record.LastAttempt == nil {
			continue
		}
		attempted = true
		if record.LastCompleteAt != nil {
			everComplete = true
		}
		switch record.LastAttempt.Outcome {
		case OutcomeComplete, OutcomeAbsent:
			finished := record.LastAttempt.FinishedAt
			if inspectedAt == nil || finished.Before(*inspectedAt) {
				inspectedAt = &finished
			}
		case OutcomePartial, OutcomeCancelled, OutcomeWithdrawn:
			partial = true
		default:
			failed = true
		}
	}
	switch {
	case !attempted:
		return CategoryNotInspected, nil
	case failed && everComplete:
		return CategoryRefreshFailed, inspectedAt
	case failed && !partial && inspectedAt == nil:
		return CategoryUnavailable, nil
	case partial || failed:
		return CategoryPartial, inspectedAt
	default:
		return CategoryInspected, inspectedAt
	}
}

func (s *Service) latestOperation(ref studioRef) *Operation {
	s.mu.Lock()
	if run := s.running[ref.workspace.ID]; run != nil {
		op := cloneOperation(run.op)
		s.mu.Unlock()
		return &op
	}
	s.mu.Unlock()
	operations, err := s.operations(ref)
	if err != nil || len(operations) == 0 {
		return nil
	}
	op := operations[len(operations)-1]
	return &op
}

// Query filters and pages the owner's entries.
type Query struct {
	Q             string
	Category      string
	Kind          string
	Source        string
	FavoritesOnly bool
	State         string
	Cursor        string
	Limit         int
}

// FactView is one declared fact with the entry's value, or Unknown.
type FactView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value string `json:"value"`
	Known bool   `json:"known"`
}

// EntryRow is one entry in a list. Source label and kind keep same-named
// entries apart.
type EntryRow struct {
	EntryID       string     `json:"entry_id"`
	Category      string     `json:"category"`
	CategoryLabel string     `json:"category_label"`
	Kind          string     `json:"kind"`
	KindLabel     string     `json:"kind_label"`
	Name          string     `json:"name"`
	SourceID      string     `json:"source_id"`
	SourceLabel   string     `json:"source_label"`
	Facts         []FactView `json:"facts"`
	State         string     `json:"state"`
	ObservedAt    time.Time  `json:"observed_at"`
	Favorite      bool       `json:"favorite"`
	Tags          []string   `json:"tags"`
	NoteExcerpt   string     `json:"note_excerpt,omitempty"`
	// IdentityHint is the plugin's own identity, set on an owner's list row
	// only when another entry of the same setup, source and kind has the same
	// name, so the two can be told apart. Lookups never carry it.
	IdentityHint string `json:"identity_hint,omitempty"`
}

// EntryPage is one page of rows.
type EntryPage struct {
	Rows                []EntryRow `json:"rows"`
	Total               int        `json:"total"`
	NextCursor          string     `json:"next_cursor,omitempty"`
	Revision            int64      `json:"revision"`
	AnnotationsRevision int64      `json:"annotations_revision"`
}

// Entries lists the owner's entries. A cursor is bound to both revisions and
// the query; anything changing since makes it stale.
func (s *Service) Entries(owner, studioID string, query Query) (EntryPage, error) {
	if len(query.Q) > maxQueryBytes || !utf8.ValidString(query.Q) || query.Limit < 0 || query.Limit > maxPageSize {
		return EntryPage{}, ErrInvalidRequest
	}
	if query.Limit == 0 {
		query.Limit = defaultPageSize
	}
	state, err := s.loadState(owner, studioID)
	if err != nil {
		return EntryPage{}, err
	}
	rows := state.matching(query)
	offset := 0
	digest := queryDigest(query, state.observations.Revision, state.annotations.Revision)
	if query.Cursor != "" {
		var cursorDigest string
		if parts := strings.SplitN(query.Cursor, ".", 2); len(parts) == 2 {
			offset, err = strconv.Atoi(parts[0])
			cursorDigest = parts[1]
		}
		if err != nil || offset < 0 || offset > len(rows) || cursorDigest != digest {
			return EntryPage{}, ErrConflict
		}
	}
	page := EntryPage{Total: len(rows), Revision: state.observations.Revision, AnnotationsRevision: state.annotations.Revision, Rows: []EntryRow{}}
	end := min(offset+query.Limit, len(rows))
	page.Rows = append(page.Rows, rows[offset:end]...)
	if end < len(rows) {
		page.NextCursor = fmt.Sprintf("%d.%s", end, digest)
	}
	return page, nil
}

func (c catalogState) matching(query Query) []EntryRow {
	needle := strings.ToLower(strings.TrimSpace(query.Q))
	sameName := func(entry EntryRecord) string {
		return strings.Join([]string{entry.InstallationID, entry.SourceID, entry.Kind, strings.ToLower(entry.Name)}, "\x00")
	}
	named := map[string]int{}
	for _, entry := range c.observations.Entries {
		named[sameName(entry)]++
	}
	rows := []EntryRow{}
	for _, entry := range c.observations.Entries {
		if (query.Category != "" && entry.Category != query.Category) || (query.Kind != "" && entry.Kind != query.Kind) ||
			(query.Source != "" && entry.SourceID != query.Source) {
			continue
		}
		row := c.row(entry)
		if query.FavoritesOnly && !row.Favorite {
			continue
		}
		if query.State != "" && row.State != query.State {
			continue
		}
		if needle != "" && !c.matches(entry, needle) {
			continue
		}
		if named[sameName(entry)] > 1 {
			row.IdentityHint = entry.NativeID
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)
		if left != right {
			return left < right
		}
		return rows[i].EntryID < rows[j].EntryID
	})
	return rows
}

// matches searches the observed name and the owner's own tags and note.
func (c catalogState) matches(entry EntryRecord, needle string) bool {
	if strings.Contains(strings.ToLower(entry.Name), needle) {
		return true
	}
	annotation := c.annotations.Annotations[entry.EntryID]
	for _, tag := range annotation.Tags {
		if strings.Contains(strings.ToLower(tag), needle) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(annotation.Note), needle)
}

func (c catalogState) row(entry EntryRecord) EntryRow {
	row := EntryRow{
		EntryID: entry.EntryID, Category: entry.Category, Kind: entry.Kind, Name: entry.Name, SourceID: entry.SourceID,
		ObservedAt: entry.LastObservedAt, Facts: []FactView{}, Tags: []string{},
	}
	if category, ok := c.declaration.Category(entry.Category); ok {
		row.CategoryLabel = category.Label
		for _, kind := range category.Kinds {
			if kind.ID == entry.Kind {
				row.KindLabel = kind.Label
			}
		}
		for _, field := range category.Facts {
			fact := FactView{ID: field.ID, Label: field.Label, Value: "Unknown"}
			for _, observed := range entry.Facts {
				if observed.ID == field.ID {
					fact.Value, fact.Known = observed.Value, true
				}
			}
			row.Facts = append(row.Facts, fact)
		}
	}
	if record := sourceRecord(c.observations, entry.InstallationID, entry.SourceID); record != nil {
		row.SourceLabel = record.Label
	}
	switch access := c.access(entry); access {
	case AccessCurrent:
		row.State = entry.State
	default:
		row.State = access
	}
	if annotation, ok := c.annotations.Annotations[entry.EntryID]; ok {
		row.Favorite, row.Tags = annotation.Favorite, slices.Clone(annotation.Tags)
		row.NoteExcerpt = excerpt(annotation.Note, noteExcerptBytes)
	}
	return row
}

// EntryDetail is one entry with its full note, attributed description and,
// for its owner, where its source is.
type EntryDetail struct {
	EntryRow
	Description        *Description `json:"description,omitempty"`
	ModifiedAt         *time.Time   `json:"modified_at,omitempty"`
	FirstObservedAt    time.Time    `json:"first_observed_at"`
	NoLongerFoundAt    *time.Time   `json:"no_longer_found_at,omitempty"`
	Identity           string       `json:"identity"`
	SourceLocation     string       `json:"source_location,omitempty"`
	Note               string       `json:"note"`
	AnnotationRevision int64        `json:"annotation_revision"`
	// SuggestedReplacement is one present entry that looks like this one
	// (same category, kind and name) when this one is gone, withdrawn or from
	// another setup and has notes. Moving them is the owner's choice.
	SuggestedReplacement *EntryRow `json:"suggested_replacement,omitempty"`
}

// Entry returns one entry for its owner.
func (s *Service) Entry(owner, studioID, entryID string) (EntryDetail, error) {
	state, err := s.loadState(owner, studioID)
	if err != nil {
		return EntryDetail{}, err
	}
	for _, entry := range state.observations.Entries {
		if entry.EntryID != entryID {
			continue
		}
		detail := EntryDetail{
			EntryRow: state.row(entry), Description: entry.Description, ModifiedAt: entry.ModifiedAt,
			FirstObservedAt: entry.FirstObservedAt, NoLongerFoundAt: entry.NoLongerFoundAt, Identity: entry.NativeID,
		}
		if state.hasGrants && state.grants.InstallationID == entry.InstallationID {
			if granted := grantSource(state.grants, entry.SourceID); granted != nil {
				detail.SourceLocation = granted.Location
			}
		}
		if annotation, ok := state.annotations.Annotations[entryID]; ok {
			detail.Note, detail.AnnotationRevision = annotation.Note, annotation.Revision
		}
		if replacement, ok := state.suggestedReplacement(entry); ok {
			row := state.row(replacement)
			detail.SuggestedReplacement = &row
		}
		return detail, nil
	}
	return EntryDetail{}, ErrNotFound
}

func queryDigest(query Query, revision, annotations int64) string {
	encoded := fmt.Sprintf("%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%t\x00%s\x00%d",
		revision, annotations, query.Q, query.Category, query.Kind, query.Source, query.FavoritesOnly, query.State, query.Limit)
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:8])
}

// excerpt cuts text to at most limit bytes on a rune boundary.
func excerpt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + "…"
}
