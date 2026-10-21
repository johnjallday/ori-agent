package server

import (
	"errors"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// typedFolderPaths is the folder picker's decision on a path the user typed:
// the request must come from Ori's own page, and the path must pass the shared
// typed-path rules. Only then does select-path issue the selection a dialog
// choice would get.
type typedFolderPaths struct {
	guards filejanitor.RootGuards
	origin *connectionshttp.OriginGuard
}

var _ workspace.TypedFolderPathChecker = typedFolderPaths{}

func newTypedFolderPaths(guards filejanitor.RootGuards) typedFolderPaths {
	return typedFolderPaths{guards: guards, origin: connectionshttp.NewOriginGuard()}
}

func (t typedFolderPaths) Allow(r *http.Request) error { return t.origin.Check(r) }

func (t typedFolderPaths) Check(raw string) (workspace.TypedFolderPath, error) {
	checked, err := folderpath.Check(raw, t.guards)
	if err != nil {
		// Only the refusal's own sentence leaves the server.
		return workspace.TypedFolderPath{}, errors.New(folderpath.Message(err))
	}
	return workspace.TypedFolderPath{
		Path: checked.Path, Warning: checked.Warning, WarningNote: folderpath.WarningNote(checked.Warning),
	}, nil
}

// catalogFolder is the same decision for a folder the owner adds to a
// resource catalog: Ori's own page, then the shared typed-path rules. It
// returns the canonical folder and the sentence to show beside it.
func (t typedFolderPaths) catalogFolder(r *http.Request, raw string) (string, string, error) {
	if err := t.Allow(r); err != nil {
		return "", "", &folderpath.Error{Message: "This request did not come from Ori's own page."}
	}
	checked, err := t.Check(raw)
	if err != nil {
		return "", "", err
	}
	return checked.Path, checked.WarningNote, nil
}
