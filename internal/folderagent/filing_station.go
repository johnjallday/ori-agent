package folderagent

import (
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// StationAnchor is a live metadata lookup: the unique lead and external anchor reference.
type StationAnchor struct{ Root, Reference, LeadID string }

func stationFileName(name string) bool {
	return filejanitor.ValidateFileName(name) == nil && filejanitor.DisplayFileName(name) == name && !workspace.ContainedEntryExcluded(name) && !filewatcher.ShouldIgnoreFile(name) && !sensitive.ContainsSecretLikeText(name)
}

// StationLook binds a new Build review to a workspace, not an inventory. No
// filenames, counts, or completed walks are required to accept a metadata scan.
func (s *Service) StationLook(owner, id string) (filejanitor.StationLook, error) {
	if s == nil {
		return filejanitor.StationLook{}, ErrUnavailable
	}
	if s.deps.GuidedStation != nil {
		if look, ok := s.deps.GuidedStation(owner, id); ok {
			return look, nil
		}
	}
	r, err := s.Record(owner, id)
	if errors.Is(err, ErrNoRecord) {
		if _, look, ok := s.inboxStation(owner, id); ok {
			return look, nil
		}
	}
	if err != nil {
		return filejanitor.StationLook{}, err
	}
	look, _, ok := s.hiredStation(r)
	if !ok {
		return filejanitor.StationLook{}, ErrWrongState
	}
	return look, nil
}

func (s *Service) CanOfferGuidedStation(owner, id string) bool {
	if s.deps.GuidedStation == nil || s.deps.StationAvailable == nil || !s.deps.StationAvailable(owner, id) {
		return false
	}
	_, ok := s.deps.GuidedStation(owner, id)
	return ok
}

func completeStationMap(m *foldermap.Map) bool {
	if m == nil || m.Stopped != "" {
		return false
	}
	for _, f := range m.Folders {
		if f.State != foldermap.StateWalked && f.State != foldermap.StateClosed {
			return false
		}
	}
	return true
}

// Keep the original hire's lead/reference and physical directory identity.
// File arrivals invalidate a displayed pile, not the right to review Build.
func (s *Service) hiredStation(r *Record) (filejanitor.StationLook, string, bool) {
	var no filejanitor.StationLook
	if r.AssignedLink != "" || r.Map == nil || r.LooseDocuments == nil || s.deps.StationAnchor == nil || (r.State != StateReported && r.State != StateKept && !completed(r)) {
		return no, "", false
	}
	a, ok := s.deps.StationAnchor(r.OwnerUserID, r.WorkspaceID, r.AgentName)
	if !ok || a.Reference == "" || a.LeadID == "" || scopeHash(a) != r.LooseDocuments.StationBinding {
		return no, "", false
	}
	f, ok := r.Map.Folder("")
	if !ok || !s.currentStationRoot(a.Root, f.BranchWitness) {
		return no, "", false
	}
	return filejanitor.StationLook{ReferenceID: a.Reference, Manager: r.AgentName, Witness: f.BranchWitness, Revision: scopeHash([]any{r.OwnerUserID, r.WorkspaceID, a, f.BranchWitness})}, a.Root, true
}

func (s *Service) currentStationRoot(path, witness string) bool {
	if witness == "" || slices.ContainsFunc(s.protectedReviewRoots(), func(p string) bool { return filejanitor.RootsOverlap(path, p) }) {
		return false
	}
	info, err := os.Lstat(path) // #nosec G304 G703 -- server-held anchored directory; metadata only.
	return err == nil && diridentity.Of(info) == witness
}

// Pile evidence is optional presentation, never an operand of Build.
func (s *Service) stationOffer(r *Record, v *View) {
	if s.deps.StationAvailable == nil || !s.deps.StationAvailable(r.OwnerUserID, r.WorkspaceID) {
		return
	}
	nameOK := stationFileName
	if strings.HasPrefix(r.AssignedLink, GuidedDocumentsLinkPrefix) {
		if s.deps.GuidedStation == nil || r.State == StateSettingUp {
			return
		}
		look, ok := s.deps.GuidedStation(r.OwnerUserID, r.WorkspaceID)
		if !ok || look.Manager != r.AgentName || r.AssignedLink != GuidedDocumentsLinkPrefix+look.ReferenceID {
			return
		}
		v.StationOffer = true
		if v.FilingReviewHint != "eligible" && v.FilingReviewHint != "history" {
			return
		}
		a, err := s.organizationAccess(r.OwnerUserID, r)
		if err != nil || !s.currentLooseObservation(r, a.Root) {
			return
		}
		nameOK = reviewableDocumentName
	} else {
		_, root, ok := s.hiredStation(r)
		if !ok {
			return
		}
		v.StationOffer = true
		if !s.currentLooseObservation(r, root) {
			return
		}
	}
	if r.LooseDocuments == nil || !completeStationMap(r.Map) {
		return
	}
	p := r.LooseDocuments.Pile
	if p != nil && p.Count > 0 && len(p.Names) == min(6, p.Count) && !slices.ContainsFunc(p.Names, func(n string) bool { return !nameOK(n) }) {
		v.FilingPile = p
	}
}
