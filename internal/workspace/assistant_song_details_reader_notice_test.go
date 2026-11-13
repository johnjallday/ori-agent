package workspace

import (
	"testing"
	"time"
)

// assistant-reads-projects 6.4 (FR46): a Home that approved song details before
// the app plugin read the files is told once and not asked again; a grant made
// now is made under the new words and needs no notice.

func consentWithoutNotice(t *testing.T, store Store, homeID string) {
	t.Helper()
	home, err := store.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	state := home.GetAssistantProgramState()
	state.SongDetailsConsent = &SongDetailsConsent{
		GrantedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), Source: SongDetailsConsentFolderOffer,
		Scope: SongDetailsScopeNames, OfferID: "older-offer",
	}
	home.SetAssistantProgramState(state)
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
}

func TestSongDetailsConsent_ANewGrantNeedsNoNotice(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewSongDetailsConsents(store)
	granted := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	consents.now = func() time.Time { return granted }
	if err := consents.GrantScope(homeID, SongDetailsConsentLearnSongs, "offer-1", SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
	home, _ := store.Get(homeID)
	consent := home.GetAssistantProgramState().GetSongDetailsConsent()
	if consent.ReaderNoticeAt == nil || !consent.ReaderNoticeAt.Equal(granted) {
		t.Fatalf("a grant made now records when the words were shown: %+v", consent.ReaderNoticeAt)
	}
	if consent.NeedsReaderNotice() {
		t.Fatal("a Home that approved under the new words is shown a notice")
	}
	// The setup card's numbers grant is made under the same words.
	other := NewInMemoryStore()
	otherID := consentHome(t, other)
	if err := NewSongDetailsConsents(other).Grant(otherID, "offer-2"); err != nil {
		t.Fatal(err)
	}
	home, _ = other.Get(otherID)
	if home.GetAssistantProgramState().GetSongDetailsConsent().NeedsReaderNotice() {
		t.Fatal("a card grant needs a notice")
	}
}

func TestSongDetailsConsent_AnOlderApprovalIsToldOnceAndNothingElseChanges(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consentWithoutNotice(t, store, homeID)
	home, _ := store.Get(homeID)
	before := home.GetAssistantProgramState().GetSongDetailsConsent()
	if !before.NeedsReaderNotice() {
		t.Fatal("a consent granted before the plugin read has no notice recorded")
	}
	consents := NewSongDetailsConsents(store)
	told := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	consents.now = func() time.Time { return told }
	if err := consents.AcknowledgeReaderNotice(homeID); err != nil {
		t.Fatal(err)
	}
	home, _ = store.Get(homeID)
	after := home.GetAssistantProgramState().GetSongDetailsConsent()
	if after.NeedsReaderNotice() || after.ReaderNoticeAt == nil || !after.ReaderNoticeAt.Equal(told) {
		t.Fatalf("the notice is recorded at the time it was acknowledged: %+v", after)
	}
	// Not the scope, not the source, not the offer, not the switch.
	if after.Scope != before.Scope || after.Source != before.Source || after.OfferID != before.OfferID ||
		!after.GrantedAt.Equal(before.GrantedAt) || after.State() != SongDetailsOn || after.RevokedAt != nil {
		t.Fatalf("acknowledging the notice changed the consent: before %+v after %+v", before, after)
	}
	// Asking twice is a no-op: the Home is not written again.
	version := mustVersion(t, store, homeID)
	consents.now = func() time.Time { return told.Add(time.Hour) }
	if err := consents.AcknowledgeReaderNotice(homeID); err != nil || mustVersion(t, store, homeID) != version {
		t.Fatalf("a second acknowledgement rewrote the Home: %v", err)
	}
	home, _ = store.Get(homeID)
	if !home.GetAssistantProgramState().GetSongDetailsConsent().ReaderNoticeAt.Equal(told) {
		t.Fatal("a second acknowledgement moved the recorded time")
	}
}

func TestSongDetailsConsent_ANoticeNeverPrecedesTheGrant(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consentWithoutNotice(t, store, homeID)
	consents := NewSongDetailsConsents(store)
	consents.now = func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) } // clock behind the grant
	if err := consents.AcknowledgeReaderNotice(homeID); err != nil {
		t.Fatal(err)
	}
	home, _ := store.Get(homeID)
	consent := home.GetAssistantProgramState().GetSongDetailsConsent()
	if consent.ReaderNoticeAt == nil || consent.ReaderNoticeAt.Before(consent.GrantedAt) {
		t.Fatalf("the notice time %v precedes the grant %v", consent.ReaderNoticeAt, consent.GrantedAt)
	}
}

func TestSongDetailsConsent_NoConsentHasNothingToAcknowledge(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewSongDetailsConsents(store)
	version := mustVersion(t, store, homeID)
	// A Home that never approved is not given a consent by acknowledging a notice.
	if err := consents.AcknowledgeReaderNotice(homeID); err != nil {
		t.Fatal(err)
	}
	if state, _ := consents.Read(homeID); state != SongDetailsNone || mustVersion(t, store, homeID) != version {
		t.Fatalf("acknowledging with no consent changed the Home: %q", state)
	}
	if (*SongDetailsConsent)(nil).NeedsReaderNotice() {
		t.Fatal("a missing consent needs a notice")
	}
	if (&SongDetailsConsent{Source: "forged"}).NeedsReaderNotice() {
		t.Fatal("an invalid consent needs a notice")
	}
	if err := consents.AcknowledgeReaderNotice(""); err == nil {
		t.Fatal("an empty Home id was accepted")
	}
	if err := consents.AcknowledgeReaderNotice("no-such-home"); err == nil {
		t.Fatal("an unknown Home was accepted")
	}
}

func TestSongDetailsConsent_ANoticeSurvivesTheSwitchAndTheClone(t *testing.T) {
	granted := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	revoked := granted.Add(time.Hour)
	consent := &SongDetailsConsent{GrantedAt: granted, Source: SongDetailsConsentFolderOffer, ReaderNoticeAt: &granted, RevokedAt: &revoked}
	clone := consent.Clone()
	*clone.ReaderNoticeAt = time.Time{}
	if consent.ReaderNoticeAt.IsZero() {
		t.Fatal("the clone shares the notice time")
	}
	// A Home whose switch is off was still told: it is not shown the notice again
	// when it turns the switch back on.
	if consent.NeedsReaderNotice() {
		t.Fatal("a Home that was told needs the notice")
	}
}
