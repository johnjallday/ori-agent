package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// Doors into the notes folder that are not Obsidian (Issue 776 FR15, FR16,
// FR22): open the folder in the file manager, preview a note Ori wrote, and list
// People. None of them writes to the folder or keeps what it read.

// ErrSecondBrainNoteIsYours refuses a preview of a note Ori cannot prove it
// wrote as it is now.
var ErrSecondBrainNoteIsYours = errors.New("personal assistant: that note is yours")

// ErrSecondBrainNoteNotFound is a previewed note that is not there.
var ErrSecondBrainNoteNotFound = errors.New("personal assistant: that note is not there")

// maxOpenedRequests bounds the request ids Open folder remembers for replay.
const maxOpenedRequests = 64

// folderOpener opens a folder in the OS file manager. platform.OpenFolder
// honours ORI_NO_DESKTOP_OPEN, logging "Skipped desktop launch" instead.
type folderOpener func(path string) error

// SetFolderOpener replaces the opener, for tests. Startup wiring keeps the
// platform's.
func (s *SecondBrainService) SetFolderOpener(open func(path string) error) {
	if s != nil && open != nil {
		s.doorsMu.Lock()
		s.openFolder = open
		s.doorsMu.Unlock()
	}
}

// OpenFolder opens the connected folder in the file manager, for either origin:
// opening a folder reads nothing. A replayed request id opens nothing again.
func (s *SecondBrainService) OpenFolder(ctx context.Context, userID, requestID string) error {
	if !secondbrain.ValidRequestID(requestID) {
		return ErrValidation
	}
	doc, err := s.read(ctx, userID)
	if err != nil {
		return err
	}
	source := doc.Connected()
	if source == nil {
		return ErrSecondBrainNotConnected
	}
	if info, err := os.Stat(source.Path); err != nil || !info.IsDir() {
		return ErrSecondBrainUnavailable
	}
	s.doorsMu.Lock()
	if s.openedRequests == nil {
		s.openedRequests = map[string]bool{}
	}
	if s.openedRequests[requestID] {
		s.doorsMu.Unlock()
		return nil
	}
	if len(s.openedOrder) == maxOpenedRequests {
		delete(s.openedRequests, s.openedOrder[0])
		s.openedOrder = s.openedOrder[1:]
	}
	s.openedRequests[requestID] = true
	s.openedOrder = append(s.openedOrder, requestID)
	open := s.openFolder
	s.doorsMu.Unlock()
	if open == nil {
		open = platform.OpenFolder
	}
	if err := open(source.Path); err != nil {
		logger.Warn("The notes folder could not be opened", logger.Fields{"error": err.Error()})
		return ErrSecondBrainUnavailable
	}
	return nil
}

// SecondBrainPreviewView is one note Ori wrote, shown inline on request
// (FR16). Body is plain text; it is never stored, cached or sent to a model.
type SecondBrainPreviewView struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
	Link      string `json:"link,omitempty"`
}

// Preview reads one note in a folder Ori set up, only when its marker proves
// Ori wrote it as it is. A connected vault is never previewed: Ori wrote none
// of it.
func (s *SecondBrainService) Preview(ctx context.Context, userID, kind, name string) (SecondBrainPreviewView, error) {
	if _, err := secondbrain.PreviewRel(kind, name); err != nil {
		return SecondBrainPreviewView{}, ErrValidation
	}
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainPreviewView{}, err
	}
	source := doc.Connected()
	if source == nil {
		return SecondBrainPreviewView{}, ErrSecondBrainNotConnected
	}
	if !source.CreatedByOri() {
		return SecondBrainPreviewView{}, ErrSecondBrainNoteIsYours
	}
	preview, err := secondbrain.ReadPreview(source.Path, kind, name)
	switch {
	case errors.Is(err, secondbrain.ErrPreviewInvalid):
		return SecondBrainPreviewView{}, ErrValidation
	case errors.Is(err, secondbrain.ErrPreviewNotOri):
		return SecondBrainPreviewView{}, ErrSecondBrainNoteIsYours
	case errors.Is(err, secondbrain.ErrPreviewNotFound):
		return SecondBrainPreviewView{}, ErrSecondBrainNoteNotFound
	case err != nil:
		return SecondBrainPreviewView{}, ErrSecondBrainUnavailable
	}
	return SecondBrainPreviewView{
		Name: preview.Name, Kind: preview.Kind, Title: preview.Title, Body: preview.Body, Truncated: preview.Truncated,
		Link: secondbrain.ObsidianPathLink(filepath.Join(source.Path, filepath.FromSlash(preview.Rel))),
	}, nil
}

// SecondBrainPeopleView lists People/*.md in a folder Ori set up (FR22).
type SecondBrainPeopleView struct {
	People  []SecondBrainPersonView `json:"people"`
	Partial bool                    `json:"partial,omitempty"`
}

// SecondBrainPersonView is one person's note: its name and whose it is.
type SecondBrainPersonView struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Link string `json:"link,omitempty"`
}

// People lists the People notes by file name, peeking only at each marker.
func (s *SecondBrainService) People(ctx context.Context, userID string) (SecondBrainPeopleView, error) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainPeopleView{}, err
	}
	source := doc.Connected()
	if source == nil {
		return SecondBrainPeopleView{}, ErrSecondBrainNotConnected
	}
	if !source.CreatedByOri() {
		return SecondBrainPeopleView{}, ErrSecondBrainNoInbox
	}
	listed, err := secondbrain.ListPeople(source.Path)
	if err != nil {
		return SecondBrainPeopleView{}, ErrSecondBrainUnavailable
	}
	view := SecondBrainPeopleView{People: make([]SecondBrainPersonView, 0, len(listed.People)), Partial: listed.Partial}
	for _, person := range listed.People {
		view.People = append(view.People, SecondBrainPersonView{
			Name: person.Name, Kind: person.Kind,
			Link: secondbrain.ObsidianPathLink(filepath.Join(source.Path, secondbrain.FolderPeople, person.File)),
		})
	}
	return view, nil
}
