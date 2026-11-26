package folderagent

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// StationLook binds the build offer to the saved, current walk. The callback
// only returns host evidence; a look, a model or an offer cannot install Filing.
func (s *Service) StationLook(owner, id string) (filejanitor.StationLook, error) {
	if s == nil {
		return filejanitor.StationLook{}, ErrUnavailable
	}
	r, err := s.Record(owner, id)
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
	if !strings.HasPrefix(r.AssignedLink, GuidedDocumentsLinkPrefix) || r.Map == nil || r.LooseDocuments == nil || r.LooseDocuments.Pile == nil || r.Map.Stopped != "" {
		return no, false
	}
	pile := r.LooseDocuments.Pile
	if pile.Count < 1 || len(pile.Names) != min(6, pile.Count) {
		return no, false
	}
	for _, name := range pile.Names {
		if !reviewableDocumentName(name) {
			return no, false
		}
	}
	for _, f := range r.Map.Folders {
		if f.State != foldermap.StateWalked && f.State != foldermap.StateClosed {
			return no, false
		}
	}
	if hint := s.looseDocumentHint(r); hint != "eligible" && hint != "history" {
		return no, false
	}
	a, err := s.organizationAccess(r.OwnerUserID, r)
	if err != nil || !s.currentLooseObservation(r, a.Root) {
		return no, false
	}
	root, _ := r.Map.Folder("")
	return filejanitor.StationLook{ReferenceID: strings.TrimPrefix(r.AssignedLink, GuidedDocumentsLinkPrefix), Manager: r.AgentName, Witness: root.BranchWitness, Revision: scopeHash([]any{a.Binding, r.LooseDocuments, root.BranchWitness})}, true
}
