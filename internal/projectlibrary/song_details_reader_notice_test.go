package projectlibrary

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assistant-reads-projects 6.4 (FR44-FR46): the approval's words name the app
// plugin that reads; a Home that approved before is told once.

func TestReaderDisplayName_IsTheReviewedListsNameForTheReadingPlugin(t *testing.T) {
	reviewed := reviewedPluginID(t)
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok || entry.DisplayName == "" {
		t.Fatalf("no reviewed display name: %+v", entry)
	}
	if got := ReaderDisplayName([]plugin.InstalledPlugin{readerPlugin(reviewed, true, 262144, "zzfmt")}); got != entry.DisplayName {
		t.Fatalf("name = %q, want %q", got, entry.DisplayName)
	}
	for name, installed := range map[string][]plugin.InstalledPlugin{
		"nothing installed":       nil,
		"a plugin with no reader": {{Name: reviewed, Enabled: true}},
		"a disabled reader":       {readerPlugin(reviewed, false, 262144, "zzfmt")},
		"an unreviewed plugin":    {readerPlugin("someone-elses-plugin", true, 262144, "zzfmt")},
		"no room for the reply":   {readerPlugin(reviewed, true, 1024, "zzfmt")},
	} {
		if got := ReaderDisplayName(installed); got != "" {
			t.Errorf("%s: name = %q, want none", name, got)
		}
	}
}

// Turning the switch on again is the owner's answer to the question that names
// the plugin: it is not followed by the notice. A Home that was never told and
// resumes numbers only (no question) still is.
func TestSetSongDetails_TheQuestionThatNamesThePluginIsTheNotice(t *testing.T) {
	r, scope, file, _, _ := connectedMusicRoot(t)
	grantNames(t, file, scope)
	clearReaderNotice(t, file, scope)
	if !homeConsent(t, file, scope).NeedsReaderNotice() {
		t.Fatal("setup: the older consent should still need the notice")
	}
	if _, err := r.library.SetSongDetails(scope, false, false); err != nil {
		t.Fatal(err)
	}
	if !homeConsent(t, file, scope).NeedsReaderNotice() {
		t.Fatal("switching off told the owner something it did not")
	}
	if _, err := r.library.SetSongDetails(scope, true, false); err != nil {
		t.Fatal(err)
	}
	if !homeConsent(t, file, scope).NeedsReaderNotice() {
		t.Fatal("resuming numbers without the question counted as a notice")
	}
	if _, err := r.library.SetSongDetails(scope, true, true); err != nil {
		t.Fatal(err)
	}
	consent := homeConsent(t, file, scope)
	if consent.NeedsReaderNotice() || consent.ReaderNoticeAt == nil || consent.ReaderNoticeAt.Before(consent.GrantedAt) {
		t.Fatalf("the full question is the notice: %+v", consent)
	}
	// Off and on again keeps what was recorded.
	if _, err := r.library.SetSongDetails(scope, false, false); err != nil {
		t.Fatal(err)
	}
	if homeConsent(t, file, scope).NeedsReaderNotice() {
		t.Fatal("switching off forgot that the owner was told")
	}
}

func clearReaderNotice(t *testing.T, store workspace.Store, scope Scope) {
	t.Helper()
	home, err := store.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	state := home.GetAssistantProgramState()
	state.SongDetailsConsent.ReaderNoticeAt = nil
	home.SetAssistantProgramState(state)
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
}
