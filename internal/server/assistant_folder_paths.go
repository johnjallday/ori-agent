package server

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assistantFolderPaths is the assistant's reader for a folder path written in
// the chat. The path gets the same typed-path rules as the field beside "Choose
// folder", and is held as the same server-side selection that field issues, so
// what the browser confirms with is a token and never the path.
type assistantFolderPaths struct {
	guards     filejanitor.RootGuards
	selections *pathselection.Store
	workspaces workspace.Store
}

var _ agenthttp.FolderPaths = assistantFolderPaths{}

func (p assistantFolderPaths) Find(message string) (agenthttp.FolderPathFound, bool) {
	found, ok := folderpath.Find(message, p.guards)
	if !ok {
		return agenthttp.FolderPathFound{}, false
	}
	return agenthttp.FolderPathFound{
		Path: found.Path, Name: found.Name, Text: found.Text, Start: found.Start, End: found.End,
		Warning: found.Warning, WarningNote: folderpath.WarningNote(found.Warning), Refusal: found.Refusal,
	}, true
}

// Hold checks the folder again (it may have gone since the message was read)
// and issues its selection: bound to the workspace for a link, unbound for the
// conversation's own folder.
func (p assistantFolderPaths) Hold(path, workspaceID string) (string, error) {
	if p.selections == nil {
		return "", errors.New("folder selections are unavailable")
	}
	checked, err := folderpath.Check(path, p.guards)
	if err != nil {
		return "", errors.New(folderpath.Message(err))
	}
	if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
		return p.selections.IssueFor(checked.Path, workspaceID)
	}
	return p.selections.Issue(checked.Path)
}

// LinkState says how the folder stands to the folders a person linked to the
// workspace (a capability's own folders are not those): it is one of them, it
// sits inside one, or it contains one.
func (p assistantFolderPaths) LinkState(workspaceID, path string) string {
	if p.workspaces == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	ws, err := p.workspaces.Get(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return ""
	}
	want := canonicalFolder(path)
	state := ""
	for _, ref := range ws.DirectoryReferences {
		if ref.WorkspaceID != ws.ID || strings.TrimSpace(ref.Purpose) != "" || strings.TrimSpace(ref.Path) == "" {
			continue
		}
		linked := canonicalFolder(ref.Path)
		switch {
		case linked == want:
			return agenthttp.FolderLinked
		case folderWithin(linked, want):
			state = agenthttp.FolderInsideLinked
		case folderWithin(want, linked) && state == "":
			state = agenthttp.FolderContainsLinked
		}
	}
	return state
}

// folderWithin reports whether child lies inside parent. Both are canonical.
func folderWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// canonicalFolder compares two spellings of one folder: symlinks resolved when
// the folder exists, cleaned otherwise.
func canonicalFolder(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// assistantFolderPaths builds the reader from the stores the folder picker
// already uses, so a path in chat and a path typed beside "Choose folder"
// follow one set of rules and one selection store.
func (b *ServerBuilder) assistantFolderPaths() agenthttp.FolderPaths {
	if b == nil || b.pathSelectionStore == nil {
		return nil
	}
	guards := filejanitor.DefaultRootGuards()
	guards.WorkspaceRoot = resolveWorkspaceRoot(b.configManager)
	return assistantFolderPaths{guards: guards, selections: b.pathSelectionStore, workspaces: b.workspaceStore}
}
