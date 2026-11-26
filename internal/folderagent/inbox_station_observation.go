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
	Anchor                                   StationAnchor
	Manager, WorkspaceName, Route, Operation string
	Pile                                     *folderdigest.ObservedPile
}

func (s *Service) inboxStation(owner, id string) (View, filejanitor.StationLook, bool) {
	var view View
	var look filejanitor.StationLook
	if s == nil || s.deps.InboxStation == nil {
		return view, look, false
	}
	src, ok := s.deps.InboxStation(owner, id)
	p := src.Pile
	if !ok || p == nil || p.Count < 1 || p.Count > folderdigest.DefaultMaxEntries || len(p.Names) != min(p.Count, 6) || p.Witness == "" || p.Policy != scopeHash(s.protectedReviewRoots()) || src.Anchor.LeadID == "" || src.Anchor.Reference == "" || src.Operation == "" {
		return view, look, false
	}
	for _, n := range p.Names {
		if !stationFileName(n) {
			return view, look, false
		}
	}
	info, err := os.Lstat(src.Anchor.Root) // #nosec G304 G703 -- owner-scoped anchored directory, metadata only
	if err != nil || !info.IsDir() || diridentity.Of(info) != p.Witness || !info.ModTime().Equal(p.Modified) {
		return view, look, false
	}
	view = View{WorkspaceID: id, WorkspaceName: src.WorkspaceName, Route: src.Route, AgentName: src.Manager, FolderName: filepath.Base(src.Anchor.Root), State: StateKept, StationHire: true, FilingPile: &LooseFilePile{Count: p.Count, Names: slices.Clone(p.Names)}}
	look = filejanitor.StationLook{ReferenceID: src.Anchor.Reference, Manager: src.Manager, Witness: p.Witness, Revision: scopeHash([]any{owner, id, src.Operation, src.Anchor, p})}
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
