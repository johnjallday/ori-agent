package folderagent

import (
	"errors"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// StationAnchor is a live metadata lookup: the unique lead and external anchor reference. Only its digest goes into the saved walk.
type StationAnchor struct{ Root, Reference, LeadID string }

func stationFileName(name string) bool {
	return filejanitor.ValidateFileName(name) == nil && filejanitor.DisplayFileName(name) == name && !workspace.ContainedEntryExcluded(name) && !filewatcher.ShouldIgnoreFile(name) && !sensitive.ContainsSecretLikeText(name)
}

// StationLook binds the build offer to the saved, current walk. The callback
// only returns host evidence; a look, a model or an offer cannot install Filing.
func (s *Service) StationLook(owner, id string) (filejanitor.StationLook, error) {
	if s == nil {
		return filejanitor.StationLook{}, ErrUnavailable
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
	look, ok := s.stationLook(r)
	if !ok {
		return filejanitor.StationLook{}, ErrWrongState
	}
	return look, nil
}

func (s *Service) stationLook(r *Record) (filejanitor.StationLook, bool) {
	var no filejanitor.StationLook
	guided := strings.HasPrefix(r.AssignedLink, GuidedDocumentsLinkPrefix)
	if (!guided && r.AssignedLink != "") || (r.State != StateReported && r.State != StateKept && !completed(r)) || r.Map == nil || r.LooseDocuments == nil || r.LooseDocuments.Pile == nil || r.Map.Stopped != "" {
		return no, false
	}
	pile := r.LooseDocuments.Pile
	if pile.Count < 1 || len(pile.Names) != min(6, pile.Count) {
		return no, false
	}
	for _, name := range pile.Names {
		if (guided && !reviewableDocumentName(name)) || (!guided && !stationFileName(name)) {
			return no, false
		}
	}
	for _, f := range r.Map.Folders {
		if f.State != foldermap.StateWalked && f.State != foldermap.StateClosed {
			return no, false
		}
	}
	root, ok := r.Map.Folder("")
	if !ok || root.BranchWitness == "" {
		return no, false
	}
	if !guided {
		if s.deps.StationAnchor == nil || r.LooseDocuments.StationBinding == "" {
			return no, false
		}
		a, ok := s.deps.StationAnchor(r.OwnerUserID, r.WorkspaceID, r.AgentName)
		if !ok || a.Reference == "" || a.LeadID == "" || scopeHash(a) != r.LooseDocuments.StationBinding || !s.currentLooseObservation(r, a.Root) {
			return no, false
		}
		info, err := os.Lstat(a.Root) // #nosec G304 G703 -- host-resolved owned workspace anchor, metadata only
		if err != nil || !info.IsDir() || diridentity.Of(info) != root.BranchWitness {
			return no, false
		}
		return filejanitor.StationLook{ReferenceID: a.Reference, Manager: r.AgentName, Witness: root.BranchWitness, Revision: scopeHash([]any{r.LooseDocuments, root.BranchWitness})}, true
	}
	if hint := s.looseDocumentHint(r); hint != "eligible" && hint != "history" {
		return no, false
	}
	a, err := s.organizationAccess(r.OwnerUserID, r)
	if err != nil || !s.currentLooseObservation(r, a.Root) {
		return no, false
	}
	return filejanitor.StationLook{ReferenceID: strings.TrimPrefix(r.AssignedLink, GuidedDocumentsLinkPrefix), Manager: r.AgentName, Witness: root.BranchWitness, Revision: scopeHash([]any{a.Binding, r.LooseDocuments, root.BranchWitness})}, true
}
