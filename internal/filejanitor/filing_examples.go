package filejanitor

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Like Filing Station's shelf placements, examples live in editable workspace
// feedback memory. A line refers to one verified action, never an arbitrary
// filesystem path; edits, ambiguous copies and missing evidence fail closed.
type exampleMemory interface {
	SnapshotExact(string) (workspace.MemoryExactSnapshot, error)
	AppendExact(string, string, workspace.MemoryEntry) (workspace.MemoryExactTarget, error)
	EditExact(string, workspace.MemoryExactTarget, *workspace.MemoryEntry) error
}

func (s *Service) SetExampleMemory(m exampleMemory) { s.exampleMemory = m }

type FilingExample struct {
	ActionID string `json:"action_id"`
	Name     string `json:"name"`
	Folder   string `json:"folder"`
}
type FolderSuggestion struct {
	FilingExample
	CandidateID   string `json:"candidate_id"`
	CandidateName string `json:"candidate_name"`
}

const examplePrefix = "filing-placement/chosen/"
const maxFolderExamples = 64

func exampleFor(a FileAction) FilingExample {
	return FilingExample{ActionID: a.ID, Name: DisplayFileName(a.SourceName), Folder: path.Dir(a.DestinationRelative)}
}
func (e FilingExample) text() string {
	return fmt.Sprintf("Suggest %q for names like %q.", e.Folder, e.Name)
}
func (e FilingExample) marker() string { return examplePrefix + e.ActionID }

// A whole name stem/prefix and the extension, never an extension alone.
// Thus hotel-receipt also matches hotel-receipt-may, but not hotel-reservation.
func exampleWords(name string) []string {
	if name != DisplayFileName(name) || sensitiveReviewName(name) || sensitive.ContainsSecretLikeText(name) {
		return nil
	}
	stem := strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
	if path.Ext(name) == "" {
		return nil
	}
	words := strings.FieldsFunc(stem, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 || len(words) > 12 {
		return nil
	}
	for _, w := range words {
		if len(w) >= 3 && strings.IndexFunc(w, unicode.IsLetter) >= 0 {
			return words
		}
	}
	return nil
}
func exampleAction(root ManagedRoot, a FileAction) bool {
	if a.Operation != OperationDeliver || a.Result != ResultApplied || a.AfterFingerprint.FileID == "" || a.Validate() != nil {
		return false
	}
	if a.RootID != root.Generation || a.ApprovedBy != root.Owner || a.WorkspaceID != root.WorkspaceID {
		return false
	}
	return len(exampleWords(a.SourceName)) > 0 && !sensitive.ContainsSecretLikeText(a.DestinationRelative) && !sensitiveReviewName(a.DestinationRelative) && len(exampleFor(a).text()) <= workspace.MemoryEntryMaxLen
}

type savedExample struct {
	a      FileAction
	words  []string
	intact bool
}

func savedExamples(root ManagedRoot, state ScanState, mem workspace.MemoryExactSnapshot) []savedExample {
	var out []savedExample
	for _, line := range mem.Entries {
		id, ok := strings.CutPrefix(line.Entry.Provenance, examplePrefix)
		if !ok {
			continue
		}
		if len(out) >= maxFolderExamples {
			return nil
		}
		a, ok := state.Action(id)
		if !ok || !exampleAction(root, a) {
			continue
		}
		out = append(out, savedExample{a: a, words: exampleWords(a.SourceName), intact: line.Entry.Type == workspace.MemoryTypeFeedback && line.Entry.Text == exampleFor(a).text()})
	}
	return out
}
func matchExample(saved []savedExample, name string) (FileAction, bool) {
	words := exampleWords(name)
	var found *savedExample
	for i := range saved {
		e := &saved[i]
		if !strings.EqualFold(path.Ext(name), path.Ext(e.a.SourceName)) || len(words) < len(e.words) || !slices.Equal(words[:len(e.words)], e.words) {
			continue
		}
		if !e.intact || (found != nil && (found.a.Delivery.Key != e.a.Delivery.Key || found.a.ID == e.a.ID)) {
			return FileAction{}, false
		}
		found = e
	}
	if found == nil {
		return FileAction{}, false
	}
	return found.a, true
}
func examplePath(root ManagedRoot, state ScanState, a FileAction) (string, bool) {
	p := state.ChosenPaths[a.Delivery.Key]
	if !a.Delivery.matches(root.WorkspaceID, root.Generation, p) {
		return "", false
	}
	st, err := os.Lstat(p)
	return p, err == nil && st.IsDir() && directoryid.Witness(st) == a.Delivery.FolderID
}
func (s *Service) folderSuggestions(owner, id string, snap pictureSnapshot) []FolderSuggestion {
	if s.exampleMemory == nil || len(snap.state.ChosenPaths) == 0 {
		return nil
	}
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return nil
	}
	mem, err := s.exampleMemory.SnapshotExact(id)
	if err != nil {
		return nil
	}
	saved := savedExamples(root, snap.state, mem)
	if len(saved) == 0 {
		return nil
	}
	var out []FolderSuggestion
	excluded := map[string]bool{}
	for _, ch := range snap.view.Choices {
		if ch.Operation == OperationTrash || ch.Operation == OperationSend {
			excluded[ch.CandidateID] = true
		}
	}
	for _, c := range snap.candidates {
		if !proposalPending(c) || c.Decision == DecisionTrash || excluded[c.ID] {
			continue
		}
		a, ok := matchExample(saved, c.Name)
		if !ok {
			continue
		}
		if _, ok := examplePath(root, snap.state, a); !ok {
			continue
		}
		out = append(out, FolderSuggestion{FilingExample: exampleFor(a), CandidateID: c.ID, CandidateName: c.Display()})
		if len(out) >= maxFolderExamples {
			break
		}
	}
	return out
}

