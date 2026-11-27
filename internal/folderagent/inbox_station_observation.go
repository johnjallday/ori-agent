package folderagent

import (
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"slices"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// InboxStation is a projection of an accepted hire: its original lead and
// reference are checked by the host, and the saved look is checked here. No
// folder-agent report or look ticket needs to be manufactured for this entry.
type InboxStation struct {
	Anchor                                            StationAnchor
	Manager, WorkspaceName, Route, Operation, Witness string
	Pile                                              *folderdigest.ObservedPile
}

func (s *Service) inboxStation(owner, id string) (View, filejanitor.StationLook, bool) {
	var view View
	var look filejanitor.StationLook
	if s == nil || s.deps.InboxStation == nil {
		return view, look, false
	}
	src, ok := s.deps.InboxStation(owner, id)
	p := src.Pile
	witness := src.Witness
	if witness == "" && p != nil {
		witness = p.Witness
	} // older witnessed hire
	if !ok || src.Anchor.LeadID == "" || src.Anchor.Reference == "" || src.Operation == "" || !s.currentStationRoot(src.Anchor.Root, witness) {
		return view, look, false
	}
	view = View{WorkspaceID: id, WorkspaceName: src.WorkspaceName, Route: src.Route, AgentName: src.Manager, FolderName: filepath.Base(src.Anchor.Root), State: StateKept, StationHire: true, StationOffer: true}
	look = filejanitor.StationLook{ReferenceID: src.Anchor.Reference, Manager: src.Manager, Witness: witness, Revision: scopeHash([]any{owner, id, src.Operation, src.Anchor, witness})}
	if p != nil && p.Count > 0 && p.Count <= folderdigest.DefaultMaxEntries && len(p.Names) == min(p.Count, 6) && p.Witness == witness && p.Policy == scopeHash(s.protectedReviewRoots()) && !slices.ContainsFunc(p.Names, func(n string) bool { return !stationFileName(n) }) {
		info, err := os.Lstat(src.Anchor.Root) // #nosec G304 G703 -- owned anchor, metadata only
		if err == nil && diridentity.Of(info) == witness && info.ModTime().Equal(p.Modified) {
			view.FilingPile = &LooseFilePile{Count: p.Count, Names: slices.Clone(p.Names)}
		}
	}
	return view, look, true
}

// ObserveInbox captures station evidence during the existing attachment walk.
// No second enumeration or content authority is introduced by a hire.
func ObserveInbox(root string, protected []string) (folderdigest.Result, error) {
	result, err := folderdigest.Scan(root, folderdigest.Options{CaptureTree: true, PileFile: func(name string) bool {
		if !stationFileName(name) {
			return false
		}
		path := filepath.Join(root, name)
		return !slices.ContainsFunc(protected, func(p string) bool { return filejanitor.RootsOverlap(path, p) })
	}})
	if result.Pile != nil {
		result.Pile.Policy = scopeHash(protected)
	}
	return result, err
}
