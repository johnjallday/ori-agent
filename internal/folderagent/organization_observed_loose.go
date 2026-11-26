package folderagent

import (
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// This is a hint about the saved look, not a selection or permission to read.
// Do not retain a list of filenames or send this observation to the model.
type LooseDocumentObservation struct {
	Eligible     bool      `json:"eligible,omitempty"`
	RootModified time.Time `json:"root_modified"`
	Policy       string    `json:"policy"`
}

func (s *Service) protectedReviewRoots() []string {
	if s.deps.DocumentsProtected != nil {
		return s.deps.DocumentsProtected()
	}
	return nil
}
func (s *Service) observeLooseDocuments(root string) (*LooseDocumentObservation, func(string)) {
	protected := s.protectedReviewRoots()
	out := &LooseDocumentObservation{Policy: scopeHash(protected)}
	if info, err := os.Lstat(root); err == nil && info.IsDir() {
		out.RootModified = info.ModTime()
	}
	return out, func(name string) {
		if out.Eligible || !reviewableDocumentName(name) {
			return
		}
		path := filepath.Join(root, name)
		out.Eligible = !slices.ContainsFunc(protected, func(p string) bool { return filejanitor.RootsOverlap(path, p) })
	}
}

func (s *Service) looseDocumentHint(rec *Record) string {
	access, err := s.organizationAccess(rec.OwnerUserID, rec)
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
	if seen == nil || seen.RootModified.IsZero() || (walked.State != foldermap.StateWalked && walked.State != foldermap.StateClosed) {
		return "partial"
	}
	info, err := os.Lstat(access.Root) // #nosec G304 G703 -- live host-resolved anchor; stat only, never enumerated on read.
	if err != nil || !info.ModTime().Equal(seen.RootModified) || seen.Policy != scopeHash(s.protectedReviewRoots()) {
		return "stale"
	}
	if seen.Eligible {
		return "eligible"
	}
	return "empty"
}
