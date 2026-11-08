package workspace

import "testing"

// PRD #792 FR9: a Home created under a declaration with the section records
// its words snapshot in the first-creation write; a reused Home is never
// backfilled, and a snapshot for another release is not taken.
func TestNewHomeRecordsTheWordsOfItsPinOnly(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	programs := NewAssistantProgramStore(store)
	homeOwner, _ := splitProviderOwners()
	key := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}
	words := musicWordsSnapshot()
	words.PluginID, words.Version = homeOwner.PluginID, homeOwner.PluginVersion
	home, created, err := programs.EnsureNamedIndependentStationWithWords(key, splitHomeDeclaration(), "Music Home", homeOwner, words)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	saved, err := store.Get(home.ID)
	if err != nil || saved.GetAssistantProgramState().ProjectCardWords == nil ||
		saved.GetAssistantProgramState().ProjectCardWords.Noun != "Song" {
		t.Fatalf("the new Home lacks its words: %+v %v", saved.GetAssistantProgramState().ProjectCardWords, err)
	}

	// Reuse never replaces or backfills.
	other := musicWordsSnapshot()
	other.PluginID, other.Version, other.Noun = homeOwner.PluginID, homeOwner.PluginVersion, "Track"
	again, created, err := programs.EnsureNamedIndependentStationWithWords(key, splitHomeDeclaration(), "Music Home", homeOwner, other)
	if err != nil || created || again.GetAssistantProgramState().ProjectCardWords.Noun != "Song" {
		t.Fatalf("reuse changed the words: %v %+v %v", created, again.GetAssistantProgramState().ProjectCardWords, err)
	}

	// Words of another release than the pin are not taken.
	if got := projectCardWordsForPin(&homeOwner, &ProjectCardWords{PluginID: homeOwner.PluginID, Version: "9.9.9",
		Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs"}); got != nil {
		t.Fatalf("a snapshot of another release was kept: %+v", got)
	}
	if got := projectCardWordsForPin(nil, words); got != nil {
		t.Fatal("a Home without a pin took words")
	}
}
