package filejanitor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// The agent's suggestion, kept (FR 45 to 49).
//
// What the model handed back is untrusted. The server keeps only what it can
// check: a file that is waiting in this review (not marked for Trash, the same
// file that was scanned) and a connection that is on. Everything else is dropped.
// A suggestion belongs to one review: a new scan, a change to any file it names,
// or the connection going off removes it from the picture. It never becomes a
// choice by itself: its card opens unticked, and only the person's tick turns its
// files into sends.

// SendSuggestion is the kept suggestion for one review. No path.
type SendSuggestion struct {
	ID        string          `json:"id"`
	BatchID   string          `json:"batch_id"`
	AgentName string          `json:"agent_name"`
	Paragraph string          `json:"paragraph"`
	Sends     []SuggestedSend `json:"sends,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// SuggestedSend is the files suggested for one connection.
type SuggestedSend struct {
	ConnectionID string          `json:"connection_id"`
	Files        []SuggestedFile `json:"files"`
}

// SuggestedFile names a file by its review identity and the file state it was
// suggested for.
type SuggestedFile struct {
	CandidateID    string `json:"candidate_id"`
	FingerprintKey string `json:"fingerprint_key"`
}

func (s SendSuggestion) validate() error {
	invalid := fmt.Errorf("%w: invalid suggestion record", ErrInvalidCandidate)
	if !strings.HasPrefix(s.ID, "suggestion-") || !managedRootGeneration.MatchString(s.ID) || !managedRootGeneration.MatchString(s.BatchID) ||
		len([]rune(s.Paragraph)) > MaxSuggestionParagraph+1 || len([]rune(s.AgentName)) > 120 || s.CreatedAt.IsZero() ||
		len(s.Sends) > MaxSendConnections {
		return invalid
	}
	seen := map[string]bool{}
	for _, send := range s.Sends {
		if !validConnectionID(send.ConnectionID) || len(send.Files) == 0 {
			return invalid
		}
		for _, file := range send.Files {
			if !managedRootGeneration.MatchString(file.CandidateID) || len(file.FingerprintKey) != 32 || seen[file.CandidateID] {
				return invalid
			}
			seen[file.CandidateID] = true
		}
	}
	if len(seen) > PlanMaxOperations {
		return invalid
	}
	return nil
}

// suggestionFor is the kept suggestion of one review, if any.
func suggestionFor(state ScanState, batchID string) (SendSuggestion, bool) {
	for i := len(state.Suggestions) - 1; i >= 0; i-- {
		if state.Suggestions[i].BatchID == batchID {
			return state.Suggestions[i], true
		}
	}
	return SendSuggestion{}, false
}

// suggestionCurrent reports whether every file a suggestion names is still the
// file it named and still waiting (FR 49). A suggestion with no files is current
// while its review is.
func suggestionCurrent(state ScanState, suggestion SendSuggestion) bool {
	for _, send := range suggestion.Sends {
		for _, file := range send.Files {
			candidate, found := state.Candidate(file.CandidateID)
			if !found || candidate.BatchID != suggestion.BatchID || !proposalPending(candidate) || candidate.Fingerprint.Key() != file.FingerprintKey {
				return false
			}
		}
	}
	return true
}

// suggestionsAvailable reports whether the agent slice is on for this workspace's
// Filing: it uses a model, and a cloud model has the person's consent (FR 42).
func suggestionsAvailable(settings JanitorSettings) bool {
	return settings.ContentMode.ReadsFileContent() && !settings.RequiresContentConsent()
}

// SetSuggestionModel wires how the Filing agent's own model is reached. Without it
// the agent cannot be asked.
func (s *Service) SetSuggestionModel(model SuggestionModel) {
	if s != nil {
		s.suggestionModel = model
	}
}

// AskForSuggestion is "Ask <agent> what to send" (FR 43): one bounded turn of the
// workspace's Filing agent, given the waiting files' names, kinds, sizes and dates
// and the connected workspaces' names, and nothing else. It records what can be
// checked and returns the review's picture of it. It moves nothing and ticks
// nothing.
func (s *Service) AskForSuggestion(ctx context.Context, owner, workspaceID, batchID string) (*PictureSuggestion, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || batchID == "latest" || !managedRootGeneration.MatchString(batchID) {
		return nil, ErrSuggestionUnavailable
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil || !suggestionsAvailable(settings) || s.suggestionModel == nil {
		return nil, ErrSuggestionUnavailable
	}
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil {
		return nil, ErrSuggestionUnavailable
	}
	curator, found := ws.FileCurator()
	if !found || curator == nil {
		return nil, ErrSuggestionUnavailable
	}
	snapshot, err := s.readPictureSnapshot(ctx, owner, workspaceID, batchID)
	if err != nil || snapshot.batchID != batchID {
		return nil, ErrSuggestionUnavailable
	}
	routes := snapshot.view.routes
	if len(routes) == 0 {
		return nil, ErrSuggestionUnavailable
	}
	trashed := map[string]bool{}
	sentOn := map[string]string{} // candidate -> connection it is already a send on
	for _, choice := range snapshot.view.Choices {
		if choice.Operation == OperationTrash {
			trashed[choice.CandidateID] = true
		}
		if choice.ConnectionID != "" {
			sentOn[choice.CandidateID] = choice.ConnectionID
		}
	}

	// Short references stand in for every identity the model sees.
	receivers := make([]suggestionReceiver, 0, len(routes))
	routeByRef := map[string]sendRoute{}
	for i, route := range routes {
		ref := "r" + strconv.Itoa(i+1)
		kinds := make([]string, 0, len(route.Kinds))
		for _, kind := range route.Kinds {
			kinds = append(kinds, kindLabel(kind))
		}
		receivers = append(receivers, suggestionReceiver{ref: ref, name: route.ReceiverName, kinds: kinds})
		routeByRef[ref] = route
	}
	var files []suggestionFile
	candidateByRef := map[string]JanitorCandidate{}
	for _, candidate := range snapshot.candidates {
		if !proposalPending(candidate) || trashed[candidate.ID] || len(files) == MaxSuggestionFilesPrompt {
			continue
		}
		ref := "f" + strconv.Itoa(len(files)+1)
		date := ""
		if !candidate.ModifiedAt.IsZero() {
			date = candidate.ModifiedAt.UTC().Format("2006-01-02")
		}
		files = append(files, suggestionFile{ref: ref, name: candidate.Display(), kind: kindLabel(candidate.EffectiveCategory()), size: candidate.Size, date: date})
		candidateByRef[ref] = candidate
	}
	if len(files) == 0 {
		return nil, ErrSuggestionUnavailable
	}

	chat, model, err := s.suggestionModel(workspaceID, curator.Name)
	if err != nil {
		logger.Warn("Filing agent has no usable model for a suggestion", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		var plain *SuggestionModelError
		if errors.As(err, &plain) && strings.TrimSpace(plain.Sentence) != "" {
			return nil, fmt.Errorf("%w: %s", ErrSuggestionUnavailable, strings.TrimSuffix(strings.TrimSpace(plain.Sentence), "."))
		}
		return nil, fmt.Errorf("%w: %s has no model it can use for this", ErrSuggestionUnavailable, curator.Name)
	}
	answer, err := runSuggestionTurn(ctx, chat, model, curator.Name, buildSuggestionReference(receivers, files))
	if err != nil {
		logger.Warn("Filing agent suggestion turn failed", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		return nil, fmt.Errorf("%w: %s could not be reached. Try again in a moment", ErrSuggestionUnavailable, curator.Name)
	}
	// The same agent must still be the Filing agent after the call.
	if fresh, err := s.readWorkspace(workspaceID); err != nil || fresh == nil {
		return nil, ErrSuggestionUnavailable
	} else if again, ok := fresh.FileCurator(); !ok || again == nil || again.ID != curator.ID {
		return nil, ErrSuggestionUnavailable
	}

	// Keep only what can be checked (FR 46).
	suggestion := SendSuggestion{
		ID: "suggestion-" + uuid.New().String(), BatchID: batchID, AgentName: suggestionParagraph(curator.Name),
		Paragraph: suggestionParagraph(answer.paragraph), CreatedAt: s.clock(),
	}
	taken := map[string]bool{}
	for _, receiver := range receivers {
		route := routeByRef[receiver.ref]
		send := SuggestedSend{ConnectionID: route.ConnectionID}
		for _, ref := range answer.sends[receiver.ref] {
			candidate, ok := candidateByRef[ref]
			// A file already going there needs no suggestion: the card holds only
			// what it would add, so it opens unticked (FR 48).
			if !ok || taken[candidate.ID] || sentOn[candidate.ID] == route.ConnectionID {
				continue
			}
			taken[candidate.ID] = true
			send.Files = append(send.Files, SuggestedFile{CandidateID: candidate.ID, FingerprintKey: candidate.Fingerprint.Key()})
		}
		if len(send.Files) > 0 {
			suggestion.Sends = append(suggestion.Sends, send)
		}
	}
	if len(suggestion.Sends) == 0 {
		suggestion.Paragraph = "" // nothing it could suggest was kept: say "no suggestion", not its words
	}

	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return nil, ErrSuggestionUnavailable
	}
	unlock := lockRoot(root.Path)
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		kept := state.Suggestions[:0:0]
		for _, existing := range state.Suggestions {
			if existing.BatchID != batchID {
				kept = append(kept, existing)
			}
		}
		state.Suggestions = append(kept, suggestion)
		return nil
	})
	unlock()
	if err != nil {
		return nil, err
	}
	picture, err := s.ReadReviewPicture(ctx, owner, workspaceID, batchID)
	if err != nil {
		return nil, err
	}
	if picture.Suggestion == nil {
		return nil, errors.New("the suggestion could not be shown")
	}
	return picture.Suggestion, nil
}

// PictureSuggestion is the agent's suggestion as the picture draws it (FR 47, 48).
// Empty means the agent had no suggestion the server could keep.
type PictureSuggestion struct {
	AgentName string                  `json:"agent_name"`
	Paragraph string                  `json:"paragraph"`
	Empty     bool                    `json:"empty"`
	Cards     []PictureSuggestionCard `json:"cards"`
}

// PictureSuggestionCard is the files suggested for one receiving workspace. Its
// tick is off until the person ticks it: GroupState says how many of its files
// are sends right now.
type PictureSuggestionCard struct {
	ConnectionID string        `json:"connection_id"`
	ReceiverName string        `json:"receiver_name"`
	GroupState   string        `json:"group_state"` // ticked | partial | unticked
	Count        int           `json:"count"`
	Files        []PictureFile `json:"files"`
}

// pictureSuggestion is the kept suggestion of the review, drawn, or nil when there
// is none, the agent slice is off, or it no longer applies (FR 42, 49).
func pictureSuggestion(state ScanState, settings JanitorSettings, view ReviewDraftView, batchID string) *PictureSuggestion {
	if !suggestionsAvailable(settings) || len(view.routes) == 0 {
		return nil
	}
	suggestion, found := suggestionFor(state, batchID)
	if !found || !suggestionCurrent(state, suggestion) {
		return nil
	}
	marked := map[string]string{} // candidate -> connection it is a send on
	for _, choice := range view.Choices {
		if choice.ConnectionID != "" {
			marked[choice.CandidateID] = choice.ConnectionID
		}
	}
	out := &PictureSuggestion{AgentName: suggestion.AgentName, Paragraph: suggestion.Paragraph, Cards: []PictureSuggestionCard{}}
	for _, send := range suggestion.Sends {
		route, on := view.routes.byID(send.ConnectionID)
		if !on {
			return nil // the connection went off: the suggestion no longer applies
		}
		card := PictureSuggestionCard{ConnectionID: route.ConnectionID, ReceiverName: route.ReceiverName, Count: len(send.Files), Files: []PictureFile{}}
		sent := 0
		for _, file := range send.Files {
			candidate, _ := state.Candidate(file.CandidateID)
			if marked[file.CandidateID] == route.ConnectionID {
				sent++
			}
			if len(card.Files) < PictureLeavingFileLimit {
				picture := pictureFileFor(candidate, PictureLeaving)
				picture.Category = candidate.EffectiveCategory()
				card.Files = append(card.Files, picture)
			}
		}
		switch {
		case sent == 0:
			card.GroupState = "unticked"
		case sent < len(send.Files):
			card.GroupState = "partial"
		default:
			card.GroupState = "ticked"
		}
		out.Cards = append(out.Cards, card)
	}
	out.Empty = len(out.Cards) == 0
	if out.Empty {
		out.Paragraph = ""
	}
	return out
}
