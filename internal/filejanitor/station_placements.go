package filejanitor

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Like the earlier Documents convention, remembered placements live only in
// the workspace's editable feedback memory. A journal proves an offer; it is
// never a saved preference. No suggestion authorizes a filesystem operation.
type placementMemory interface {
	SnapshotExact(string) (workspace.MemoryExactSnapshot, error)
	AppendManyExact(string, string, []workspace.MemoryEntry) ([]workspace.MemoryExactTarget, error)
	EditExact(string, workspace.MemoryExactTarget, *workspace.MemoryEntry) error
}

func (s *Service) SetPlacementMemory(m placementMemory) { s.placementMemory = m }

const maxPlacements = 64

var placementWord = regexp.MustCompile(`^[\p{L}]{2,40}`)
var placementExt = regexp.MustCompile(`^\.[a-z0-9]{1,12}$`)

func placementPattern(name string) (word, ext string) {
	if !safeShelfName(name) || sensitiveReviewName(name) || sensitive.ContainsSecretLikeText(name) {
		return "", ""
	}
	ext = strings.ToLower(path.Ext(name))
	if !placementExt.MatchString(ext) {
		return "", ""
	}
	stem := strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
	word = placementWord.FindString(stem)
	if word != "" {
		rest := strings.TrimPrefix(stem, word)
		if rest != "" && !strings.ContainsRune("0123456789 -_.(", []rune(rest)[0]) {
			word = ""
		}
	}
	return word, ext
}

type PlacementRow struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Count             int    `json:"count,omitempty"`
	Broken            bool   `json:"broken,omitempty"`
	word, ext, folder string
	shelf             PictureShelf
	target            workspace.MemoryExactTarget
	marker            string
}
type PlacementList struct {
	Revision string         `json:"revision"`
	Rows     []PlacementRow `json:"rows"`
}

func placementPrefix(sc shelfScope) string { return "filing-placement/" + sc.scope + "/" }
func placementText(word, ext, folder string) string {
	if word == "" {
		return fmt.Sprintf("Files of type %q → %q", ext, folder)
	}
	return fmt.Sprintf("Files named %q of type %q → %q", word, ext, folder)
}
func placementMarker(sc shelfScope, shelfKey, text string) string {
	digest, _ := setupDigest(text)
	return placementPrefix(sc) + shelfKey + "/" + digest[:24]
}
func parsePlacement(text string) (word, ext, folder string, ok bool) {
	if _, err := fmt.Sscanf(text, "Files named %q of type %q → %q", &word, &ext, &folder); err != nil {
		word = ""
		if _, err = fmt.Sscanf(text, "Files of type %q → %q", &ext, &folder); err != nil {
			return
		}
	}
	ok = placementText(word, ext, folder) == text && placementExt.MatchString(ext) && (word == "" || placementWord.FindString(word) == word) && len(text) <= workspace.MemoryEntryMaxLen
	return
}

