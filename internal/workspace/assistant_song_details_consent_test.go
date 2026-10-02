package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSongDetailsConsentValidate(t *testing.T) {
	granted := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	earlier := granted.Add(-time.Hour)
	later := granted.Add(time.Hour)
	cases := []struct {
		name    string
		consent *SongDetailsConsent
		state   string
	}{
		{"granted from a card", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentFolderOffer, OfferID: "offer-1"}, SongDetailsOn},
		{"switched off", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentFolderOffer, RevokedAt: &later}, SongDetailsOff},
		{"missing", nil, SongDetailsNone},
		{"no grant time", &SongDetailsConsent{Source: SongDetailsConsentFolderOffer}, SongDetailsNone},
		{"another source", &SongDetailsConsent{GrantedAt: granted, Source: "home_switch"}, SongDetailsNone},
		{"an offer id with a newline", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentFolderOffer, OfferID: "a\nb"}, SongDetailsNone},
		{"an oversized offer id", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentFolderOffer, OfferID: strings.Repeat("a", 161)}, SongDetailsNone},
		{"revoked before granted", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentFolderOffer, RevokedAt: &earlier}, SongDetailsNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.consent.State(); got != tc.state {
				t.Fatalf("State() = %q, want %q", got, tc.state)
			}
			valid := tc.state != SongDetailsNone
			if err := tc.consent.Validate(); (err == nil) != valid || (err != nil && !errors.Is(err, ErrSongDetailsConsentInvalid)) {
				t.Fatalf("Validate() = %v, want valid=%v", err, valid)
			}
			// An invalid stored record counts as no consent.
			if tc.consent.Active() != (tc.state == SongDetailsOn) {
				t.Fatalf("Active() = %v for state %q", tc.consent.Active(), tc.state)
			}
		})
	}
}

func TestSongDetailsConsent_HomeStateRoundTrip(t *testing.T) {
	revoked := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	state := &AssistantProgramState{SchemaVersion: AssistantProgramStateSchemaVersion, StateRevision: 4,
		SongDetailsConsent: &SongDetailsConsent{GrantedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
			Source: SongDetailsConsentFolderOffer, OfferID: "offer-1", RevokedAt: &revoked}}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"song_details_consent":{"granted_at":"2026-10-02T00:00:00Z","source":"folder_offer","offer_id":"offer-1","revoked_at":"2026-10-03T00:00:00Z"}`) {
		t.Fatalf("encoded state = %s", encoded)
	}
	var decoded AssistantProgramState
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetSongDetailsConsent().State() != SongDetailsOff || decoded.SongDetailsConsent.OfferID != "offer-1" {
		t.Fatalf("decoded consent = %+v", decoded.SongDetailsConsent)
	}
	clone := CloneAssistantProgramState(state)
	*clone.SongDetailsConsent.RevokedAt = time.Time{}
	clone.SongDetailsConsent.OfferID = "changed"
	if state.SongDetailsConsent.RevokedAt.IsZero() || state.SongDetailsConsent.OfferID != "offer-1" {
		t.Fatalf("clone shares the consent: %+v", state.SongDetailsConsent)
	}
}

func TestSongDetailsConsent_AHomeWithoutItIsUnchanged(t *testing.T) {
	state := &AssistantProgramState{SchemaVersion: AssistantProgramStateSchemaVersion, StateRevision: 4}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "song_details") {
		t.Fatalf("a Home without the consent writes it: %s", encoded)
	}
	if CloneAssistantProgramState(state).SongDetailsConsent != nil || state.GetSongDetailsConsent().State() != SongDetailsNone {
		t.Fatal("a Home without the consent reads as having one")
	}
}

func TestSongDetailsConsentsGrant(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewSongDetailsConsents(store)
	if state, err := consents.Read(homeID); err != nil || state != SongDetailsNone {
		t.Fatalf("a new Home: %q %v", state, err)
	}
	if err := consents.Grant(homeID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	if state, err := consents.Read(homeID); err != nil || state != SongDetailsOn {
		t.Fatalf("after the grant: %q %v", state, err)
	}
	// Granting again is the same grant: the Home is not rewritten.
	version := mustVersion(t, store, homeID)
	if err := consents.Grant(homeID, "offer-2"); err != nil || mustVersion(t, store, homeID) != version {
		t.Fatalf("a second grant rewrote the Home: %v", err)
	}
	// A Home whose switch is off is not turned back on by a later grant.
	home, _ := store.Get(homeID)
	state := home.GetAssistantProgramState()
	off := state.SongDetailsConsent.GrantedAt.Add(time.Minute)
	state.SongDetailsConsent.RevokedAt = &off
	home.SetAssistantProgramState(state)
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	if err := consents.Grant(homeID, "offer-3"); err != nil {
		t.Fatal(err)
	}
	if got, _ := consents.Read(homeID); got != SongDetailsOff {
		t.Fatalf("a later grant turned the switch back on: %q", got)
	}
	// An unreadable record is replaced by the grant.
	home, _ = store.Get(homeID)
	state = home.GetAssistantProgramState()
	state.SongDetailsConsent = &SongDetailsConsent{Source: "forged"}
	home.SetAssistantProgramState(state)
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	if got, _ := consents.Read(homeID); got != SongDetailsNone {
		t.Fatalf("a forged record reads as %q", got)
	}
	if err := consents.Grant(homeID, "offer-4"); err != nil {
		t.Fatal(err)
	}
	if got, _ := consents.Read(homeID); got != SongDetailsOn {
		t.Fatalf("after replacing a forged record: %q", got)
	}
}

func TestSongDetailsConsentsRefuseAProjectAndBadGrants(t *testing.T) {
	store := NewInMemoryStore()
	consents := NewSongDetailsConsents(store)
	if err := consents.Grant("missing", "offer-1"); err == nil {
		t.Fatal("granted on a missing Home")
	}
	homeID := consentHome(t, store)
	if err := consents.Grant(homeID, "bad\noffer"); !errors.Is(err, ErrSongDetailsConsentInvalid) {
		t.Fatalf("bad offer id: %v", err)
	}
	if got, _ := consents.Read(homeID); got != SongDetailsNone {
		t.Fatalf("a refused grant wrote %q", got)
	}
}
