package projectlibrary

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrSongDetailsNotGranted means the Home never agreed to song details (it was
// set up before them, or step by step): its switch cannot be turned on.
var ErrSongDetailsNotGranted = errors.New("this Home never agreed to read song details")

var errSongDetailsUnchanged = errors.New("song details unchanged")

// SongDetailsState is the Home's switch: workspace.SongDetailsNone, On or Off.
// It reads the Home only; the library need not be initialized.
func (s *Store) SongDetailsState(scope Scope) (string, error) {
	if s == nil || s.workspaces == nil || !scope.valid() {
		return "", ErrUnavailable
	}
	home, err := s.workspaces.Get(scope.HomeID)
	if err != nil {
		return "", unavailable(ReasonHomeUnavailable)
	}
	state, err := s.home(scope, home)
	if err != nil {
		return "", err
	}
	return state.GetSongDetailsConsent().State(), nil
}

// SetSongDetails turns the Home's song-details switch off or on (D6) in one
// fenced Home write. Off records revoked_at and clears every stored song fact
// (the numbers, the track names, markers and regions), and the collection brief
// written from them, in that same write, so nothing read under the consent
// outlives it; the next scan opens no file. The consent's scope is left as it
// was, only its switch moves.
//
// On starts no scan: facts return on the next one. It is the owner's new answer,
// so it resumes exactly what they have just agreed to and no more: names true is
// the full wording (numbers, track names, markers and regions) and makes the
// consent cover names, a new and broader grant when it did not before; names
// false resumes the numbers only (tempo, length, track count). A consent that is
// already on at that scope changes nothing. On needs a Home that consented at
// setup (ErrSongDetailsNotGranted otherwise). Off is reductive and stays possible
// on a read-only Home; on needs a writable one.
func (s *Store) SetSongDetails(scope Scope, enabled, names bool) (string, error) {
	if s == nil || s.workspaces == nil || !scope.valid() {
		return "", ErrUnavailable
	}
	result := ""
	err := s.workspaces.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state, err := s.home(scope, home)
		if err != nil {
			return err
		}
		consent := state.GetSongDetailsConsent()
		if consent.State() == workspace.SongDetailsNone {
			return ErrSongDetailsNotGranted
		}
		if enabled {
			if !s.providerWritable(scope, home) {
				return ErrUnavailable
			}
			if consent.RevokedAt == nil && (!names || consent.CoversNames()) {
				result = workspace.SongDetailsOn
				return errSongDetailsUnchanged
			}
			now := s.now().UTC()
			if now.Before(consent.GrantedAt) {
				now = consent.GrantedAt
			}
			// The owner's answer to the switch's own question, written as a grant
			// at the scope they agreed to; an older record's source and time never
			// stand for a wider one.
			next := &workspace.SongDetailsConsent{GrantedAt: now, Source: consent.Source, Scope: workspace.SongDetailsScopeNumbers, OfferID: consent.OfferID, ReaderNoticeAt: consent.ReaderNoticeAt}
			if names {
				next.Source, next.Scope, next.OfferID = workspace.SongDetailsConsentLearnSongs, workspace.SongDetailsScopeNames, "switch"
				// The full question names the plugin that reads and says Ori keeps
				// only the listed facts, so this owner was told in the answer itself.
				next.ReaderNoticeAt = &now
			}
			consent = next
		} else {
			cleared := 0
			if len(state.ProjectLibrary) != 0 {
				library, count, err := s.clearedFacts(scope, state)
				if err != nil {
					return err
				}
				state.ProjectLibrary, cleared = library, count
			}
			if consent.RevokedAt != nil && cleared == 0 {
				result = workspace.SongDetailsOff
				return errSongDetailsUnchanged
			}
			if consent.RevokedAt == nil {
				now := s.now().UTC()
				if now.Before(consent.GrantedAt) {
					now = consent.GrantedAt
				}
				consent.RevokedAt = &now
			}
		}
		if err := consent.Validate(); err != nil {
			return err
		}
		state.SongDetailsConsent = consent
		home.SetAssistantProgramState(state)
		result = consent.State()
		return nil
	})
	switch {
	case errors.Is(err, errSongDetailsUnchanged):
		return result, nil
	case err == nil:
		return result, nil
	case errors.Is(err, ErrSongDetailsNotGranted), errors.Is(err, ErrUnavailable), errors.Is(err, ErrCorrupt),
		errors.Is(err, ErrLimit), errors.Is(err, ErrMirrorDiverged), errors.Is(err, workspace.ErrSongDetailsConsentInvalid):
		return "", err
	case errors.Is(err, workspace.ErrStaleWorkspaceVersion):
		return "", fmt.Errorf("%w: %w", ErrConflict, err)
	default:
		return "", fmt.Errorf("persist song details: %w", err)
	}
}

// clearedFacts is the Home's library document with every observation's facts
// removed and its revision moved on, revalidated before it is stored, and how
// many it removed. With none to remove the document is returned unchanged.
func (s *Store) clearedFacts(scope Scope, state *workspace.AssistantProgramState) (json.RawMessage, int, error) {
	doc, err := decodeDocument(state.ProjectLibrary, scope)
	if err != nil || !inactiveRootsValid(state, doc) {
		return nil, 0, ErrCorrupt
	}
	cleared := 0
	for i := range doc.Entries {
		for j := range doc.Entries[i].Observations {
			if doc.Entries[i].Observations[j].Facts != nil {
				doc.Entries[i].Observations[j].Facts = nil
				cleared++
			}
		}
	}
	// The collection brief was written from those facts: it goes with them.
	if doc.CollectionBrief != nil {
		doc.CollectionBrief = nil
		cleared++
	}
	if cleared == 0 {
		return state.ProjectLibrary, 0, nil
	}
	doc.Revision++
	if !doc.valid(scope) {
		return nil, 0, ErrCorrupt
	}
	encoded, err := json.Marshal(doc)
	if err != nil || len(encoded) > maxDocumentBytes {
		return nil, 0, ErrLimit
	}
	return encoded, cleared, nil
}
