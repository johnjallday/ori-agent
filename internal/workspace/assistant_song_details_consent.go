package workspace

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Where a song-details consent comes from. Both are a person's answer to an
// offer; a Home without one is never granted it by anything else.
const (
	// SongDetailsConsentFolderOffer is the older folder card's Set up. It
	// covers numbers only (tempo, length, track count).
	SongDetailsConsentFolderOffer = "folder_offer"
	// SongDetailsConsentLearnSongs is the folder agent's offer to learn the
	// songs, made once its Set it up run has finished.
	SongDetailsConsentLearnSongs = "learn_songs"
)

// What a song-details consent covers. A record with no scope, as every record
// written before the scope existed, covers numbers only: widening it to names
// is a new grant the person answers, never something an old record implies.
const (
	SongDetailsScopeNumbers = "numbers"
	SongDetailsScopeNames   = "names"
)

// ErrSongDetailsConsentInvalid means a song-details consent failed validation.
// A stored one that fails is treated as no consent.
var ErrSongDetailsConsentInvalid = errors.New("song details consent is invalid")

// ErrSongDetailsConsentRevoked means the Home's consent exists but its switch
// is off. A grant never turns it back on; the Home's switch does.
var ErrSongDetailsConsentRevoked = errors.New("song details consent is switched off")

