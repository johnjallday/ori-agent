package filejanitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ShelfPicture is a read-only view of a saved scan. Its keys select things
// the host can find again, not paths or permission to read or move a file.
// Only one level of folders is listed at a time. No model is needed.
type ShelfPicture struct {
	Folder    string         `json:"folder"`
	Batch     string         `json:"batch"`
	Revision  string         `json:"revision"`
	Supported bool           `json:"supported"`
	Partial   bool           `json:"partial,omitempty"`
	Folders   []PictureShelf `json:"folders"`
	Files     []ShelfFile    `json:"files"`
}
type PictureShelf struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	dir  DirRef
}
type ShelfFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Suggested string `json:"suggested,omitempty"`
}

type shelfScope struct {
	root                      ManagedRoot
	witness, scope, filedName string
}

func shelfWitness(generation, id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(generation + "\x00" + id))
	return hex.EncodeToString(sum[:])
}

func (s *Service) shelfScope(owner, id string) (shelfScope, error) {
	var no shelfScope
	install, installed, err := s.reviewInstall(owner, id)
	if err != nil || !installed {
		return no, ErrReviewUnavailable
	}
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return no, ErrReviewUnavailable
	}
	settings, err := s.requireConfigured(id)
	if err != nil {
		return no, ErrReviewUnavailable
	}
	proof := settings.ApprovedReference
	if proof == nil || proof.RootID != root.Generation || proof.ReferenceID != root.DirectoryReferenceID || proof.Witness == "" {
		return no, ErrReviewUnavailable
	}
	resolved, err := s.scannerFor().ResolveRoot(settings)
	if err != nil || resolved != root.Path {
		return no, ErrReviewUnavailable
	}
	info, err := os.Lstat(resolved) // #nosec G304 G703 -- verified canonical managed root, identity check only
	if err != nil || !info.IsDir() || shelfWitness(root.Generation, platformFileID(info)) != proof.Witness {
		return no, ErrReviewUnavailable
	}
	return shelfScope{root: root, witness: platformFileID(info), scope: reviewScopeDigest(root, install), filedName: settings.FilingRootName}, nil
}

func (s shelfScope) folders(parent DirRef) ([]PictureShelf, bool, error) {
	list, err := workspace.ListWitnessedPage(s.root.Path, parent.Rel, s.witness, parent.ID)
	if err != nil {
		return nil, false, ErrReviewUnavailable
	}
	out := []PictureShelf{}
	for _, entry := range list.Entries {
		if entry.Kind != "folder" || entry.Version.Identity == "" || !sameDevice(s.witness, entry.Version.Identity) || entry.Version.Placeholder || !safeShelfName(entry.Name) || (parent.Rel == "" && strings.EqualFold(entry.Name, s.filedName)) {
			continue
		}
		ref := DirRef{Rel: path.Join(parent.Rel, entry.Name), ID: entry.Version.Identity}
		key, err := setupDigest([]string{"filing-shelf-v1", s.scope, s.witness, parent.ID, ref.Rel, ref.ID})
		if err != nil {
			return nil, false, err
		}
		out = append(out, PictureShelf{Key: key, Name: ref.Rel, dir: ref})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, list.Truncated, nil
}

func safeShelfName(name string) bool {
	return ValidateFileName(name) == nil && name == DisplayFileName(name) && !workspace.ContainedEntryExcluded(name) && !folderdigest.IsSkippedFolder(name)
}

// Suggest only when the name spells a unique shelf. A tie is not a vote;
// sensitive-looking and unrecognisable filenames start in the loose tray.
func suggestedShelf(name string, folders []PictureShelf) string {
	if sensitiveReviewName(name) || name != DisplayFileName(name) {
		return ""
	}
	words := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	tokens := map[string]bool{}
	for _, word := range words(strings.TrimSuffix(name, path.Ext(name))) {
		tokens[strings.TrimSuffix(word, "s")] = true
	}
	match := ""
	for _, folder := range folders {
		parts := words(path.Base(folder.Name))
		if len(parts) != 1 {
			continue
		}
		word := strings.TrimSuffix(parts[0], "s")
		if len(word) < 3 || !tokens[word] {
			continue
		}
		if match != "" {
			return ""
		}
		match = folder.Key
	}
	return match
}

// ReadShelfPicture never starts a scan. parentKey, if present, must still
// name a first-level folder from this same rooted picture. The caller cannot
// supply a relative path, directory identity or arbitrary second-level root.
func (s *Service) ReadShelfPicture(ctx context.Context, owner, id, batchID, parentKey string) (ShelfPicture, error) {
	var no ShelfPicture
	if s == nil || s.store == nil || ctx.Err() != nil || batchID == "latest" || !managedRootGeneration.MatchString(batchID) || (parentKey != "" && len(parentKey) != 64) {
		return no, ErrReviewUnavailable
	}
	scope, err := s.shelfScope(owner, id)
	if err != nil {
		return no, err
	}
	state, err := s.store.loadScanState(id, true)
	if err != nil {
		return no, err
	}
	return s.shelfPicture(ctx, owner, id, batchID, parentKey, scope, state)
}

func (s *Service) shelfPicture(ctx context.Context, owner, id, batchID, parentKey string, scope shelfScope, state ScanState) (ShelfPicture, error) {
	var no ShelfPicture
	groups, binding, root, err := s.reviewDraftSnapshot(owner, id, batchID, state)
	if err != nil || root != scope.root || binding.Digest != scope.scope {
		return no, ErrReviewUnavailable
	}
	draft := projectReviewDraft(state, batchID, binding, groups)
	if draft.State == "invalid" || draft.State == "stale" {
		return no, ErrReviewConflict
	}
	first, partial, err := scope.folders(DirRef{ID: scope.witness})
	if err != nil {
		return no, err
	}
	view := ShelfPicture{Folder: DisplayFileName(filepath.Base(root.Path)), Batch: batchID, Folders: first, Partial: partial, Files: []ShelfFile{}, Supported: s.ConditionalMoveAvailable(root.Path, scope.witness) == nil}
	if parentKey != "" {
		found := false
		for _, shelf := range first {
			if shelf.Key == parentKey {
				found = true
				view.Folders, view.Partial, err = scope.folders(shelf.dir)
				break
			}
		}
		if !found || err != nil {
			return no, ErrReviewConflict
		}
	}
	excluded := map[string]bool{}
	for _, choice := range draft.Choices {
		if choice.Operation == OperationTrash || choice.Operation == OperationSend {
			excluded[choice.CandidateID] = true
		}
	}
	var versions []string
	for _, c := range state.CandidatesFor(batchID) {
		if !proposalPending(c) || c.Decision == DecisionTrash || excluded[c.ID] || state.IsSkipped(c.Fingerprint) || workspace.ContainedEntryExcluded(c.Name) {
			continue
		}
		versions = append(versions, c.Fingerprint.Key())
		view.Files = append(view.Files, ShelfFile{ID: c.ID, Name: c.Display(), Suggested: suggestedShelf(c.Name, first)})
	}
	view.Revision, err = setupDigest([]any{"filing-shelves-v1", scope.scope, scope.witness, batchID, draft.Revision, groups.Revision, first, view.Files, versions})
	if err != nil {
		return no, err
	}
	if again, err := s.shelfScope(owner, id); err != nil || again != scope || ctx.Err() != nil {
		return no, ErrReviewUnavailable
	}
	return view, nil
}
