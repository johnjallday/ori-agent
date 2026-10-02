package workspace

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// SongDetailsConsentFolderOffer is the folder card's Set up: the only place a
// song-details consent is granted. A Home without one never gets it later.
const SongDetailsConsentFolderOffer = "folder_offer"

// ErrSongDetailsConsentInvalid means a song-details consent failed validation.
// A stored one that fails is treated as no consent.
var ErrSongDetailsConsentInvalid = errors.New("song details consent is invalid")

// SongDetailsConsent is the user's agreement, given on the setup card of a new
// Home, that each scan may read three facts (tempo, length, track count) from
// the Home's project files. RevokedAt is the Home's switch turned off;
// clearing it turns the read back on. It lives on AssistantProgramState so it
// travels in the Home envelope and is written under the library's write fence.
type SongDetailsConsent struct {
	GrantedAt time.Time  `json:"granted_at"`
	Source    string     `json:"source"`
	OfferID   string     `json:"offer_id,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Clone returns a deep copy.
func (c *SongDetailsConsent) Clone() *SongDetailsConsent {
	if c == nil {
		return nil
	}
	clone := *c
	if c.RevokedAt != nil {
		value := *c.RevokedAt
		clone.RevokedAt = &value
	}
	return &clone
}

// Validate keeps a consent bounded and well formed.
func (c *SongDetailsConsent) Validate() error {
	invalid := func(what string) error { return fmt.Errorf("%w: %s", ErrSongDetailsConsentInvalid, what) }
	switch {
	case c == nil:
		return invalid("missing")
	case c.GrantedAt.IsZero():
		return invalid("granted_at")
	case c.Source != SongDetailsConsentFolderOffer:
		return invalid("source")
	case !consentText(c.OfferID, 160, true):
		return invalid("offer_id")
	case c.RevokedAt != nil && c.RevokedAt.Before(c.GrantedAt):
		return invalid("revoked_at")
	}
	return nil
}

// Active is true for a valid consent whose switch is on.
func (c *SongDetailsConsent) Active() bool {
	return c != nil && c.RevokedAt == nil && c.Validate() == nil
}

// Song-details states a Home reports: no consent, switched on, switched off.
const (
	SongDetailsNone = "none"
	SongDetailsOn   = "on"
	SongDetailsOff  = "off"
)

// State is SongDetailsNone for a missing or invalid record.
func (c *SongDetailsConsent) State() string {
	switch {
	case c == nil || c.Validate() != nil:
		return SongDetailsNone
	case c.RevokedAt != nil:
		return SongDetailsOff
	default:
		return SongDetailsOn
	}
}

// GetSongDetailsConsent returns the Home's consent, or nil.
func (state *AssistantProgramState) GetSongDetailsConsent() *SongDetailsConsent {
	if state == nil {
		return nil
	}
	return state.SongDetailsConsent.Clone()
}

// SongDetailsConsents grants a Home's song-details consent. Switching it off
// and on clears or keeps stored facts in the same write, so that lives with the
// library (projectlibrary.Store.SetSongDetails), not here.
type SongDetailsConsents struct {
	store Store
	now   func() time.Time
}

// NewSongDetailsConsents binds the consent service to a workspace store.
func NewSongDetailsConsents(store Store) *SongDetailsConsents {
	return &SongDetailsConsents{store: store, now: time.Now}
}

// Read returns the Home's consent state: SongDetailsNone, On or Off.
func (c *SongDetailsConsents) Read(homeID string) (string, error) {
	if c == nil || c.store == nil || strings.TrimSpace(homeID) == "" {
		return "", ErrAssistantStationNotFound
	}
	home, err := c.store.Get(strings.TrimSpace(homeID))
	if err != nil || home == nil || home.GetAssistantProgramState() == nil || home.GetAssistantProjectLink() != nil {
		return "", ErrAssistantStationNotFound
	}
	return home.GetAssistantProgramState().GetSongDetailsConsent().State(), nil
}

// Grant records the consent from a folder card's Set up. A Home that already
// holds a valid record keeps it unchanged: granting twice is one grant, and a
// Home whose switch is off is never turned back on by a later card.
func (c *SongDetailsConsents) Grant(homeID, offerID string) error {
	if c == nil || c.store == nil || strings.TrimSpace(homeID) == "" {
		return ErrAssistantStationNotFound
	}
	next := &SongDetailsConsent{GrantedAt: c.now().UTC(), Source: SongDetailsConsentFolderOffer, OfferID: offerID}
	if err := next.Validate(); err != nil {
		return err
	}
	err := c.store.Update(strings.TrimSpace(homeID), func(home *Workspace) error {
		state := home.GetAssistantProgramState()
		if state == nil || home.GetAssistantProjectLink() != nil {
			return ErrAssistantStationNotFound
		}
		if state.GetSongDetailsConsent().Validate() == nil {
			return errConsentUnchanged
		}
		state.SongDetailsConsent = next.Clone()
		home.SetAssistantProgramState(state)
		return nil
	})
	if errors.Is(err, errConsentUnchanged) {
		return nil
	}
	return err
}
