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

func TestSongDetailsConsent_ALegacyRecordReadsAsNumbersOnly(t *testing.T) {
	// A record written before the scope existed has no scope field.
	var state AssistantProgramState
	legacy := `{"song_details_consent":{"granted_at":"2026-10-02T00:00:00Z","source":"folder_offer","offer_id":"offer-1"}}`
	if err := json.Unmarshal([]byte(legacy), &state); err != nil {
		t.Fatal(err)
	}
	consent := state.GetSongDetailsConsent()
	if consent.State() != SongDetailsOn || !consent.Active() {
		t.Fatalf("a legacy record stopped being on: %+v", consent)
	}
	if consent.CoversNames() {
		t.Fatal("a legacy record without a scope reaches names")
	}
	// Writing it back does not invent a scope.
	encoded, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"scope"`) {
		t.Fatalf("a legacy record gained a scope: %s", encoded)
	}
}

func TestSongDetailsConsent_SourceAndScopeValidate(t *testing.T) {
	granted := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	later := granted.Add(time.Hour)
	cases := []struct {
		name  string
		c     *SongDetailsConsent
		state string
		names bool
	}{
		{"learn songs, numbers", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentLearnSongs, Scope: SongDetailsScopeNumbers}, SongDetailsOn, false},
		{"learn songs, names", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentLearnSongs, Scope: SongDetailsScopeNames}, SongDetailsOn, true},
		{"card record, names", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentFolderOffer, Scope: SongDetailsScopeNames}, SongDetailsOn, true},
		{"learn songs, no scope", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentLearnSongs}, SongDetailsOn, false},
		{"an unknown scope", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentLearnSongs, Scope: "everything"}, SongDetailsNone, false},
		{"names, switched off", &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentLearnSongs, Scope: SongDetailsScopeNames, RevokedAt: &later}, SongDetailsOff, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.State(); got != tc.state {
				t.Fatalf("State() = %q, want %q", got, tc.state)
			}
			if got := tc.c.CoversNames(); got != tc.names {
				t.Fatalf("CoversNames() = %v, want %v", got, tc.names)
			}
		})
	}
	var none *SongDetailsConsent
	if none.CoversNames() {
		t.Fatal("a missing consent reaches names")
	}
}

func TestSongDetailsConsentsGrantScope(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewSongDetailsConsents(store)
	read := func() *SongDetailsConsent {
		t.Helper()
		home, err := store.Get(homeID)
		if err != nil {
			t.Fatal(err)
		}
		return home.GetAssistantProgramState().GetSongDetailsConsent()
	}

	// The folder agent's offer, answered yes: numbers, with its own source.
	if err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "folder-agent:ws-1", SongDetailsScopeNumbers); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.Source != SongDetailsConsentLearnSongs || got.Scope != SongDetailsScopeNumbers || got.OfferID != "folder-agent:ws-1" || got.CoversNames() {
		t.Fatalf("after the numbers grant: %+v", got)
	}

	// Asking again for what is already covered is one grant: not rewritten.
	version := mustVersion(t, store, homeID)
	if err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "folder-agent:ws-2", SongDetailsScopeNumbers); err != nil || mustVersion(t, store, homeID) != version {
		t.Fatalf("a repeated numbers grant rewrote the Home: %v", err)
	}
	// An empty scope means numbers.
	if err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "folder-agent:ws-2", ""); err != nil || mustVersion(t, store, homeID) != version {
		t.Fatalf("an empty scope rewrote the Home: %v", err)
	}

	// The upgrade: a numbers record asked for names becomes one broader grant.
	if err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "folder-agent:ws-3", SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
	got = read()
	if !got.CoversNames() || got.OfferID != "folder-agent:ws-3" || got.RevokedAt != nil {
		t.Fatalf("after the upgrade: %+v", got)
	}

	// Names already covers numbers: asking for numbers does not narrow it.
	version = mustVersion(t, store, homeID)
	if err := consents.GrantScope(homeID, SongDetailsConsentFolderOffer, "offer-9", SongDetailsScopeNumbers); err != nil || mustVersion(t, store, homeID) != version {
		t.Fatalf("a numbers grant narrowed or rewrote a names consent: %v", err)
	}
	if !read().CoversNames() {
		t.Fatal("a numbers grant narrowed a names consent")
	}

	// The switch off: the scope is kept but reaches nothing, and no grant turns
	// it back on.
	home, _ := store.Get(homeID)
	state := home.GetAssistantProgramState()
	off := state.SongDetailsConsent.GrantedAt.Add(time.Minute)
	state.SongDetailsConsent.RevokedAt = &off
	home.SetAssistantProgramState(state)
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Scope != SongDetailsScopeNames || got.CoversNames() || got.Active() || got.State() != SongDetailsOff {
		t.Fatalf("a switched-off names consent still reaches names: %+v", got)
	}
	version = mustVersion(t, store, homeID)
	err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "folder-agent:ws-4", SongDetailsScopeNames)
	if !errors.Is(err, ErrSongDetailsConsentRevoked) || mustVersion(t, store, homeID) != version {
		t.Fatalf("a grant touched a switched-off Home: %v", err)
	}
	if got := read(); got.State() != SongDetailsOff || got.OfferID != "folder-agent:ws-3" {
		t.Fatalf("a grant changed a switched-off consent: %+v", got)
	}
	// The older card path keeps its quiet behaviour on a switched-off Home.
	if err := consents.Grant(homeID, "offer-5"); err != nil {
		t.Fatalf("Grant on a switched-off Home = %v, want nil", err)
	}
}

func TestSongDetailsConsentsReadScope(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewSongDetailsConsents(store)
	check := func(label, wantState string, wantNames bool) {
		t.Helper()
		state, names, err := consents.ReadScope(homeID)
		if err != nil || state != wantState || names != wantNames {
			t.Fatalf("%s: ReadScope = %q names=%v err=%v, want %q names=%v", label, state, names, err, wantState, wantNames)
		}
	}
	check("a new Home", SongDetailsNone, false)
	if err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "o-1", SongDetailsScopeNumbers); err != nil {
		t.Fatal(err)
	}
	check("numbers", SongDetailsOn, false)
	if err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "o-2", SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
	check("names", SongDetailsOn, true)
	home, _ := store.Get(homeID)
	state := home.GetAssistantProgramState()
	off := state.SongDetailsConsent.GrantedAt.Add(time.Minute)
	state.SongDetailsConsent.RevokedAt = &off
	home.SetAssistantProgramState(state)
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	check("switched off", SongDetailsOff, false)
	if _, _, err := consents.ReadScope("missing"); err == nil {
		t.Fatal("read the consent of a missing Home")
	}
}

func TestSongDetailsConsentsGrantScopeRefusesBadInput(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewSongDetailsConsents(store)
	for name, args := range map[string][3]string{
		"unknown source": {"home_switch", "offer-1", SongDetailsScopeNumbers},
		"unknown scope":  {SongDetailsConsentLearnSongs, "offer-1", "everything"},
		"bad offer id":   {SongDetailsConsentLearnSongs, "bad\noffer", SongDetailsScopeNumbers},
	} {
		if err := consents.GrantScope(homeID, args[0], args[1], args[2]); !errors.Is(err, ErrSongDetailsConsentInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if got, _ := consents.Read(homeID); got != SongDetailsNone {
		t.Fatalf("a refused grant wrote %q", got)
	}
	if err := consents.GrantScope("missing", SongDetailsConsentLearnSongs, "offer-1", SongDetailsScopeNumbers); err == nil {
		t.Fatal("granted on a missing Home")
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