// SongDetailsConsent is the user's agreement that each scan may read details
// from the Home's project files: numbers (tempo, length, track count) and,
// when Scope is names, the track names, markers and regions the person typed
// into each project. RevokedAt is the Home's switch turned off; clearing it
// turns the read back on. It lives on AssistantProgramState so it travels in
// the Home envelope and is written under the library's write fence.
type SongDetailsConsent struct {
	GrantedAt time.Time `json:"granted_at"`
	Source    string    `json:"source"`
	// Scope is SongDetailsScopeNumbers or SongDetailsScopeNames; empty reads as
	// numbers.
	Scope     string     `json:"scope,omitempty"`
	OfferID   string     `json:"offer_id,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// ReaderNoticeAt is when the owner was told that the app plugin, not Ori
	// itself, now reads the project files (assistant-reads-projects FR46). A
	// consent granted under those words carries its grant time; one granted
	// before has none, so its Home shows the notice once and is not asked again.
	ReaderNoticeAt *time.Time `json:"reader_notice_at,omitempty"`
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
	if c.ReaderNoticeAt != nil {
		value := *c.ReaderNoticeAt
		clone.ReaderNoticeAt = &value
	}
	return &clone
}

// NeedsReaderNotice is true for a valid consent whose Home was never told that
// the app plugin now reads the files.
func (c *SongDetailsConsent) NeedsReaderNotice() bool {
	return c != nil && c.Validate() == nil && c.ReaderNoticeAt == nil
}

// Validate keeps a consent bounded and well formed.
func (c *SongDetailsConsent) Validate() error {
	invalid := func(what string) error { return fmt.Errorf("%w: %s", ErrSongDetailsConsentInvalid, what) }
	switch {
	case c == nil:
		return invalid("missing")
	case c.GrantedAt.IsZero():
		return invalid("granted_at")
	case c.Source != SongDetailsConsentFolderOffer && c.Source != SongDetailsConsentLearnSongs:
		return invalid("source")
	case c.Scope != "" && c.Scope != SongDetailsScopeNumbers && c.Scope != SongDetailsScopeNames:
		return invalid("scope")
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

// CoversNames is true for an active consent that reaches track names, markers
// and regions. A numbers-only or scope-less record never does.
func (c *SongDetailsConsent) CoversNames() bool {
	return c.Active() && c.Scope == SongDetailsScopeNames
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
	state, _, err := c.ReadScope(homeID)
	return state, err
}

// ReadScope returns the Home's consent state and whether an active consent also
// reaches track names, markers and regions. A switched-off or missing consent
// reaches none.
func (c *SongDetailsConsents) ReadScope(homeID string) (state string, names bool, err error) {
	if c == nil || c.store == nil || strings.TrimSpace(homeID) == "" {
		return "", false, ErrAssistantStationNotFound
	}
	home, err := c.store.Get(strings.TrimSpace(homeID))
	if err != nil || home == nil || home.GetAssistantProgramState() == nil || home.GetAssistantProjectLink() != nil {
		return "", false, ErrAssistantStationNotFound
	}
	consent := home.GetAssistantProgramState().GetSongDetailsConsent()
	return consent.State(), consent.CoversNames(), nil
}

// Grant records the consent from a folder card's Set up, for numbers only. A
// Home that already holds a valid record keeps it unchanged: granting twice is
// one grant, and a Home whose switch is off is never turned back on by a later
// card.
func (c *SongDetailsConsents) Grant(homeID, offerID string) error {
	err := c.GrantScope(homeID, SongDetailsConsentFolderOffer, offerID, SongDetailsScopeNumbers)
	if errors.Is(err, ErrSongDetailsConsentRevoked) {
		return nil
	}
	return err
}

// AcknowledgeReaderNotice records that the owner saw the notice that the app
// plugin now reads their project files. It changes nothing else: not the scope,
// not the switch. A Home already told, or with no consent, is left as it is.
func (c *SongDetailsConsents) AcknowledgeReaderNotice(homeID string) error {
	if c == nil || c.store == nil || strings.TrimSpace(homeID) == "" {
		return ErrAssistantStationNotFound
	}
	err := c.store.Update(strings.TrimSpace(homeID), func(home *Workspace) error {
		state := home.GetAssistantProgramState()
		if state == nil || home.GetAssistantProjectLink() != nil {
			return ErrAssistantStationNotFound
		}
		current := state.GetSongDetailsConsent()
		if !current.NeedsReaderNotice() {
			return errConsentUnchanged
		}
		at := c.now().UTC()
		if at.Before(current.GrantedAt) {
			at = current.GrantedAt
		}
		current.ReaderNoticeAt = &at
		state.SongDetailsConsent = current
		home.SetAssistantProgramState(state)
		return nil
	})
	if errors.Is(err, errConsentUnchanged) {
		return nil
	}
	return err
}

// GrantScope records a person's answer to an offer to read song details, at the
// given scope (empty reads as numbers).
//
//   - No record, or an invalid one: the grant is written.
//   - A record whose switch is off: ErrSongDetailsConsentRevoked, nothing
//     changes. Only the Home's switch turns reading back on.
//   - An active record that already covers the scope: nothing changes, so
//     granting twice is one grant.
//   - An active numbers record asked for names: the upgrade. It is written as a
//     new, broader grant (source, offer and time), not as a second provenance.
func (c *SongDetailsConsents) GrantScope(homeID, source, offerID, scope string) error {
	if c == nil || c.store == nil || strings.TrimSpace(homeID) == "" {
		return ErrAssistantStationNotFound
	}
	if scope == "" {
		scope = SongDetailsScopeNumbers
	}
	granted := c.now().UTC()
	// A grant made now is made under the words that say the app plugin reads the
	// files, so it needs no notice.
	next := &SongDetailsConsent{GrantedAt: granted, Source: source, Scope: scope, OfferID: offerID, ReaderNoticeAt: &granted}
	if err := next.Validate(); err != nil {
		return err
	}
	err := c.store.Update(strings.TrimSpace(homeID), func(home *Workspace) error {
		state := home.GetAssistantProgramState()
		if state == nil || home.GetAssistantProjectLink() != nil {
			return ErrAssistantStationNotFound
		}
		current := state.GetSongDetailsConsent()
		if current.Validate() == nil {
			switch {
			case current.RevokedAt != nil:
				return ErrSongDetailsConsentRevoked
			case scope == SongDetailsScopeNumbers || current.CoversNames():
				return errConsentUnchanged
			}
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