// Confirm and Forget change a preference, never a review choice or approval.
func (s *Service) EditFilingExample(owner, id, actionID string, forget bool) error {
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil || s.exampleMemory == nil {
		return ErrReviewUnavailable
	}
	unlock := lockRoot(root.Path)
	defer unlock()
	state, err := s.store.loadScanState(id, true)
	if err != nil {
		return err
	}
	a, ok := state.Action(actionID)
	if !ok || !exampleAction(root, a) {
		return ErrReviewConflict
	}
	e := exampleFor(a)
	mem, err := s.exampleMemory.SnapshotExact(id)
	if err != nil {
		return ErrReviewUnavailable
	}
	count := 0
	for _, l := range mem.Entries {
		if strings.HasPrefix(l.Entry.Provenance, examplePrefix) {
			count++
		}
		if e.marker() == l.Entry.Provenance {
			if l.Entry.Text != e.text() {
				return ErrReviewConflict
			}
			if forget {
				return s.exampleMemory.EditExact(id, l.Target, nil)
			}
			return nil
		}
	}
	if forget {
		return nil
	}
	if count >= maxFolderExamples || !a.Undoable() {
		return ErrReviewConflict
	}
	if _, ok := examplePath(root, state, a); !ok {
		return ErrReviewConflict
	}
	if again, e := s.StoredManagedRoot(owner, id); e != nil || again != root {
		return ErrReviewConflict
	}
	_, err = s.exampleMemory.AppendExact(id, mem.FileHash, workspace.MemoryEntry{Date: s.clock().UTC().Format("2006-01-02"), Type: workspace.MemoryTypeFeedback, Text: e.text(), Provenance: e.marker()})
	return err
}
func (s *Service) receiptExamples(owner, id string, state ScanState, rec FilingRecord) []FilingExample {
	if s.exampleMemory == nil || rec.CompletedAt.IsZero() || !rec.UndoClaimedAt.IsZero() || len(state.ChosenPaths) == 0 {
		return nil
	}
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return nil
	}
	mem, err := s.exampleMemory.SnapshotExact(id)
	if err != nil {
		return nil
	}
	exists := map[string]bool{}
	for _, l := range mem.Entries {
		exists[l.Entry.Provenance] = true
	}
	var out []FilingExample
	for _, a := range filingActions(state, rec.ID) {
		e := exampleFor(a)
		if a.Undoable() && exampleAction(root, a) && !exists[e.marker()] {
			out = append(out, e)
			if len(out) >= maxFolderExamples {
				break
			}
		}
	}
	return out
}

// Resolve only this candidate's current, unique remembered example. Called
// again at plan construction under the admission lock. A replaced folder at
// the old spelling must never acquire the former identity's suggestion.
func (s *Service) resolveExample(root ManagedRoot, state ScanState, candidateID, actionID string) (string, string, error) {
	if s.exampleMemory == nil || actionID == "" {
		return "", "", ErrReviewConflict
	}
	c, ok := state.Candidate(candidateID)
	if !ok || !proposalPending(c) {
		return "", "", ErrReviewConflict
	}
	mem, err := s.exampleMemory.SnapshotExact(root.WorkspaceID)
	if err != nil {
		return "", "", ErrReviewConflict
	}
	a, ok := matchExample(savedExamples(root, state, mem), c.Name)
	if !ok || a.ID != actionID {
		return "", "", ErrReviewConflict
	}
	p, ok := examplePath(root, state, a)
	if !ok {
		return "", "", ErrReviewConflict
	}
	return p, a.Delivery.Key, nil
}
func (s *Service) ResolveExamplePath(owner, id, candidateID, actionID string) (string, error) {
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return "", err
	}
	state, err := s.store.loadScanState(id, true)
	if err != nil {
		return "", err
	}
	p, _, err := s.resolveExample(root, state, candidateID, actionID)
	return p, err
}
