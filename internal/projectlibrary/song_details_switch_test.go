package projectlibrary

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func homeConsent(t *testing.T, store workspace.Store, scope Scope) *workspace.SongDetailsConsent {
	t.Helper()
	home, err := store.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	return home.GetAssistantProgramState().GetSongDetailsConsent()
}

func sameConsent(a, b *workspace.SongDetailsConsent) bool {
	return a.GrantedAt.Equal(b.GrantedAt) && a.Source == b.Source && a.Scope == b.Scope && a.OfferID == b.OfferID &&
		(a.RevokedAt == nil) == (b.RevokedAt == nil)
}

// Off removes everything a names consent let Ori store, in the same Home write as
// the switch: the numbers, the track names, the markers and the regions. The
// consent's scope stays as it was, only its switch moves, and what agents are
// given from the library is empty at once.
func TestSetSongDetails_OffClearsTheNamesToo(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	grantNames(t, file, scope)
	scanRoot(t, r, scope, root.ID, "names")
	if single := storedFactsAt(t, r, scope, root.ID, "Single"); single == nil || single.Names == nil {
		t.Fatalf("setup: the names were not read: %+v", single)
	}
	if songs, err := r.library.LearnedSongsInRoot(scope, root.ID); err != nil || len(songs) == 0 {
		t.Fatalf("setup: the folder agent is given %d songs (%v)", len(songs), err)
	}

	if state, err := r.library.SetSongDetails(scope, false, false); err != nil || state != workspace.SongDetailsOff {
		t.Fatalf("off: %q %v", state, err)
	}
	stored := string(storedLibrary(t, file, scope))
	for _, leaked := range []string{"Kick", "Bass", "Intro", "Chorus", "Bridge", "Lead vocal", `"names":{`, `"tempo_bpm"`} {
		if at := strings.Index(stored, leaked); at >= 0 {
			t.Fatalf("switching off left %q stored: …%s…", leaked, stored[max(0, at-80):min(len(stored), at+120)])
		}
	}
	if songs, err := r.library.LearnedSongsInRoot(scope, root.ID); err != nil || len(songs) != 0 {
		t.Fatalf("after off the folder agent is still given %d songs (%v)", len(songs), err)
	}
	consent := homeConsent(t, file, scope)
	if consent.State() != workspace.SongDetailsOff || consent.Scope != workspace.SongDetailsScopeNames {
		t.Fatalf("consent = %+v: off moves the switch only", consent)
	}
}

// Turning the switch on is the owner's new answer: it resumes what they agreed to
// just now. The full wording (names true) makes the consent cover names again; the
// numbers wording (names false) resumes the numbers only, so a switch turned on
// without the question never starts reading names on the strength of an older yes.
func TestSetSongDetails_OnResumesExactlyWhatTheOwnerAgreedTo(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	grantNames(t, file, scope)
	if _, err := r.library.SetSongDetails(scope, false, false); err != nil {
		t.Fatal(err)
	}

	// The numbers wording: on, but not for names.
	if state, err := r.library.SetSongDetails(scope, true, false); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("on (numbers): %q %v", state, err)
	}
	if consent := homeConsent(t, file, scope); consent.CoversNames() || !consent.Active() {
		t.Fatalf("a numbers-only turn-on covers names: %+v", consent)
	}
	scanRoot(t, r, scope, root.ID, "numbers-again")
	if single := storedFactsAt(t, r, scope, root.ID, "Single"); single == nil || single.TempoBPM != 92 || single.Names != nil {
		t.Fatalf("after a numbers-only turn-on: %+v", single)
	}
	if strings.Contains(string(storedLibrary(t, file, scope)), "Kick") {
		t.Fatal("a numbers-only turn-on stored a track name")
	}

	// The full wording: the owner agrees to names, which is a new, broader grant.
	if state, err := r.library.SetSongDetails(scope, true, true); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("on (names): %q %v", state, err)
	}
	consent := homeConsent(t, file, scope)
	if !consent.CoversNames() || consent.Source != workspace.SongDetailsConsentLearnSongs || consent.OfferID != "switch" || consent.RevokedAt != nil {
		t.Fatalf("consent after agreeing to names = %+v", consent)
	}
	scanRoot(t, r, scope, root.ID, "names-again")
	if single := storedFactsAt(t, r, scope, root.ID, "Single"); single == nil || single.Names == nil || len(single.Names.Tracks) != 2 {
		t.Fatalf("after agreeing to names: %+v", single)
	}
}

// A Home that holds the older card's numbers-only consent and is on is widened by
// the same answer, from the switch, with no off in between; asking for nothing
// wider than it has changes nothing.
func TestSetSongDetails_ANumbersOnlyHomeIsWidenedByTheOwnersAnswer(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	before := homeConsent(t, file, scope)
	if before.Source != workspace.SongDetailsConsentFolderOffer || before.CoversNames() {
		t.Fatalf("setup: %+v", before)
	}

	if state, err := r.library.SetSongDetails(scope, true, false); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("on (numbers) on a Home already on: %q %v", state, err)
	}
	if unchanged := homeConsent(t, file, scope); !sameConsent(unchanged, before) {
		t.Fatalf("an answer no wider than the consent changed it: %+v → %+v", before, unchanged)
	}

	if state, err := r.library.SetSongDetails(scope, true, true); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("widening: %q %v", state, err)
	}
	widened := homeConsent(t, file, scope)
	if !widened.CoversNames() || widened.Source != workspace.SongDetailsConsentLearnSongs || widened.GrantedAt.Before(before.GrantedAt) {
		t.Fatalf("widened consent = %+v (was %+v)", widened, before)
	}
	// A repeated answer is one answer.
	if state, err := r.library.SetSongDetails(scope, true, true); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("repeat: %q %v", state, err)
	}
	if again := homeConsent(t, file, scope); !sameConsent(again, widened) {
		t.Fatalf("a repeated widening rewrote the consent: %+v → %+v", widened, again)
	}
	scanRoot(t, r, scope, root.ID, "widened")
	if single := storedFactsAt(t, r, scope, root.ID, "Single"); single == nil || single.Names == nil {
		t.Fatalf("after widening the next scan read no names: %+v", single)
	}
}

// A Home that never agreed to anything has no switch, whatever the body says.
func TestSetSongDetails_NamesCannotGrantWhatWasNeverOffered(t *testing.T) {
	r, scope, _, _, _ := connectedMusicRoot(t)
	if _, err := r.library.SetSongDetails(scope, true, true); err == nil {
		t.Fatal("the switch granted names to a Home that never consented")
	}
}
