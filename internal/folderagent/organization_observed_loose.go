package folderagent

import (
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

// This is a hint about the saved look, not a selection or permission to read.
// Only the owner-facing report gets samples, never the model or a file action.
type LooseDocumentObservation struct {
	Pile           *LooseFilePile `json:"pile,omitempty"`
	Eligible       bool           `json:"eligible,omitempty"`
	RootModified   time.Time      `json:"root_modified"`
	Policy         string         `json:"policy"`
	StationBinding string         `json:"station_binding,omitempty"` // hashed hired anchor/manager at the walk; never a read grant
}

// LooseFilePile is bounded display evidence. Older boolean-only observations
// cannot supply a count and must not be upgraded by re-listing the directory.
type LooseFilePile struct {
	Count int      `json:"count"`
	Names []string `json:"names"`
}

func (s *Service) protectedReviewRoots() []string {
	if s.deps.DocumentsProtected != nil {
		return s.deps.DocumentsProtected()
	}
	return nil
}
func (s *Service) observeLooseDocuments(root string) (*LooseDocumentObservation, func(string)) {
	return s.observeLooseNames(root, reviewableDocumentName)
}
func (s *Service) observeLooseNames(root string, allowed func(string) bool) (*LooseDocumentObservation, func(string)) {
	protected := s.protectedReviewRoots()
	out := &LooseDocumentObservation{Policy: scopeHash(protected), Pile: &LooseFilePile{}}
	if info, err := os.Lstat(root); err == nil && info.IsDir() {
		out.RootModified = info.ModTime()
	}
	return out, func(name string) {
		if !allowed(name) {
			return
		}
		path := filepath.Join(root, name)
		if slices.ContainsFunc(protected, func(p string) bool { return filejanitor.RootsOverlap(path, p) }) {
			return
		}
		out.Eligible = true
		out.Pile.Count++
		if len(out.Pile.Names) < 6 {
			out.Pile.Names = append(out.Pile.Names, name)
		}
	}
}

func (s *Service) looseDocumentHint(rec *Record) string {
	access, err := s.savedReviewAccess(rec.OwnerUserID, rec)
	if err != nil {
		return "unavailable"
	}
	walked, ok := rec.Map.Folder("")
	if !ok {
		return "partial"
	}
	authority := documentsRef(rec.OwnerUserID, rec.WorkspaceID, access.Root, walked.BranchWitness, access.Lead, access.Binding)
	if slices.ContainsFunc(rec.OrganizationRuns, func(run OrganizationRun) bool { return run.Authority == authority }) {
		return "history"
	}
	seen := rec.LooseDocuments
	if seen == nil || seen.Pile == nil || seen.RootModified.IsZero() || !completeStationMap(rec.Map) {
		return "partial"
	}
	if !s.currentLooseObservation(rec, access.Root) {
		return "stale"
	}
	if seen.Eligible {
		return "eligible"
	}
	return "empty"
}

func (s *Service) currentLooseObservation(rec *Record, root string) bool {
	seen := rec.LooseDocuments
	info, err := os.Lstat(root) // #nosec G304 G703 -- live host-resolved anchor; stat only, never enumerated on read.
	return seen != nil && err == nil && info.IsDir() && info.ModTime().Equal(seen.RootModified) && seen.Policy == scopeHash(s.protectedReviewRoots())
}