// Resolve only named folders, never list their siblings or recreate a missing
// destination. A replaced parent or child invalidates the original shelf key.
func (s *Service) placementList(sc shelfScope) (PlacementList, error) {
	out := PlacementList{Rows: []PlacementRow{}}
	if s.placementMemory == nil {
		return out, nil
	}
	mem, err := s.placementMemory.SnapshotExact(sc.root.WorkspaceID)
	if err != nil {
		return out, err
	}
	out.Revision = mem.FileHash
	prefix := placementPrefix(sc)
	for _, line := range mem.Entries {
		if !strings.HasPrefix(line.Entry.Provenance, prefix) {
			continue
		}
		if len(out.Rows) >= maxPlacements {
			return PlacementList{}, ErrReviewBounds
		}
		row := PlacementRow{ID: line.Target.LineHash, Label: "Edited placement", Broken: true, target: line.Target, marker: line.Entry.Provenance}
		word, ext, folder, ok := parsePlacement(line.Entry.Text)
		if ok && line.Entry.Type == workspace.MemoryTypeFeedback && !sensitive.ContainsSecretLikeText(line.Entry.Text) {
			row.word, row.ext, row.folder = word, ext, folder
			row.Label = line.Entry.Text
			if sh, e := sc.exactFolder(folder); e == nil && placementMarker(sc, sh.Key, row.Label) == row.marker {
				row.Broken = false
				row.shelf = sh
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}
func (l PlacementList) match(name string) (PictureShelf, bool) {
	word, ext := placementPattern(name)
	if ext == "" {
		return PictureShelf{}, false
	}
	var match PictureShelf
	found := false
	for _, row := range l.Rows {
		if row.ext != ext || (row.word != "" && row.word != word) {
			continue
		}
		if found || row.Broken {
			return PictureShelf{}, true
		}
		found = true
		match = row.shelf
	}
	return match, found
}
func (s *Service) ReadPlacements(owner, id string) (PlacementList, error) {
	sc, err := s.shelfScope(owner, id)
	if err != nil {
		return PlacementList{}, err
	}
	return s.placementList(sc)
}

// DocumentPlacementMatches replaces the historical content-based rule once
// a workspace chooses a station method. An unavailable preference is not
// permission to fall back to an earlier inference from document contents.
func (s *Service) DocumentPlacementMatches(owner, id string, names []string) (map[string]string, bool) {
	settings, err := s.store.LoadSettings(id)
	if err != nil || settings.FilingMethod == "" {
		return nil, false
	}
	if settings.FilingMethod != MethodFolders {
		return nil, true
	}
	l, err := s.ReadPlacements(owner, id)
	if err != nil {
		return nil, true
	}
	out := map[string]string{}
	for _, name := range names {
		if sh, _ := l.match(name); sh.Key != "" {
			out[name] = sh.Name
		}
	}
	return out, true
}

func (s *Service) RemovePlacement(owner, id, rev, key string) error {
	sc, err := s.shelfScope(owner, id)
	if err != nil {
		return err
	}
	l, err := s.placementList(sc)
	if err != nil || l.Revision != rev || rev == "" || key == "" {
		return ErrReviewConflict
	}
	for _, r := range l.Rows {
		if r.ID == key {
			return s.placementMemory.EditExact(id, r.target, nil)
		}
	}
	return ErrReviewConflict
}

func (s *Service) receiptPlacementOffer(sc shelfScope, state ScanState, rec FilingRecord, stored PlacementList) PlacementList {
	out := PlacementList{Rows: []PlacementRow{}}
	if stored.Revision == "" || rec.RootID != sc.root.Generation || rec.Owner != sc.root.Owner || !rec.ShelfPicture || rec.CompletedAt.IsZero() || !rec.UndoClaimedAt.IsZero() {
		return out
	}
	rows := map[string]PlacementRow{}
	folders := map[string]PictureShelf{}
	for _, a := range filingActions(state, rec.ID) {
		if a.Result != ResultApplied || a.Operation != OperationPlace || !a.Undoable() || a.Place == nil || a.Place.RootDirID != sc.witness || a.RootID != rec.RootID {
			continue
		}
		folder := path.Dir(a.DestinationRelative)
		word, ext := placementPattern(a.SourceName)
		if ext == "" || sensitiveReviewName(folder) || sensitive.ContainsSecretLikeText(folder) {
			continue
		}
		sh, ok := folders[folder]
		if !ok {
			sh, _ = sc.exactFolder(folder)
			folders[folder] = sh
		}
		if sh.Key == "" || sh.dir.ID != a.Place.FolderID {
			continue
		}
		label := placementText(word, ext, folder)
		if len(label) > workspace.MemoryEntryMaxLen {
			continue
		}
		marker := placementMarker(sc, sh.Key, label)
		if slices.ContainsFunc(stored.Rows, func(r PlacementRow) bool { return r.marker == marker }) {
			continue
		}
		r := rows[label]
		r.Label = label
		r.ID, _ = setupDigest(marker)
		r.marker = marker
		r.Count++
		rows[label] = r
	}
	for _, r := range rows {
		out.Rows = append(out.Rows, r)
	}
	slices.SortFunc(out.Rows, func(a, b PlacementRow) int { return strings.Compare(a.Label, b.Label) })
	out.Revision, _ = setupDigest([]any{rec.ID, sc.scope, stored.Revision, out.Rows})
	return out
}

// StationPile describes just this current saved scan. It does not walk the
// root on Map load: only existing targets in confirmed Memory are verified.
type StationPile struct {
	Batch   string `json:"batch"`
	Folder  string `json:"folder"`
	Waiting int    `json:"waiting"`
	Placed  int    `json:"placed"`
}

func (s *Service) RememberedPile(owner, id, batch string) *StationPile {
	sc, err := s.shelfScope(owner, id)
	if err != nil {
		return nil
	}
	saved, err := s.placementList(sc)
	if err != nil || len(saved.Rows) == 0 {
		return nil
	}
	state, err := s.store.LoadScanState(id)
	if err != nil {
		return nil
	}
	latest, ok := latestCandidateBatch(state)
	if !ok || latest.ID != batch || latest.RootID != sc.root.Generation {
		return nil
	}
	groups, binding, root, err := s.reviewDraftSnapshot(owner, id, batch, state)
	if err != nil || root != sc.root || binding.Digest != sc.scope {
		return nil
	}
	draft := projectReviewDraft(state, batch, binding, groups)
	if draft.State == "invalid" || draft.State == "stale" {
		return nil
	}
	exclude := map[string]bool{}
	for _, ch := range draft.Choices {
		if ch.Operation == OperationTrash || ch.Operation == OperationSend {
			exclude[ch.CandidateID] = true
		}
	}
	p := StationPile{Batch: batch, Folder: DisplayFileName(path.Base(sc.root.Path))}
	for _, c := range state.CandidatesFor(batch) {
		if !proposalPending(c) || c.Decision == DecisionTrash || exclude[c.ID] || state.IsSkipped(c.Fingerprint) || workspace.ContainedEntryExcluded(c.Name) {
			continue
		}
		p.Waiting++
		if sh, _ := saved.match(c.Name); sh.Key != "" {
			p.Placed++
		}
	}
	return &p
}

func (s *Service) SaveReceiptPlacements(ctx context.Context, owner, id, filing, rev string, keys []string) error {
	if len(keys) == 0 || len(keys) > maxPlacements || rev == "" {
		return ErrInvalidAction
	}
	sc, err := s.shelfScope(owner, id)
	if err != nil {
		return err
	}
	stored, err := s.placementList(sc)
	if err != nil {
		return err
	}
	if len(stored.Rows)+len(keys) > maxPlacements {
		return ErrReviewBounds
	}
	state, err := s.store.LoadScanState(id)
	if err != nil {
		return err
	}
	rec, ok := s.filingFor(state, owner, filing)
	if !ok {
		return ErrFilingNotFound
	}
	offer := s.receiptPlacementOffer(sc, state, rec, stored)
	if offer.Revision != rev {
		return ErrReviewConflict
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var entries []workspace.MemoryEntry
	for _, r := range offer.Rows {
		if want[r.ID] {
			entries = append(entries, workspace.MemoryEntry{Type: workspace.MemoryTypeFeedback, Date: s.clock().UTC().Format("2006-01-02"), Text: r.Label, Provenance: r.marker})
		}
	}
	if len(entries) != len(keys) {
		return ErrReviewConflict
	}
	if again, e := s.shelfScope(owner, id); e != nil || again != sc || ctx.Err() != nil {
		return ErrReviewConflict
	}
	_, err = s.placementMemory.AppendManyExact(id, stored.Revision, entries)
	return err
}
