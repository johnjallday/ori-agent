package server

import (
	"context"
	"fmt"
	"path"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

// folderAgentLibrary is what the folder agent's listings ask of a Home's
// library: the scopes it issued, and one reviewed-and-committed scan of the
// root or of a scope. *sessionhttp.Handler provides it.
type folderAgentLibrary interface {
	FolderAgentScopes(ownerUserID, homeID, rootID string) ([]projectlibrary.ScanScopeChoice, error)
	ScanFolderAgentRoot(ctx context.Context, ownerUserID, homeID, rootID, key string) (int, bool, error)
	ScanFolderAgentScope(ctx context.Context, ownerUserID, homeID, rootID, scopeID, key string) (int, bool, error)
}

// folderAgentListing is what the listings found: how many songs the library
// lists under the root afterwards, the plural word for them, and how many
// folders holding songs no listing reached.
type folderAgentListing struct {
	listed    int
	noun      string
	unreached int
}

// listFolderAgentSongs lists the folder's songs in the Home's library: the whole
// root, then the scopes the library issued that lead to the folders the walk
// found holding songs. Set it up runs it to put the songs on the shelf; "Learn
// them" runs it again, under a fresh key, so the library's own fact pass reads
// the songs under the consent just given. The scans themselves read no project
// file; only the pass does, and only under a consent.
//
// songsLine and scopesLine are the plan lines it reports on through setLine (a
// name that is empty, or that the plan lacks, is ignored). An error is the
// failure of the root listing, the one the run cannot go on without.
func listFolderAgentSongs(ctx context.Context, library folderAgentLibrary, rec folderagent.Record, owner, homeID, rootID, key,
	songsLine, scopesLine string, setLine func(name, state, progress string)) (folderAgentListing, error) {
	shape := folderdigest.Shape("")
	if rec.Facts.Portfolio != nil {
		shape = rec.Facts.Portfolio.Shape
	}
	setLine(songsLine, personalassistant.FolderLineWorking, "Listing "+foldermap.ShapeNoun(shape, 2))
	listed, _, err := library.ScanFolderAgentRoot(ctx, owner, homeID, rootID, key+"-scan")
	if err != nil {
		setLine(songsLine, personalassistant.FolderLineFailed, "")
		return folderAgentListing{}, err
	}
	needed := folderagent.ProjectParents(rec.Facts)
	scanned, skipped, failures := map[string]bool{}, map[string]bool{}, map[string]int{}
	scans := 0
	if len(needed) > 0 {
		setLine(scopesLine, personalassistant.FolderLineWorking, "")
	}
	for scans < folderagent.MaxScopedScans {
		issued, err := library.FolderAgentScopes(owner, homeID, rootID)
		if err != nil {
			// The folders still needed stay unreached (and say so); it is logged
			// because nothing else shows why a listing stopped at the root.
			logger.Warn("Folder agent could not read the library's scopes", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
			break
		}
		scopes := make([]folderagent.LibraryScope, 0, len(issued))
		for _, s := range issued {
			scopes = append(scopes, folderagent.LibraryScope{ID: s.ID, RelativeFolder: s.RelativeFolder})
		}
		done := make(map[string]bool, len(scanned)+len(skipped))
		for folder := range scanned {
			done[folder] = true
		}
		for folder := range skipped {
			done[folder] = true
		}
		next := folderagent.NextScopes(needed, scopes, done)
		if len(next) == 0 {
			break
		}
		for _, s := range next {
			if scans >= folderagent.MaxScopedScans {
				break
			}
			// The folder's own name, never its path.
			setLine(scopesLine, personalassistant.FolderLineWorking, "Looking into "+path.Base(s.RelativeFolder))
			scans++
			count, _, err := library.ScanFolderAgentScope(ctx, owner, homeID, rootID, s.ID, fmt.Sprintf("%s-scope-%d", key, scans))
			if err != nil {
				// Tried again in the next round; twice failed counts as not
				// reached.
				failures[s.RelativeFolder]++
				if failures[s.RelativeFolder] >= 2 {
					skipped[s.RelativeFolder] = true
				}
				logger.Warn("Folder agent scoped listing did not finish", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
				continue
			}
			scanned[s.RelativeFolder] = true
			listed = count
		}
	}
	unreached := folderagent.Unreached(needed, scanned)
	if len(needed) > 0 {
		state := personalassistant.FolderLineDone
		if len(unreached) > 0 {
			state = personalassistant.FolderLineFailed
		}
		words := ""
		if n := len(scanned); n > 0 && state == personalassistant.FolderLineDone {
			words = fmt.Sprintf("Looked into %d %s", n, pluralWord(n, "folder", "folders"))
		}
		setLine(scopesLine, state, words)
	}
	noun := foldermap.ShapeNoun(shape, listed)
	setLine(songsLine, personalassistant.FolderLineDone, fmt.Sprintf("Listed %d %s on the shelf", listed, noun))
	return folderAgentListing{listed: listed, noun: noun, unreached: len(unreached)}, nil
}
