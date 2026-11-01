package personalassistant

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// The drawer's one project question (first-week FR20 to FR23): "What's the main
// thing you're working on right now?" The answer is saved as a reviewed fact by
// the interview's own save; this file writes its note, and remembers that the
// question was answered or skipped so the drawer never asks twice.

var (
	// ErrSecondBrainFillInNotAllowed refuses a project note in a second brain
	// whose owner has not yet let Ori write there (the consent card's answer).
	ErrSecondBrainFillInNotAllowed = errors.New("personal assistant: the second brain does not yet let Ori keep project notes")
	// ErrSecondBrainProjectName refuses a name that is not one plain folder name.
	ErrSecondBrainProjectName = errors.New("personal assistant: that is not a project name")
)

const (
	secondBrainActionProject     = "project"
	secondBrainActionProjectSkip = "project_skip"
)

// SecondBrainProjectInput is the answer and the name to give its note.
type SecondBrainProjectInput struct {
	Name      string
	Answer    string
	RequestID string
}

// SecondBrainProjectNote is the note the answer wrote.
type SecondBrainProjectNote struct {
	Name string `json:"name"`
	// Rel is Projects/<name>/README.md, below the notes folder.
	Rel string `json:"rel"`
	// Link opens the note in Obsidian; a link only.
	Link string `json:"link,omitempty"`
}

// WriteProject writes Projects/<name>/README.md with the answer as its first
// paragraph, in a second brain Ori set up whose owner has allowed project notes.
// It replaces nothing the user wrote: a README already there that is not exactly
// as Ori wrote it is ErrUserModified. A replay of the same request returns the
// note the first call wrote. A workspace made later with this name replaces the
// note with its own README, the same file, through the ordinary sync.
func (s *SecondBrainService) WriteProject(ctx context.Context, userID string, input SecondBrainProjectInput) (SecondBrainProjectNote, error) {
	if !secondbrain.ValidRequestID(input.RequestID) {
		return SecondBrainProjectNote{}, ErrValidation
	}
	name := strings.TrimSpace(input.Name)
	rel, ok := secondbrain.ProjectNoteRel(name)
	if !ok {
		return SecondBrainProjectNote{}, ErrSecondBrainProjectName
	}
	answer, ok := secondbrain.ProjectAnswerLine(input.Answer)
	if !ok {
		return SecondBrainProjectNote{}, ErrValidation
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainProjectNote{}, err
	}
	source := doc.Connected()
	if source == nil {
		return SecondBrainProjectNote{}, ErrSecondBrainNotConnected
	}
	view := projectView(*source, name, rel)
	if _, replayed, err := doc.Replay(input.RequestID, secondBrainActionProject); err != nil || replayed {
		return view, err
	}
	writer, err := secondbrain.NewWriter(source)
	if err != nil {
		return SecondBrainProjectNote{}, err
	}
	if !source.FillIn.Allowed() {
		return SecondBrainProjectNote{}, ErrSecondBrainFillInNotAllowed
	}
	now := s.deps.Now()
	body := secondbrain.ProjectAnswerNote(name, answer, now.Format("2 Jan 2006"))
	if _, err := writer.Write(rel, secondbrain.NoteKindProject, body); err != nil {
		return SecondBrainProjectNote{}, err
	}
	s.dropInboxCache()
	if _, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, replayed, err := doc.Replay(input.RequestID, secondBrainActionProject); err != nil || replayed {
			return err
		}
		current := doc.Connected()
		if current == nil || current.ID != source.ID {
			return nil
		}
		if current.FillIn == nil {
			current.FillIn = &secondbrain.FillIn{}
		}
		at := now.UTC()
		current.FillIn.ProjectAsk, current.FillIn.ProjectAskAt = secondbrain.ProjectAnswered, &at
		current.FillIn.AddToReport(map[string]int{secondbrain.NoteKindProject: 1}, now)
		doc.Remember(secondbrain.Receipt{RequestID: input.RequestID, Action: secondBrainActionProject, At: now})
		return nil
	}); err != nil {
		// The note is on disk; a retry with this id writes the same note again.
		return view, err
	}
	s.wrote(ctx, userID, map[string]int{secondbrain.NoteKindProject: 1})
	return view, nil
}

func projectView(source secondbrain.Source, name, rel string) SecondBrainProjectNote {
	view := SecondBrainProjectNote{Name: name, Rel: rel}
	if source.CreatedByOri() {
		view.Link = secondbrain.ObsidianPathLink(filepath.Join(source.Path, filepath.FromSlash(rel)))
	}
	return view
}

// SkipProject records that the user skipped the question: it is not asked again
// in the drawer, and stays available on the Profile page (FR23). Skipping after
// answering changes nothing.
func (s *SecondBrainService) SkipProject(ctx context.Context, userID, requestID string) (SecondBrainView, error) {
	if !secondbrain.ValidRequestID(requestID) {
		return SecondBrainView{}, ErrValidation
	}
	_, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, replayed, err := doc.Replay(requestID, secondBrainActionProjectSkip); err != nil || replayed {
			return err
		}
		connected := doc.Connected()
		if connected == nil {
			return ErrSecondBrainNotConnected
		}
		if !connected.CreatedByOri() {
			return ErrSecondBrainNotOwned
		}
		if connected.FillIn == nil {
			connected.FillIn = &secondbrain.FillIn{}
		}
		if connected.FillIn.ProjectAsk == "" {
			at := s.deps.Now().UTC()
			connected.FillIn.ProjectAsk, connected.FillIn.ProjectAskAt = secondbrain.ProjectSkipped, &at
		}
		doc.Remember(secondbrain.Receipt{RequestID: requestID, Action: secondBrainActionProjectSkip, At: s.deps.Now()})
		return nil
	})
	if err != nil {
		return SecondBrainView{}, err
	}
	return s.Status(ctx, userID)
}
